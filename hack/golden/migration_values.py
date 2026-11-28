"""Render every pure QdrantMigration output with the Python operator, as golden data for the Go port."""

import dataclasses
import json
import sys
from collections.abc import Callable
from pathlib import Path
from typing import Any

from qdrant_operator.domain import CollectionMigration
from qdrant_operator.domain import MigrationSpec
from qdrant_operator.domain import MigrationStatus
from qdrant_operator.domain import TargetOverrides
from qdrant_operator.domain import create_body_from_config
from qdrant_operator.domain import group_by_shard_key
from qdrant_operator.domain import payload_indexes_from_schema
from qdrant_operator.domain import plan_sync_window
from qdrant_operator.domain import point_digest
from qdrant_operator.domain import point_id_order
from qdrant_operator.domain import vector_layout

METADATA = {"name": "move", "namespace": "tenant-a"}


def api_view(value: Any) -> Any:
    """Drop what the Go API types omit when empty: null, zero, empty string and empty list."""
    if isinstance(value, list):
        return [api_view(item) for item in value]
    if not isinstance(value, dict):
        return value
    view = {key: api_view(item) for key, item in value.items()}
    return {
        key: item
        for key, item in view.items()
        if not (item is None or item == "" or item == [] or (type(item) is int and item == 0))
    }


def attempt(build: Callable[[], dict[str, Any]]) -> dict[str, Any]:
    try:
        return {"error": False, **build()}
    except (ValueError, KeyError):
        return {"error": True}


def secret_ref(ref: Any) -> dict[str, Any] | None:
    return dataclasses.asdict(ref) if ref else None


def spec_output(case: dict[str, Any], selections: list[list[str]], names: list[str]) -> dict[str, Any]:
    def build() -> dict[str, Any]:
        spec = MigrationSpec.from_dict(case["spec"], METADATA)
        endpoint = spec.source.endpoint
        return {
            "source_description": spec.source.description,
            "source_cluster_ref": dataclasses.asdict(spec.source.cluster_ref) if spec.source.cluster_ref else None,
            "endpoint": (
                {
                    "url": endpoint.url,
                    "api_key_ref": secret_ref(endpoint.api_key_ref),
                    "ca_ref": secret_ref(endpoint.ca_ref),
                }
                if endpoint
                else None
            ),
            "target_cluster_ref": dataclasses.asdict(spec.target_cluster_ref),
            "collections": list(spec.collections),
            "collection_mapping": dict(spec.collection_mapping),
            "batch_size": spec.batch_size,
            "batch_delay_ms": spec.batch_delay_ms,
            "checkpoint_batches": spec.checkpoint_batches,
            "verify_passes": spec.verify_passes,
            "sync": spec.sync,
            "sync_max_passes": spec.sync_max_passes,
            "create_missing": spec.create_missing,
            "ensure_payload_indexes": spec.ensure_payload_indexes,
            "target_params": spec.target.to_params(),
            "shard_key_params": spec.target.shard_key_params(),
            "shard_key": spec.target.shard_key,
            "shard_key_field": spec.target.shard_key_field,
            "target_names": {name: spec.target_name(name) for name in names},
            "selections": [selection_output(spec, available) for available in selections],
        }

    return attempt(build)


def selection_output(spec: MigrationSpec, available: list[str]) -> dict[str, Any]:
    return attempt(lambda: {"selected": list(spec.select_collections(available))})


def create_body_output(case: dict[str, Any]) -> dict[str, Any]:
    overrides = TargetOverrides.from_dict(case["target"])
    return {"body": create_body_from_config(case["config"], overrides)}


def payload_schema_output(case: dict[str, Any]) -> dict[str, Any]:
    def build() -> dict[str, Any]:
        indexes = sorted(payload_indexes_from_schema(case["schema"]))
        return {"indexes": [{"field": field, "schema": schema} for field, schema in indexes]}

    return attempt(build)


def vector_layout_output(case: dict[str, Any]) -> dict[str, Any]:
    return attempt(lambda: {"layout": vector_layout(case["config"])})


def route_output(case: dict[str, Any]) -> dict[str, Any]:
    def build() -> dict[str, Any]:
        groups = group_by_shard_key(case["points"], TargetOverrides.from_dict(case["target"]))
        return {"groups": [{"key": key, "points": points} for key, points in groups.items()]}

    return attempt(build)


def sync_window_output(case: dict[str, Any]) -> dict[str, Any]:
    changed, stale = plan_sync_window(case["source"], case["target"])
    return {"changed_ids": [point["id"] for point in changed], "stale": stale}


def status_output(case: dict[str, Any]) -> dict[str, Any]:
    status = MigrationStatus.from_dict(case["status"])
    resumes: dict[str, Any] = {}
    for name in case["resume_inputs"]:
        found = status.resume_point(name)
        resumes[name] = api_view(found.to_dict()) if found else None
    return {
        "api": api_view(status.to_dict()),
        "progress": status.progress.to_dict(),
        "resume_points": resumes,
    }


def main() -> None:
    cases = json.loads(Path(sys.argv[1]).read_text())
    golden = {
        "specs": [
            spec_output(case, cases["selection_inputs"], cases["target_name_inputs"])
            for case in cases["specs"]
        ],
        "create_bodies": [create_body_output(case) for case in cases["create_bodies"]],
        "payload_schemas": [payload_schema_output(case) for case in cases["payload_schemas"]],
        "vector_layouts": [vector_layout_output(case) for case in cases["vector_layouts"]],
        "routes": [route_output(case) for case in cases["routes"]],
        "point_orders": [sorted(ids, key=point_id_order) for ids in cases["point_orders"]],
        "digests": [point_digest(case["point"]) for case in cases["digests"]],
        "sync_windows": [sync_window_output(case) for case in cases["sync_windows"]],
        "statuses": [status_output(case) for case in cases["statuses"]],
    }
    Path(sys.argv[2]).write_text(json.dumps(golden, indent=2, sort_keys=True) + "\n")


if __name__ == "__main__":
    main()
