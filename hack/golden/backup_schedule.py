"""Render the pure QdrantBackupSchedule outputs with the Python operator, as golden data for the Go port."""

import json
import sys
from dataclasses import replace
from datetime import datetime
from pathlib import Path
from typing import Any

from qdrant_operator.domain import BackupPhase
from qdrant_operator.domain import BackupRecord
from qdrant_operator.domain import BackupScheduleSpec
from qdrant_operator.domain import BackupScheduleStatus
from qdrant_operator.domain import format_time
from qdrant_operator.domain import owner_reference
from qdrant_operator.domain import parse_time

METADATA = {"name": "demo", "namespace": "tenant-a"}


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


def time_or_none(value: datetime | None) -> str | None:
    return format_time(value) if value else None


def spec_outputs(spec: BackupScheduleSpec, case: dict[str, Any], owner: dict[str, Any]) -> dict[str, Any]:
    now = parse_time(case["now"])
    if not now:
        raise ValueError(f"case {case['name']} has no now")
    resource_owner = {
        "apiVersion": owner["apiVersion"],
        "kind": owner["kind"],
        "metadata": {"name": owner["name"], "uid": owner["uid"]},
    }
    backup_name = spec.backup_name(parse_time(case["backup_at"]) or now)
    return {
        "label_selector": spec.label_selector,
        "backup_name": backup_name,
        "previous_slot": format_time(spec.previous_slot(now)),
        "next_slot": format_time(spec.next_slot(now)),
        "due": [time_or_none(spec.due(now, parse_time(baseline))) for baseline in case["baselines"]],
        "backup_resource": spec.to_backup_resource(backup_name, resource_owner),
        "owner_reference": owner_reference(resource_owner),
        "suspend": spec.suspend,
        "concurrency_policy": spec.concurrency_policy.value,
        "starting_deadline_seconds": spec.starting_deadline_seconds,
        "delete_backups_with_schedule": spec.delete_backups_with_schedule,
        "retention_configured": spec.retention_policy.configured,
        "method": spec.method.value,
        "collections": list(spec.collections),
    }


def status_outputs(case: dict[str, Any]) -> dict[str, Any]:
    status = BackupScheduleStatus.from_dict(case["status"])
    recent = tuple(
        BackupRecord(
            r["name"],
            parse_time(r["creationTime"]),
            BackupPhase(r["phase"]),
            parse_time(r.get("completionTime")),
            r.get("size"),
        )
        for r in case.get("recent", [])
    )
    return {"api": prune(replace(status, recent_backups=recent).to_dict())}


def main() -> None:
    cases_path, out_path = Path(sys.argv[1]), Path(sys.argv[2])
    cases = json.loads(cases_path.read_text())
    golden = {
        "owner": cases["owner"],
        "specs": [
            {
                **case,
                "outputs": spec_outputs(
                    BackupScheduleSpec.from_dict(case["spec"], METADATA), case, cases["owner"]
                ),
            }
            for case in cases["specs"]
        ],
        "statuses": [{**case, "outputs": status_outputs(case)} for case in cases["statuses"]],
    }
    out_path.write_text(json.dumps(golden, indent=2, sort_keys=True) + "\n")


if __name__ == "__main__":
    main()
