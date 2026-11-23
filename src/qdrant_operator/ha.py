"""Replica coordination: kopf arguments read from the environment."""

from collections.abc import Mapping

WATCH_NAMESPACES_ENV = "WATCH_NAMESPACES"


def namespace_args(environ: Mapping[str, str]) -> list[str]:
    """`--namespace` per entry of WATCH_NAMESPACES, else `--all-namespaces`."""
    names = [name.strip() for name in environ.get(WATCH_NAMESPACES_ENV, "").split(",")]
    watched = [name for name in names if name]
    if not watched:
        return ["--all-namespaces"]
    return [argument for name in watched for argument in ("--namespace", name)]
