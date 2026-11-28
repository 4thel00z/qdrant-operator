"""Render every pure QdrantAccessKey output with the Python operator, as golden data for the Go port."""

import dataclasses
import json
import sys
from datetime import UTC
from datetime import datetime
from datetime import timedelta
from pathlib import Path
from typing import Any

from qdrant_operator.domain import AccessKeySpec
from qdrant_operator.domain import AccessKeyStatus
from qdrant_operator.domain import key_fingerprint
from qdrant_operator.domain import parse_duration

METADATA = {"name": "app-token", "namespace": "tenant-a"}
EPOCH = datetime(1970, 1, 1, tzinfo=UTC)


def micros(value: datetime | None) -> int | None:
    if value is None:
        return None
    return (value - EPOCH) // timedelta(microseconds=1)


def seconds(value: timedelta | None) -> float | None:
    if not value:
        return None
    return value.total_seconds()


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


def duration_output(value: str) -> dict[str, Any]:
    try:
        return {"seconds": parse_duration(value).total_seconds(), "error": False}
    except (ValueError, OverflowError):
        return {"seconds": None, "error": True}


def spec_output(case: dict[str, Any]) -> dict[str, Any]:
    try:
        spec = AccessKeySpec.from_dict(case["spec"], METADATA)
    except ValueError:
        return {"error": True}
    issued = datetime.fromisoformat(case["issued_at"].replace("Z", "+00:00"))
    return {
        "error": False,
        "cluster_ref": dataclasses.asdict(spec.cluster_ref),
        "secret_name": spec.secret_name,
        "access_claim": spec.access_claim(),
        "claims": spec.claims(issued),
        "expires_at_micros": micros(spec.expires_at(issued)),
        "renew_at_micros": micros(spec.renew_at(issued)),
        "ttl_seconds": seconds(spec.ttl),
        "renew_before_seconds": seconds(spec.renew_before),
        "token_secret_ref": dataclasses.asdict(spec.token_secret_ref()),
    }


def status_output(case: dict[str, Any]) -> dict[str, Any]:
    status = AccessKeyStatus.from_dict(case["status"], METADATA["namespace"])
    checks = [
        status.token_current(
            check["generation"],
            check["fingerprint"],
            check["secret_present"],
            datetime.fromisoformat(check["now"].replace("Z", "+00:00")),
        )
        for check in case["checks"]
    ]
    return {
        "api": api_view(status.to_dict()),
        "secret_ref": dataclasses.asdict(status.secret_ref) if status.secret_ref else None,
        "token_current": checks,
    }


def main() -> None:
    cases = json.loads(Path(sys.argv[1]).read_text())
    golden = {
        "durations": [duration_output(value) for value in cases["durations"]],
        "fingerprints": [key_fingerprint(value) for value in cases["fingerprints"]],
        "specs": [spec_output(case) for case in cases["specs"]],
        "statuses": [status_output(case) for case in cases["statuses"]],
    }
    Path(sys.argv[2]).write_text(json.dumps(golden, indent=2, sort_keys=True) + "\n")


if __name__ == "__main__":
    main()
