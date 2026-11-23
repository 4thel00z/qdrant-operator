"""Helm post-renderer: pins the StatefulSet partition in the rendered manifests."""

import re
import sys
from typing import Any
from typing import cast

import yaml

DOCUMENT_START = re.compile(r"(?m)^(?=---[ \t]*$)")
COMMENT_OR_SEPARATOR = re.compile(r"^\s*(#.*|---\s*)?$")
STRATEGY_BLOCK = re.compile(r"(?m)^  updateStrategy:.*\n(?:[ ]{3,}.*\n)*")
SPEC_LINE = re.compile(r"(?m)^spec:[ \t]*\n")
USAGE = "usage: qdrant-operator-postrender <statefulset> <partition>"


def mapping(value: Any) -> dict[str, Any]:
    if not isinstance(value, dict):
        return {}
    return {str(key): item for key, item in cast(dict[Any, Any], value).items()}


def is_statefulset(document: Any, name: str) -> bool:
    fields = mapping(document)
    return (
        fields.get("kind") == "StatefulSet" and mapping(fields.get("metadata")).get("name") == name
    )


def head_length(text: str) -> int:
    """Length of the leading separator and comment lines, kept verbatim."""
    lines = text.splitlines(keepends=True)
    kept = 0
    for line in lines:
        if not COMMENT_OR_SEPARATOR.match(line):
            break
        kept += len(line)
    return kept


def strategy(partition: int) -> dict[str, Any]:
    return {"type": "RollingUpdate", "rollingUpdate": {"partition": partition}}


def patch_text(text: str, partition: int) -> str:
    """Swap the updateStrategy lines in place, leaving the rest of the document untouched."""
    block = (
        "  updateStrategy:\n"
        "    type: RollingUpdate\n"
        "    rollingUpdate:\n"
        f"      partition: {partition}\n"
    )
    if STRATEGY_BLOCK.search(text):
        return STRATEGY_BLOCK.sub(lambda _: block, text, count=1)
    return SPEC_LINE.sub(lambda match: match.group(0) + block, text, count=1)


def dump(text: str, partition: int) -> str:
    document: dict[str, Any] = yaml.safe_load(text)
    document.setdefault("spec", {})["updateStrategy"] = strategy(partition)
    return text[: head_length(text)] + yaml.safe_dump(document, sort_keys=False)


def pin_partition(text: str, partition: int) -> str:
    patched = patch_text(text, partition)
    spec = mapping(mapping(parse(patched)).get("spec"))
    if spec.get("updateStrategy") == strategy(partition):
        return patched
    return dump(text, partition)


def parse(text: str) -> Any:
    try:
        return yaml.safe_load(text)
    except yaml.YAMLError:
        return None


def set_partition(stream: str, statefulset: str, partition: int) -> str:
    """Rewrite the named StatefulSet's updateStrategy to RollingUpdate at `partition`.

    Every other document is returned byte for byte.
    """
    return "".join(
        pin_partition(part, partition) if is_statefulset(parse(part), statefulset) else part
        for part in DOCUMENT_START.split(stream)
    )


def main() -> None:
    arguments = sys.argv[1:]
    if len(arguments) != 2 or not arguments[1].isdigit():
        sys.exit(USAGE)
    sys.stdout.write(set_partition(sys.stdin.read(), arguments[0], int(arguments[1])))
