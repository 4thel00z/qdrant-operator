"""Render every pure QdrantCollection output with the Python operator, as golden data for the Go port."""

import asyncio
import copy
import json
import sys
from datetime import UTC
from datetime import datetime
from pathlib import Path
from typing import Any

sys.path.insert(0, str(Path(__file__).resolve().parents[2]))

from qdrant_operator import usecases
from qdrant_operator.domain import RESET_GROUPS
from qdrant_operator.domain import CollectionPhase
from qdrant_operator.domain import CollectionSpec
from qdrant_operator.domain import CollectionStatus
from qdrant_operator.domain import QdrantNode
from qdrant_operator.domain import at_default
from qdrant_operator.domain import is_subset
from qdrant_operator.domain import live_optimizers
from qdrant_operator.usecases import ReconcileCollection
from tests.fakes import FakeQdrant

METADATA = {"name": "docs", "namespace": "tenant-a"}
NOW = datetime(2026, 6, 1, 12, 0, 0, tzinfo=UTC)
NODE = QdrantNode("http://qdrant.test:6333")


class RecordingQdrant(FakeQdrant):
    """The in-memory fake, plus the field schema each index creation was given."""

    def __init__(self) -> None:
        super().__init__()
        self.created_schemas: list[Any] = []

    async def create_payload_index(
        self, node: QdrantNode, collection: str, field_name: str, field_schema: Any
    ) -> None:
        self.created_schemas.append(field_schema)
        await super().create_payload_index(node, collection, field_name, field_schema)


def exact(value: Any) -> str:
    return json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=False)


def is_blank(value: Any) -> bool:
    if isinstance(value, bool):
        return False
    return value is None or value == "" or value == [] or value == {} or value == 0


def api_form(value: Any) -> Any:
    """What the API status type keeps: sparse maps, no nulls, empty lists or zero counters."""
    if isinstance(value, dict):
        cleaned = {key: api_form(item) for key, item in value.items()}
        return {key: item for key, item in cleaned.items() if not is_blank(item)}
    if isinstance(value, list):
        return [api_form(item) for item in value]
    return value


def status_form(status: CollectionStatus) -> dict[str, Any]:
    return api_form(status.to_dict())


def index_plan(spec: CollectionSpec, case: dict[str, Any]) -> dict[str, Any]:
    qdrant = RecordingQdrant()
    qdrant.add_collection(NODE.url, spec.collection_name)
    schema = copy.deepcopy(case["payload_schema"])
    qdrant.entry(NODE, spec.collection_name)["payload_schema"] = copy.deepcopy(schema)
    use_case = ReconcileCollection(qdrant, None)  # type: ignore[arg-type]
    managed = asyncio.run(
        use_case.reconcile_indexes(NODE, spec, schema, tuple(case.get("managed_indexes", [])))
    )
    schemas = iter(qdrant.created_schemas)
    operations = [
        {"op": op, "field": field}
        | ({"schema": next(schemas)} if op == "create" else {})
        for op, _, field in qdrant.index_changes
    ]
    return {"operations": operations, "managed": list(managed)}


def alias_plan(spec: CollectionSpec, case: dict[str, Any]) -> dict[str, Any]:
    qdrant = RecordingQdrant()
    qdrant.aliases = dict(case["actual_aliases"])
    use_case = ReconcileCollection(qdrant, None)  # type: ignore[arg-type]
    managed = asyncio.run(
        use_case.reconcile_aliases(NODE, spec, tuple(case.get("managed_aliases", [])))
    )
    actions = [action for batch in qdrant.alias_changes for action in batch]
    return {"actions": actions, "managed": list(managed)}


def shard_key_plan(spec: CollectionSpec, case: dict[str, Any]) -> dict[str, Any]:
    qdrant = RecordingQdrant()
    qdrant.add_collection(NODE.url, spec.collection_name)
    qdrant.entry(NODE, spec.collection_name)["shard_keys"] = list(case["existing_shard_keys"])
    use_case = ReconcileCollection(qdrant, None)  # type: ignore[arg-type]
    keys = asyncio.run(use_case.reconcile_shard_keys(NODE, spec))
    return {
        "create": [body for _, body in qdrant.shard_key_bodies],
        "keys": list(keys),
        "manages": spec.sharding_method == "custom",
    }


