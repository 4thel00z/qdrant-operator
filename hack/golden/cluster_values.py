"""Render Helm values with the Python operator for each case, as golden data for the Go port."""

import json
import sys
from pathlib import Path

from qdrant_operator.domain import ClusterSpec

METADATA = {"name": "demo", "namespace": "search"}


def main() -> None:
    cases_path, out_path = Path(sys.argv[1]), Path(sys.argv[2])
    cases = json.loads(cases_path.read_text())
    golden = [
        {
            "name": case["name"],
            "spec": case["spec"],
            "values": ClusterSpec.from_dict(case["spec"], METADATA).to_helm_values(),
        }
        for case in cases
    ]
    out_path.write_text(json.dumps(golden, indent=2, sort_keys=True) + "\n")


if __name__ == "__main__":
    main()
