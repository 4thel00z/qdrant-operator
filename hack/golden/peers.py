"""Render the pure peer and shard planners with the Python operator, as golden data for the Go port."""

import json
import random
import sys
from pathlib import Path
from typing import Any

from qdrant_operator.peers import ClusterTopology
from qdrant_operator.peers import CollectionShards
from qdrant_operator.peers import Peer
from qdrant_operator.peers import Replica
from qdrant_operator.peers import ShardOperation
from qdrant_operator.peers import ShardOperationKind
from qdrant_operator.peers import ShardPlan
from qdrant_operator.peers import dead_peers
from qdrant_operator.peers import departing_peers
from qdrant_operator.peers import least_loaded
from qdrant_operator.peers import peer_load
from qdrant_operator.peers import plan_evacuation
from qdrant_operator.peers import plan_rebalance
from qdrant_operator.peers import plan_recovery
from qdrant_operator.peers import replaced_peer
from qdrant_operator.peers import settled
from qdrant_operator.peers import surviving_peers

RELEASE = "qdrant-db"
BIG = 18446744073709551615
PEER_IDS = [10, 11, 12, 13, 201, 663769463945476, 9007199254740993, BIG]
STATES = ["Active", "Active", "Active", "Dead", "Partial", "Initializing"]


def uri(release: str, ordinal: int) -> str:
    return f"http://{release}-{ordinal}.{release}-headless.ns.svc.cluster.local:6335/"


def shard_dict(shards: CollectionShards) -> dict[str, Any]:
    return {
        "collection": shards.collection,
        "replicas": [
            {"shard_id": r.shard_id, "peer_id": r.peer_id, "state": r.state}
            for r in shards.replicas
        ],
        "replication_factor": shards.replication_factor,
        "transfers": shards.transfers,
    }


def operation_dict(op: ShardOperation) -> dict[str, Any]:
    return {
        "kind": op.kind.value,
        "collection": op.collection,
        "shard_id": op.shard_id,
        "peer_id": op.peer_id,
        "target_peer_id": op.target_peer_id,
        "body": op.to_body(),
        "describe": op.describe(),
    }


def plan_dict(plan: ShardPlan) -> dict[str, Any]:
    return {
        "operations": [operation_dict(o) for o in plan.operations],
        "lost": [{"collection": x.collection, "shard_id": x.shard_id} for x in plan.lost],
        "lost_described": [x.describe() for x in plan.lost],
        "in_flight": plan.in_flight,
        "blocked": list(plan.blocked),
    }


def build(
    name: str,
    replicas: list[tuple[int, int, str]],
    rf: int = 1,
    transfers: int = 0,
    collection: str = "docs",
) -> CollectionShards:
    return CollectionShards(
        collection, tuple(Replica(s, p, st) for s, p, st in replicas), rf, transfers
    )


def random_collection(rng: random.Random, name: str, pool: list[int]) -> CollectionShards:
    replicas: list[Replica] = []
    for shard_id in range(rng.randint(0, 4)):
        for peer_id in rng.sample(pool, rng.randint(1, min(3, len(pool)))):
            state = "Active" if rng.random() < 0.8 else rng.choice(STATES)
            replicas.append(Replica(shard_id, peer_id, state))
    rng.shuffle(replicas)
    transfers = 1 if rng.random() < 0.12 else 0
    return CollectionShards(name, tuple(replicas), rng.randint(1, 3), transfers)


def random_survey(rng: random.Random, pool: list[int]) -> list[CollectionShards]:
    return [random_collection(rng, f"c{i}", pool) for i in range(rng.randint(0, 4))]


