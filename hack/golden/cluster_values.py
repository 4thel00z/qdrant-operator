"""Render every pure QdrantCluster output with the Python operator, as golden data for the Go port."""

import dataclasses
import json
import sys
from pathlib import Path
from typing import Any

from qdrant_operator.domain import ClusterSpec

METADATA = {"name": "demo", "namespace": "search"}

CLAIM_NAMES = [
    "qdrant-storage-qdrant-demo-0",
    "qdrant-storage-qdrant-demo-1",
    "qdrant-storage-qdrant-demo-2",
    "qdrant-snapshots-qdrant-demo-3",
    "qdrant-storage-qdrant-other-5",
    "qdrant-storage-qdrant-demo-x",
]


def outputs(spec: ClusterSpec) -> dict[str, Any]:
    api_key_ref = spec.api_key.resolve_secret_ref(spec.name, spec.namespace)
    return {
        "values": spec.to_helm_values(),
        "claim_sizes": spec.claim_sizes(),
        "departed_claims": spec.departed_claims(CLAIM_NAMES),
        "node_urls": list(spec.node_urls()),
        "service_url": spec.service_url(),
        "pod_label_selector": spec.pod_label_selector,
        "volume_selector": spec.volume_selector,
        "generated_keys": list(spec.generated_keys()),
        "api_key_ref": dataclasses.asdict(api_key_ref) if api_key_ref else None,
    }


def main() -> None:
    cases_path, out_path = Path(sys.argv[1]), Path(sys.argv[2])
    cases = json.loads(cases_path.read_text())
    golden = [
        {
            "name": case["name"],
            "spec": case["spec"],
            "outputs": outputs(ClusterSpec.from_dict(case["spec"], METADATA)),
        }
        for case in cases
    ]
    out_path.write_text(json.dumps({"claim_names": CLAIM_NAMES, "cases": golden}, indent=2, sort_keys=True) + "\n")


if __name__ == "__main__":
    main()
