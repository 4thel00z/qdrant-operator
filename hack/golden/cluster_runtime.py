"""Render the pure QdrantCluster runtime rules with the Python operator, as golden data for the Go port."""

import itertools
import json
import sys
from pathlib import Path
from typing import Any

from qdrant_operator.domain import ClusterHealth
from qdrant_operator.domain import PodRevision
from qdrant_operator.domain import PodState
from qdrant_operator.domain import RolloutSnapshot
from qdrant_operator.domain import RolloutStrategy
from qdrant_operator.domain import StatefulSetStatus
from qdrant_operator.domain import Zone
from qdrant_operator.domain import data_claim_name
from qdrant_operator.domain import describe_problems
from qdrant_operator.domain import hold_partition
from qdrant_operator.domain import next_partition
from qdrant_operator.domain import plan_volumes
from qdrant_operator.domain import pod_revisions
from qdrant_operator.domain import released_pods_settled
from qdrant_operator.domain import rollout_wait

OLD = "rev-old"
NEW = "rev-new"
STRATEGIES = [RolloutStrategy.READINESS, RolloutStrategy.SHARD_HEALTH]


def pod_dict(pod: PodRevision) -> dict[str, Any]:
    return {"ordinal": pod.ordinal, "revision": pod.revision, "ready": pod.ready}


def wait_dict(snapshot: RolloutSnapshot) -> dict[str, str] | None:
    wait = rollout_wait(snapshot)
    return {"reason": wait.reason, "message": wait.message} if wait else None


def snapshot_case(
    name: str,
    partition: int,
    replicas: int,
    update_revision: str | None,
    pods: list[PodRevision],
    problems: list[str],
) -> dict[str, Any]:
    snapshot = RolloutSnapshot(
        partition, replicas, update_revision, tuple(pods), ClusterHealth(tuple(problems))
    )
    return {
        "name": name,
        "partition": partition,
        "replicas": replicas,
        "update_revision": update_revision,
        "pods": [pod_dict(p) for p in pods],
        "problems": problems,
        "outputs": {
            "behind": list(snapshot.behind()),
            "held_at": snapshot.held_at,
            "wait": wait_dict(snapshot),
            "next_readiness": next_partition(RolloutStrategy.READINESS, snapshot),
            "next_shard_health": next_partition(RolloutStrategy.SHARD_HEALTH, snapshot),
        },
    }


def snapshot_cases() -> list[dict[str, Any]]:
    cases = []
    for replicas in range(0, 4):
        for revisions in itertools.product([OLD, NEW], repeat=replicas):
            for ready in itertools.product([True, False], repeat=replicas):
                for partition in range(0, replicas + 2):
                    for problems in ([], ["shard 1 of docs is Dead"]):
                        pods = [
                            PodRevision(o, r, f) for o, (r, f) in enumerate(zip(revisions, ready))
                        ]
                        flags = "".join("t" if f else "f" for f in ready)
                        label = f"r{replicas}-p{partition}-{'-'.join(revisions)}-{flags}-{len(problems)}"
                        cases.append(snapshot_case(label, partition, replicas, NEW, pods, problems))
    gone = [PodRevision(0, OLD, True), PodRevision(1, None, False), PodRevision(2, None, False)]
    cases.append(snapshot_case("missing-pods", 3, 3, NEW, gone, []))
    olds = [PodRevision(o, OLD, True) for o in range(3)]
    cases.append(snapshot_case("no-update-revision", 1, 3, None, olds, []))
    cases.append(snapshot_case("partition-above-replicas", 5, 3, NEW, olds, []))
    many = [f"problem {i}" for i in range(13)]
    mixed = [PodRevision(o, OLD if o < 2 else NEW, True) for o in range(3)]
    cases.append(snapshot_case("many-problems", 2, 3, NEW, mixed, many))
    return cases


def pod_revision_cases() -> list[dict[str, Any]]:
    release = "qdrant-demo"
    inputs = [
        ("empty", 3, []),
        ("partial", 3, [PodState(f"{release}-0", OLD, True)]),
        ("extra-ordinals-ignored", 2, [PodState(f"{release}-{o}", NEW, True) for o in range(4)]),
        (
            "foreign-names",
            2,
            [
                PodState("other-0", NEW, True),
                PodState(f"{release}-x", NEW, True),
                PodState(f"{release}-1-2", NEW, True),
                PodState(f"{release}-", NEW, True),
                PodState(f"{release}-1", None, False),
            ],
        ),
        ("zero-replicas", 0, [PodState(f"{release}-0", NEW, True)]),
        (
            "prefix-collision",
            2,
            [PodState("qdrant-demo-2-0", NEW, True), PodState(f"{release}-0", OLD, False)],
        ),
        (
            "duplicate-last-wins",
            1,
            [PodState(f"{release}-0", OLD, False), PodState(f"{release}-0", NEW, True)],
        ),
    ]
    return [
        {
            "name": name,
            "release": release,
            "replicas": replicas,
            "pods": [{"name": p.name, "revision": p.revision, "ready": p.ready} for p in pods],
            "out": [pod_dict(p) for p in pod_revisions(release, replicas, pods)],
        }
        for name, replicas, pods in inputs
    ]