def observe(spec: CollectionSpec, case: dict[str, Any]) -> dict[str, Any]:
    usecases.now_utc = lambda: NOW
    status = ReconcileCollection.status_for(
        spec,
        case.get("generation"),
        CollectionStatus.from_dict(case["current"]),
        case["info"],
        CollectionPhase(case["phase"]),
        case["reason"],
        case["message"],
    )
    return status_form(status)


def config_outputs(spec: CollectionSpec, case: dict[str, Any]) -> dict[str, Any]:
    config = case["config"]
    applied = {group: tuple(names) for group, names in case.get("applied", {}).items()}
    resets = spec.resets(config, applied)
    outputs: dict[str, Any] = {
        "immutable_ok": is_subset(spec.immutable_config(), config),
        "update_body": spec.update_body(config),
        "update_body_exact": exact(spec.update_body(config)),
        "update_body_with_resets": spec.update_body(config, resets),
        "update_body_with_resets_exact": exact(spec.update_body(config, resets)),
        "resets": resets,
        "reset_names": [f"{key}.{name}" for key, fields in resets.items() for name in fields],
    }
    return outputs


def spec_outputs(case: dict[str, Any]) -> dict[str, Any]:
    try:
        spec = CollectionSpec.from_dict(case["spec"], METADATA)
    except ValueError:
        return {"error": True}
    outputs: dict[str, Any] = {
        "collection_name": spec.collection_name,
        "cluster_ref": spec.cluster_ref.to_dict(),
        "deletion_policy": spec.deletion_policy.value,
        "single_unnamed": spec.single_unnamed,
        "create_body": spec.create_body(),
        "create_body_exact": exact(spec.create_body()),
        "immutable_config": spec.immutable_config(),
        "mutable_config": spec.mutable_config(),
        "declared_fields": {group: list(names) for group, names in spec.declared_fields().items()},
        "shard_key_bodies": [key.to_body() for key in spec.shard_keys],
        "index_schemas": {index.field_name: index.field_schema() for index in spec.payload_indexes},
        "alias_names": sorted(spec.aliases),
    }
    if "config" in case:
        outputs |= config_outputs(spec, case)
    if "payload_schema" in case:
        outputs["index_plan"] = index_plan(spec, case)
        outputs["index_satisfied"] = {
            index.field_name: index.satisfied_by(case["payload_schema"][index.field_name])
            for index in spec.payload_indexes
            if case["payload_schema"].get(index.field_name)
        }
    if "actual_aliases" in case:
        outputs["alias_plan"] = alias_plan(spec, case)
    if "existing_shard_keys" in case:
        outputs["shard_key_plan"] = shard_key_plan(spec, case)
    if "observe" in case:
        outputs["observed_status"] = observe(spec, case["observe"])
    return outputs


def status_outputs(case: dict[str, Any]) -> dict[str, Any]:
    try:
        status = CollectionStatus.from_dict(case["status"])
    except ValueError:
        return {"error": True}
    return {"status": status_form(status)}


def main() -> None:
    cases = json.loads(Path(sys.argv[1]).read_text())
    golden = {
        "reset_groups": {
            group: {
                "patch_key": reset.patch_key,
                "live_key": reset.live_key,
                "defaults": {name: list(pair) for name, pair in reset.defaults.items()},
            }
            for group, reset in RESET_GROUPS.items()
        },
        "spec_cases": [
            {**case, "outputs": spec_outputs(case)} for case in cases["spec_cases"]
        ],
        "subset_cases": [
            {**case, "want": is_subset(case["desired"], case["actual"])}
            for case in cases["subset_cases"]
        ],
        "optimizer_cases": [
            {**case, "want": live_optimizers(case["config"])} for case in cases["optimizer_cases"]
        ],
        "default_cases": [
            {**case, "want": at_default(case["live"], case["default"])}
            for case in cases["default_cases"]
        ],
        "status_cases": [{**case, "outputs": status_outputs(case)} for case in cases["status_cases"]],
    }
    Path(sys.argv[2]).write_text(json.dumps(golden, indent=2, sort_keys=True) + "\n")


if __name__ == "__main__":
    main()
