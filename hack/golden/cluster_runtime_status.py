"""Render ClusterStatus round trips with the Python operator, as golden data for the Go translation."""

import json
import sys
from pathlib import Path
from typing import Any

from qdrant_operator.domain import ClusterStatus

CASES = [
    {"name": "empty", "status": {}},
    {"name": "phase-only", "status": {"phase": "Running"}},
    {
        "name": "full",
        "status": {
            "phase": "Upgrading",
            "replicas": 3,
            "readyReplicas": 2,
            "updatedReplicas": 1,
            "currentRevision": "rev-old",
            "updateRevision": "rev-new",
            "appliedHash": "abc123",
            "version": "v1.15.0",
            "helmRelease": "qdrant-demo",
            "endpoint": "http://qdrant-demo.search.svc:6333",
            "observedGeneration": 7,
            "conditions": [
                {
                    "type": "Ready",
                    "status": "False",
                    "lastTransitionTime": "2026-01-01T10:00:00Z",
                    "reason": "RestartingPod",
                    "message": "pod 2 is restarting",
                },
                {
                    "type": "Available",
                    "status": "True",
                    "lastTransitionTime": "2026-01-01T10:00:00.123456Z",
                },
                {
                    "type": "Offset",
                    "status": "Unknown",
                    "lastTransitionTime": "2026-01-01T12:00:00+02:00",
                },
            ],
        },
    },
    {"name": "terminating", "status": {"phase": "Terminating", "replicas": 1}},
]


def without_empty(data: dict[str, Any]) -> dict[str, Any]:
    return {key: value for key, value in data.items() if value not in (None, 0, "", [])}


def main() -> None:
    out_path = Path(sys.argv[1])
    golden = []
    for case in CASES:
        out = ClusterStatus.from_dict(case["status"]).to_dict()
        out["conditions"] = [without_empty(c) for c in out["conditions"]]
        golden.append({"name": case["name"], "status": case["status"], "out": without_empty(out)})
    out_path.write_text(json.dumps({"cases": golden}, indent=2, sort_keys=True) + "\n")


if __name__ == "__main__":
    main()
