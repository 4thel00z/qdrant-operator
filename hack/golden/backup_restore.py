"""Render the pure QdrantRestore outputs with the Python operator, as golden data for the Go port."""

import json
import sys
from collections.abc import Callable
from pathlib import Path
from typing import Any

from qdrant_operator.domain import BackupSpec
from qdrant_operator.domain import Condition
from qdrant_operator.domain import RestorePhase
from qdrant_operator.domain import RestoredCollection
from qdrant_operator.domain import RestoreProgress
from qdrant_operator.domain import RestoreSpec
from qdrant_operator.domain import RestoreStatus
from qdrant_operator.domain import VolumeSnapshotRecord
from qdrant_operator.domain import parse_time
from qdrant_operator.domain import plan_volume_restore
from qdrant_operator.domain import select_collections
from qdrant_operator.domain import indexing_timeout_message

RESTORE_META = {"name": "restore", "namespace": "ns"}
BACKUP_META = {"name": "nightly", "namespace": "ns"}
AVAILABLE = ["a", "b", "c"]


def prune(value: Any) -> Any:
    """Drop what a Go omitempty field would drop: nulls, empty strings and containers, zeros."""
    if isinstance(value, dict):
        pruned = {key: prune(item) for key, item in value.items()}
        return {key: item for key, item in pruned.items() if not empty(item)}
    if isinstance(value, list):
        return [prune(item) for item in value]
    return value


def empty(value: Any) -> bool:
    if isinstance(value, bool):
        return False
    return value is None or value == "" or value == 0 or value == [] or value == {}


def attempt(compute: Callable[[], Any]) -> dict[str, Any]:
    try:
        return {"value": compute()}
    except ValueError as error:
        return {"error": str(error)}


def spec_outputs(spec: RestoreSpec) -> dict[str, Any]:
    backup_ref = spec.backup_ref.to_resource_ref() if spec.backup_ref else None
    return {
        "target_cluster_ref": spec.target_cluster_ref.to_dict(),
        "target_names": {name: spec.target_name(name) for name in AVAILABLE},
        "selection": attempt(lambda: list(spec.select_collections(AVAILABLE))),
        "priority": spec.priority.value,
        "wait_for_indexing": spec.wait_for_indexing,
        "indexing_timeout_seconds": spec.indexing_timeout_seconds,
        "backup_ref": {"name": backup_ref.name, "namespace": backup_ref.namespace, "plural": backup_ref.kind.plural}
        if backup_ref
        else None,
        "source_s3": spec.source_s3.to_dict() if spec.source_s3 else None,
        "source_uri": spec.source_s3.uri() if spec.source_s3 else None,
    }


def spec_case_outputs(case: dict[str, Any]) -> dict[str, Any]:
    try:
        return spec_outputs(RestoreSpec.from_dict(case["spec"], RESTORE_META))
    except ValueError as error:
        return {"error": str(error)}


def timeout_outputs(case: dict[str, Any]) -> dict[str, Any]:
    spec = RestoreSpec.from_dict(
        {
            "targetClusterRef": {"name": "db"},
            "backupRef": {"name": "bk"},
            "indexingTimeoutSeconds": case["timeout"],
        },
        RESTORE_META,
    )
    return {"message": indexing_timeout_message(spec, case["collection"], case["info"])}


def plan_outputs(case: dict[str, Any]) -> dict[str, Any]:
    restore = RestoreSpec.from_dict(case["restore"], RESTORE_META)
    source = BackupSpec.from_dict(case["source"], BACKUP_META)
    records = [VolumeSnapshotRecord.from_dict(r) for r in case["records"]]
    nodes = {int(index): node for index, node in case["nodes"].items()}
    return attempt(
        lambda: list(
            plan_volume_restore(
                restore,
                source,
                records,
                case["target_replicas"],
                case["deployed"],
                case["existing_claims"],
                nodes,
            )
        )
    )


def build_status(data: dict[str, Any]) -> RestoreStatus:
    progress = data.get("progress", {})
    return RestoreStatus(
        phase=RestorePhase(data.get("phase", "Pending")),
        start_time=parse_time(data.get("startTime")),
        completion_time=parse_time(data.get("completionTime")),
        source_backup=data.get("sourceBackup"),
        restored_collections=tuple(
            RestoredCollection(
                name=c["name"],
                status=c["status"],
                original_name=c.get("originalName"),
                size=c.get("size"),
                points_count=c.get("pointsCount"),
                error=c.get("error"),
            )
            for c in data.get("restoredCollections", [])
        ),
        progress=RestoreProgress(
            progress.get("collectionsTotal", 0),
            progress.get("collectionsCompleted", 0),
            progress.get("currentCollection"),
        ),
        error=data.get("error"),
        conditions=tuple(Condition.from_dict(c) for c in data.get("conditions", [])),
    )


def status_outputs(case: dict[str, Any]) -> dict[str, Any]:
    status = build_status(case["status"])
    return {"percentage": status.progress.percentage, "api": prune(status.to_dict())}


def main() -> None:
    cases_path, out_path = Path(sys.argv[1]), Path(sys.argv[2])
    cases = json.loads(cases_path.read_text())
    golden = {
        "available": AVAILABLE,
        "specs": [{**case, "outputs": spec_case_outputs(case)} for case in cases["specs"]],
        "selections": [
            {
                **case,
                "outputs": attempt(
                    lambda case=case: list(select_collections(tuple(case["requested"]), case["available"], case["source"]))
                ),
            }
            for case in cases["selections"]
        ],
        "timeouts": [{**case, "outputs": timeout_outputs(case)} for case in cases["timeouts"]],
        "volume_plans": [{**case, "outputs": plan_outputs(case)} for case in cases["volume_plans"]],
        "statuses": [{**case, "outputs": status_outputs(case)} for case in cases["statuses"]],
    }
    out_path.write_text(json.dumps(golden, indent=2, sort_keys=True) + "\n")


if __name__ == "__main__":
    main()