def hand_surveys() -> list[tuple[str, list[CollectionShards]]]:
    a = "Active"
    return [
        ("empty", []),
        ("unique-replicas", [build("", [(0, 10, a), (1, 11, a), (2, 12, a)])]),
        ("redundant-rf2", [build("", [(0, 10, a), (0, 11, a), (0, 12, a)], rf=2)]),
        ("below-rf", [build("", [(0, 10, a), (0, 12, a)], rf=2)]),
        ("every-survivor-holds", [build("", [(0, 10, a), (0, 11, a), (0, 12, a)], rf=3)]),
        ("transfer", [build("", [(0, 12, a)], transfers=1)]),
        ("not-active", [build("", [(0, 12, "Dead")])]),
        ("peer-once", [build("", [(0, 12, a), (1, 12, a)])]),
        ("kept-not-active", [build("", [(0, 10, "Dead"), (0, 12, a)], rf=1)]),
        ("recovery-short", [build("", [(0, 10, a), (0, 11, "Dead")], rf=2)]),
        ("recovery-met", [build("", [(0, 10, a), (0, 201, a), (0, 11, "Dead")], rf=2)]),
        ("recovery-lost", [build("", [(0, 11, a)], rf=1)]),
        (
            "rebalance-drop",
            [build("", [(0, 10, a), (0, 11, a), (0, 12, a), (1, 10, a), (1, 11, a)], rf=2)],
        ),
        ("rebalance-replicate", [build("", [(0, 10, a), (1, 11, a), (1, 12, a)], rf=2)]),
        ("rebalance-move", [build("", [(0, 10, a), (1, 10, a), (2, 11, a), (3, 11, a)])]),
        ("rebalance-even", [build("", [(0, 10, a), (1, 11, a), (2, 12, a), (3, 10, a)])]),
        (
            "rebalance-blocked",
            [
                build("", [(0, 10, a), (1, 10, a), (2, 10, a)], transfers=1),
                CollectionShards("other", (Replica(0, 10, a), Replica(0, 99, a)), 1, 0),
            ],
        ),
        (
            "rebalance-peer-once",
            [
                build("", [(0, 10, a), (1, 10, a), (2, 10, a)]),
                CollectionShards("other", tuple(Replica(s, 10, a) for s in range(3)), 1, 0),
            ],
        ),
        ("large-ids", [build("", [(0, BIG, a), (1, 663769463945476, a), (2, 9007199254740993, a)])]),
    ]