def describe_cases() -> list[dict[str, Any]]:
    cases = []
    for count in (0, 1, 9, 10, 11, 25):
        problems = [f"p{i}" for i in range(count)]
        cases.append(
            {"name": f"{count}-problems", "problems": problems, "out": describe_problems(problems)}
        )
    return cases


def hold_cases() -> list[dict[str, Any]]:
    return [
        {"strategy": strategy.value, "replicas": replicas, "out": hold_partition(strategy, replicas)}
        for strategy in STRATEGIES
        for replicas in (0, 1, 3)
    ]


def settled_cases() -> list[dict[str, Any]]:
    cases = []
    for update_revision in (NEW, None):
        for partition in range(0, 4):
            for revisions in itertools.product([OLD, NEW], repeat=3):
                for ready in itertools.product([True, False], repeat=3):
                    pods = [
                        PodRevision(o, r, f) for o, (r, f) in enumerate(zip(revisions, ready))
                    ]
                    cases.append(
                        {
                            "partition": partition,
                            "update_revision": update_revision,
                            "pods": [pod_dict(p) for p in pods],
                            "out": released_pods_settled(partition, update_revision, pods),
                        }
                    )
    return cases


def statefulset_cases() -> list[dict[str, Any]]:
    cases = []
    for current, update in itertools.product([None, OLD, NEW], repeat=2):
        status = StatefulSetStatus(3, 3, 3, current, update)
        cases.append({"current_revision": current, "update_revision": update, "out": status.rolling})
    return cases


def volume_cases() -> list[dict[str, Any]]:
    storage = "qdrant-storage"
    snapshots = "qdrant-snapshots"
    inputs = [
        ("grow", {storage: "40Gi"}, {storage: "5Gi"}),
        ("same-unit-mix", {storage: "1024Mi"}, {storage: "1Gi"}),
        ("equal", {storage: "10Gi"}, {storage: "10Gi"}),
        ("claim-added", {snapshots: "50Gi", storage: "10Gi"}, {storage: "10Gi"}),
        ("claim-removed", {storage: "10Gi"}, {snapshots: "50Gi", storage: "10Gi"}),
        ("claim-swapped", {"a": "1Gi"}, {"b": "1Gi"}),
        ("shrink", {storage: "5Gi"}, {storage: "10Gi"}),
        ("shrink-two", {snapshots: "1Gi", storage: "1Gi"}, {snapshots: "2Gi", storage: "2Gi"}),
        (
            "grow-both",
            {snapshots: "60Gi", storage: "20Gi"},
            {snapshots: "50Gi", storage: "10Gi"},
        ),
        ("grow-and-add", {snapshots: "50Gi", storage: "20Gi"}, {storage: "10Gi"}),
        ("bad-desired", {storage: "lots"}, {storage: "10Gi"}),
        ("bad-live", {storage: "10Gi"}, {storage: "lots"}),
        ("empty", {}, {}),
        ("decimal-units", {storage: "1.5Gi"}, {storage: "1Gi"}),
    ]
    cases = []
    for name, desired, live in inputs:
        case: dict[str, Any] = {"name": name, "desired": desired, "live": live}
        try:
            plan = plan_volumes(desired, live)
            case["out"] = {"grow": [list(g) for g in plan.grow], "recreate": plan.recreate}
        except ValueError:
            case["error"] = True
        cases.append(case)
    return cases


def claim_cases() -> list[dict[str, Any]]:
    return [
        {"cluster": c, "ordinal": o, "out": data_claim_name(c, o)}
        for c in ("demo", "a-b")
        for o in (0, 7, 12)
    ]


def zone_cases() -> list[dict[str, Any]]:
    inputs = [
        {"zone": "z1", "zoneLabel": "topology.kubernetes.io/zone"},
        {"zone": "z1"},
        {"zoneLabel": "l"},
        {},
        {"zone": "", "zoneLabel": "l"},
    ]
    cases = []
    for data in inputs:
        zone = Zone.from_dict(data)
        cases.append(
            {
                "zone": data.get("zone", ""),
                "label": data.get("zoneLabel", ""),
                "out": {"label": zone.label, "name": zone.name} if zone else None,
            }
        )
    return cases


def main() -> None:
    out_path = Path(sys.argv[1])
    golden = {
        "snapshots": snapshot_cases(),
        "pod_revisions": pod_revision_cases(),
        "describe_problems": describe_cases(),
        "hold_partition": hold_cases(),
        "released_pods_settled": settled_cases(),
        "statefulset_rolling": statefulset_cases(),
        "volume_plans": volume_cases(),
        "data_claim_names": claim_cases(),
        "zones": zone_cases(),
    }
    out_path.write_text(json.dumps(golden, sort_keys=True, separators=(",", ":")) + "\n")


if __name__ == "__main__":
    main()
