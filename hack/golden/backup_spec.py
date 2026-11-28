"""Render the pure QdrantBackup outputs with the Python operator, as golden data for the Go port."""

import dataclasses
import json
import sys
from collections.abc import Callable
from dataclasses import replace
from datetime import datetime
from pathlib import Path
from typing import Any

from qdrant_operator.domain import BackupManifest
from qdrant_operator.domain import BackupRecord
from qdrant_operator.domain import BackupSpec
from qdrant_operator.domain import BackupStatus
from qdrant_operator.domain import CollectionBackupStatus
from qdrant_operator.domain import RetentionPolicy
from qdrant_operator.domain import SnapshotRecord
from qdrant_operator.domain import BackupPhase
from qdrant_operator.domain import format_time
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


def attempt(compute: Callable[[], Any]) -> dict[str, Any]:
    try:
        return {"value": compute()}
    except ValueError as error:
        return {"error": str(error)}


def time_or_none(value: datetime | None) -> str | None:
    return format_time(value) if value else None


def secret_ref(ref: Any) -> dict[str, str]:
    return dataclasses.asdict(ref)


def spec_outputs(spec: BackupSpec, completion: str) -> dict[str, Any]:
    storage = spec.storage
    return {
        "cluster_ref": spec.cluster_ref.to_dict(),
        "collections": list(spec.collections),
        "method": spec.method.value,
        "s3_storage": attempt(lambda: spec.s3_storage().to_dict()),
        "access_key_ref": secret_ref(storage.credentials_secret_ref.access_key_ref()) if storage else None,
        "secret_key_ref": secret_ref(storage.credentials_secret_ref.secret_key_ref()) if storage else None,
        "location": attempt(lambda: spec.location),
        "snapshot_selector": spec.snapshot_selector,
        "volume_snapshot_names": [spec.volume_snapshot_name(i) for i in range(3)],
        "volume_snapshot_bodies": [spec.volume_snapshot_body(i) for i in range(2)],
        "root_key": attempt(lambda: spec.root_key),
        "snapshot_key": attempt(lambda: spec.snapshot_key("coll", 1, "s.snapshot")),
        "expires_at": time_or_none(spec.expires_at(parse_time(completion))),
    }


def manifest_outputs(manifest: BackupManifest) -> dict[str, Any]:
    first = manifest.collection_names[0] if manifest.collection_names else "none"
    found = manifest.collection(first)
    return {
        "json": manifest.to_dict(),
        "collection_names": list(manifest.collection_names),
        "size_bytes": manifest.size_bytes,
        "collection_sizes": {c.name: c.size_bytes for c in manifest.collections},
        "first_collection": found.to_dict() if found else None,
        "missing_collection": manifest.collection("zzz") is not None,
    }


def collection_status(data: dict[str, Any]) -> CollectionBackupStatus:
    return CollectionBackupStatus(
        name=data["name"],
        status=data["status"],
        snapshots=tuple(SnapshotRecord.from_dict(s) for s in data["snapshots"]),
        error=data.get("error"),
    )


def status_outputs(case: dict[str, Any]) -> dict[str, Any]:
    status = BackupStatus.from_dict(case["status"])
    status = replace(status, collections=tuple(collection_status(c) for c in case.get("collections", [])))
    return {"finished": status.finished, "api": prune(status.to_dict())}


def record_outputs(case: dict[str, Any]) -> dict[str, Any]:
    record = BackupRecord.from_resource(case["backup"])
    return {"finished": record.finished, "record": prune(record.to_dict())}


def retention_outputs(case: dict[str, Any]) -> dict[str, Any]:
    policy = RetentionPolicy.from_dict(case["policy"])
    backups = [
        BackupRecord(b["name"], parse_time(b["creationTime"]), BackupPhase(b["phase"])) for b in case["backups"]
    ]
    return {"configured": policy.configured, "expired": [b.name for b in policy.expired(backups)]}


def main() -> None:
    cases_path, out_path = Path(sys.argv[1]), Path(sys.argv[2])
    cases = json.loads(cases_path.read_text())
    golden = {
        "specs": [
            {**case, "outputs": spec_outputs(BackupSpec.from_dict(case["spec"], METADATA), case["completion"])}
            for case in cases["specs"]
        ],
        "manifests": [
            {**case, "outputs": manifest_outputs(BackupManifest.from_dict(case["manifest"]))}
            for case in cases["manifests"]
        ],
        "statuses": [{**case, "outputs": status_outputs(case)} for case in cases["statuses"]],
        "records": [{**case, "outputs": record_outputs(case)} for case in cases["records"]],
        "retention": [{**case, "outputs": retention_outputs(case)} for case in cases["retention"]],
    }
    out_path.write_text(json.dumps(golden, indent=2, sort_keys=True) + "\n")


if __name__ == "__main__":
    main()