def main() -> None:
    out_path = Path(sys.argv[1])
    rng = random.Random(20260807)
    pool_sets: list[list[int]] = [
        [10, 11, 12],
        [10, 11, 12, 13],
        [10, 11, 12, 13, 201],
        [10, 663769463945476, 9007199254740993, BIG],
    ]

    peers_cases: list[dict[str, Any]] = []
    for release, other in [(RELEASE, "other"), ("a.b", "axb"), ("q+", "q")]:
        for ordinal in (0, 7, 12):
            for text in (
                uri(release, ordinal),
                f"http://{release}-{ordinal}x.svc:6335/",
                f"http://{other}-{ordinal}.svc:6335/",
                f"http://{release}-.svc:6335/",
                "http://external:6335/",
                f"https://{release}-{ordinal}.svc",
                f"{release}-{ordinal}.svc",
            ):
                out = Peer(1, text).ordinal(release)
                peers_cases.append({"release": release, "uri": text, "ordinal": out})

    topologies: list[dict[str, Any]] = []
    topo_inputs: list[tuple[list[tuple[int, str]], int, dict[int, int]]] = []
    foreign = [(99, "http://external:6335/")]
    for replicas in (0, 1, 2, 3, 4):
        for _ in range(12):
            members = [(PEER_IDS[i], uri(RELEASE, i)) for i in range(rng.randint(0, 5))]
            if rng.random() < 0.5:
                members.append((201, uri(RELEASE, rng.randint(0, 3))))
            if rng.random() < 0.4:
                members += foreign
            pods = {o: rng.choice(PEER_IDS) for o in range(replicas) if rng.random() < 0.9}
            if rng.random() < 0.5:
                pods = {o: members[o][0] for o in range(min(replicas, len(members)))}
            topo_inputs.append((members, replicas, pods))
    topo_inputs.append(([(10, uri(RELEASE, 0)), (11, uri(RELEASE, 1)), (201, uri(RELEASE, 1))], 2, {0: 10, 1: 201}))
    topo_inputs.append(([(10, uri(RELEASE, 0)), (11, uri(RELEASE, 1)), (12, uri(RELEASE, 2))], 2, {0: 10, 1: 11}))
    topo_inputs.append(([(10, uri(RELEASE, 0)), (11, uri(RELEASE, 1)), (201, uri(RELEASE, 1))], 2, {0: 10}))
    topo_inputs.append(([(10, uri(RELEASE, 0)), (99, "http://external:6335/")], 1, {0: 10}))
    for members, replicas, pods in topo_inputs:
        topology = ClusterTopology(members[0][0] if members else 0, tuple(Peer(p, u) for p, u in members))
        topologies.append(
            {
                "release": RELEASE,
                "replicas": replicas,
                "peers": [{"peer_id": p, "uri": u} for p, u in members],
                "pods": {str(k): v for k, v in pods.items()},
                "departing": sorted(departing_peers(topology, RELEASE, replicas)),
                "surviving": list(surviving_peers(topology, RELEASE, replicas)),
                "dead": sorted(dead_peers(topology, pods, RELEASE, replicas)),
            }
        )

    logs = [
        "Error: peer URI http://qdrant-rst-1.qdrant-rst-headless:6335/ already used by peer "
        "663769463945476 which still has shards",
        "Qdrant is ready",
        "",
        "already used by peer 18446744073709551615 x",
        "already used by peer 7 and already used by peer 9",
        "already used by peer x",
    ]
    replaced = [{"log": log, "out": replaced_peer(log)} for log in logs]

    evacuation: list[dict[str, Any]] = []
    recovery: list[dict[str, Any]] = []
    rebalance: list[dict[str, Any]] = []
    cases: list[tuple[str, list[CollectionShards], list[int], list[int], list[int], list[int], list[int]]] = []
    for name, survey in hand_surveys():
        live_all = [10, 11] if name == "rebalance-blocked" else [10, 11, 12]
        if name == "large-ids":
            cases.append((name, survey, [BIG], [663769463945476, 9007199254740993, 5], [BIG], [663769463945476, 5], [BIG, 663769463945476, 9007199254740993, 5]))
            continue
        cases.append((name, survey, [12], [10, 11], [11], [10, 201], live_all))
    for index in range(200):
        pool = pool_sets[index % len(pool_sets)] + ([99] if index % 7 == 0 else [])
        survey = random_survey(rng, pool)
        shuffled = pool[:]
        rng.shuffle(shuffled)
        cut = rng.randint(0, len(shuffled))
        live_all = shuffled if rng.random() < 0.7 else shuffled[: max(1, len(shuffled) - 1)]
        cases.append((f"random-{index}", survey, shuffled[:cut], shuffled[cut:], shuffled[:cut], shuffled[cut:], live_all))

    surveys = [(name, survey, [BIG, 10, 11]) for name, survey, *_ in cases]
    for name, survey, leaving, staying, dead, live, live_all in cases:
        surv = [shard_dict(s) for s in survey]
        evacuation.append(
            {
                "name": name,
                "survey": surv,
                "leaving": leaving,
                "staying": staying,
                "out": plan_dict(plan_evacuation(survey, frozenset(leaving), staying)),
            }
        )
        recovery.append(
            {
                "name": name,
                "survey": surv,
                "dead": dead,
                "live": live,
                "out": plan_dict(plan_recovery(survey, frozenset(dead), live)),
            }
        )
        rebalance.append(
            {
                "name": name,
                "survey": surv,
                "live": live_all,
                "out": plan_dict(plan_rebalance(survey, live_all)),
                "settled": [settled(s) for s in survey],
            }
        )

    loads: list[dict[str, Any]] = []
    for name, survey, pool in surveys[:60]:
        ids = rng.sample(PEER_IDS, rng.randint(0, 5)) + ([10, 10] if rng.random() < 0.2 else [])
        load = peer_load(survey, ids)
        excluded = rng.sample(PEER_IDS, rng.randint(0, 4))
        loads.append(
            {
                "survey": [shard_dict(s) for s in survey],
                "peer_ids": ids,
                "load": {str(k): v for k, v in load.items()},
                "excluded": excluded,
                "least_loaded": least_loaded(load, excluded),
                "inactive": [list(s.inactive()) for s in survey],
                "shard_ids": [list(s.shard_ids()) for s in survey],
            }
        )

    operations: list[dict[str, Any]] = []
    for kind in ShardOperationKind:
        for target in (None, 11, BIG):
            operations.append(operation_dict(ShardOperation(kind, "docs", 3, 10, target)))

    golden = {
        "ordinals": peers_cases,
        "topologies": topologies,
        "replaced": replaced,
        "evacuation": evacuation,
        "recovery": recovery,
        "rebalance": rebalance,
        "loads": loads,
        "operations": operations,
    }
    out_path.write_text(json.dumps(golden, indent=1, sort_keys=True) + "\n")


if __name__ == "__main__":
    main()
