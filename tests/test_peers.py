from qdrant_operator.peers import ClusterTopology
from qdrant_operator.peers import CollectionShards
from qdrant_operator.peers import Peer
from qdrant_operator.peers import Replica
from qdrant_operator.peers import ShardLoss
from qdrant_operator.peers import ShardOperation
from qdrant_operator.peers import ShardOperationKind
from qdrant_operator.peers import ShardPlan
from qdrant_operator.peers import dead_peers
from qdrant_operator.peers import departing_peers
from qdrant_operator.peers import plan_evacuation
from qdrant_operator.peers import plan_rebalance
from qdrant_operator.peers import plan_recovery
from qdrant_operator.peers import replaced_peer
from qdrant_operator.peers import surviving_peers

RELEASE = "qdrant-db"


def uri(ordinal: int) -> str:
    return f"http://{RELEASE}-{ordinal}.{RELEASE}-headless.ns.svc.cluster.local:6335/"


def topology(*peers: tuple[int, int]) -> ClusterTopology:
    return ClusterTopology(peers[0][0], tuple(Peer(peer_id, uri(o)) for peer_id, o in peers))


def shards(*replicas: tuple[int, int, str], rf: int = 1, transfers: int = 0) -> CollectionShards:
    return CollectionShards(
        "docs", tuple(Replica(s, p, state) for s, p, state in replicas), rf, transfers
    )


def test_peer_ordinal_comes_from_the_pod_name_in_the_uri() -> None:
    assert Peer(1, uri(7)).ordinal(RELEASE) == 7
    assert Peer(1, "http://other-0.svc:6335/").ordinal(RELEASE) is None
    assert Peer(1, f"http://{RELEASE}-1x.svc:6335/").ordinal(RELEASE) is None


def test_operations_serialize_to_the_cluster_api_bodies() -> None:
    move = ShardOperation(ShardOperationKind.MOVE, "docs", 3, 10, 11)
    replicate = ShardOperation(ShardOperationKind.REPLICATE, "docs", 3, 10, 11)
    drop = ShardOperation(ShardOperationKind.DROP, "docs", 3, 10)

    assert move.to_body() == {"move_shard": {"shard_id": 3, "from_peer_id": 10, "to_peer_id": 11}}
    assert replicate.to_body() == {
        "replicate_shard": {"shard_id": 3, "from_peer_id": 10, "to_peer_id": 11}
    }
    assert drop.to_body() == {"drop_replica": {"shard_id": 3, "peer_id": 10}}


def test_departing_and_surviving_peers_follow_the_replica_count() -> None:
    topo = topology((10, 0), (11, 1), (12, 2))

    assert departing_peers(topo, RELEASE, 2) == frozenset({12})
    assert surviving_peers(topo, RELEASE, 2) == (10, 11)


def test_evacuation_moves_unique_replicas_to_the_least_loaded_survivor() -> None:
    survey = [shards((0, 10, "Active"), (1, 11, "Active"), (2, 12, "Active"))]

    plan = plan_evacuation(survey, frozenset({12}), [10, 11])

    assert plan.operations == (ShardOperation(ShardOperationKind.MOVE, "docs", 2, 12, 10),)
    assert plan.lost == ()


def test_evacuation_drops_replicas_that_survivors_hold_up_to_the_replication_factor() -> None:
    survey = [shards((0, 10, "Active"), (0, 11, "Active"), (0, 12, "Active"), rf=2)]

    plan = plan_evacuation(survey, frozenset({12}), [10, 11])

    assert plan.operations == (ShardOperation(ShardOperationKind.DROP, "docs", 0, 12),)


def test_evacuation_moves_a_replica_the_survivors_hold_below_the_replication_factor() -> None:
    survey = [shards((0, 10, "Active"), (0, 12, "Active"), rf=2)]

    plan = plan_evacuation(survey, frozenset({12}), [10, 11])

    assert plan.operations == (ShardOperation(ShardOperationKind.MOVE, "docs", 0, 12, 11),)


def test_evacuation_drops_when_every_survivor_already_holds_the_shard() -> None:
    survey = [shards((0, 10, "Active"), (0, 11, "Active"), (0, 12, "Active"), rf=3)]

    plan = plan_evacuation(survey, frozenset({12}), [10, 11])

    assert plan.operations == (ShardOperation(ShardOperationKind.DROP, "docs", 0, 12),)


def test_evacuation_waits_for_running_transfers() -> None:
    survey = [shards((0, 12, "Active"), transfers=1)]

    plan = plan_evacuation(survey, frozenset({12}), [10, 11])

    assert plan.operations == ()
    assert plan.blocked == ("docs",)
    assert plan.in_flight == 1


def test_evacuation_will_not_move_a_replica_that_is_not_active() -> None:
    survey = [shards((0, 12, "Dead"))]

    plan = plan_evacuation(survey, frozenset({12}), [10, 11])

    assert plan.operations == ()
    assert plan.lost == (ShardLoss("docs", 0),)


def test_evacuation_uses_a_peer_once_per_plan() -> None:
    survey = [shards((0, 12, "Active"), (1, 12, "Active"))]

    plan = plan_evacuation(survey, frozenset({12}), [10, 11])

    assert [(o.shard_id, o.target_peer_id) for o in plan.operations] == [(0, 10)]


def test_dead_peer_is_the_old_id_at_a_rebuilt_pods_address() -> None:
    topo = topology((10, 0), (11, 1), (201, 1))

    assert dead_peers(topo, {0: 10, 1: 201}, RELEASE, 2) == frozenset({11})


def test_dead_peers_include_peers_beyond_the_replica_count() -> None:
    topo = topology((10, 0), (11, 1), (12, 2))

    assert dead_peers(topo, {0: 10, 1: 11}, RELEASE, 2) == frozenset({12})


def test_dead_peers_are_not_judged_unless_every_pod_reported() -> None:
    topo = topology((10, 0), (11, 1), (201, 1))

    assert dead_peers(topo, {0: 10}, RELEASE, 2) == frozenset()


def test_foreign_peers_are_never_dead() -> None:
    topo = ClusterTopology(10, (Peer(10, uri(0)), Peer(99, "http://external:6335/")))

    assert dead_peers(topo, {0: 10}, RELEASE, 1) == frozenset()


def test_recovery_replicates_from_an_active_replica_to_a_peer_without_one() -> None:
    survey = [shards((0, 10, "Active"), (0, 11, "Dead"), rf=2)]

    plan = plan_recovery(survey, frozenset({11}), [10, 201])

    assert plan.operations == (ShardOperation(ShardOperationKind.REPLICATE, "docs", 0, 10, 201),)


def test_recovery_is_done_once_live_replicas_meet_the_replication_factor() -> None:
    survey = [shards((0, 10, "Active"), (0, 201, "Active"), (0, 11, "Dead"), rf=2)]

    assert plan_recovery(survey, frozenset({11}), [10, 201]).operations == ()


def test_recovery_reports_shards_with_no_live_active_replica() -> None:
    survey = [shards((0, 11, "Active"), rf=1)]

    plan = plan_recovery(survey, frozenset({11}), [10, 201])

    assert plan.operations == ()
    assert plan.lost == (ShardLoss("docs", 0),)


def test_replaced_peer_is_read_from_the_join_error() -> None:
    log = (
        "Error: peer URI http://qdrant-rst-1.qdrant-rst-headless:6335/ already used by peer "
        "663769463945476 which still has shards"
    )

    assert replaced_peer(log) == 663769463945476
    assert replaced_peer("Qdrant is ready") is None


def test_rebalance_drops_a_replica_beyond_the_replication_factor_from_the_busiest_holder() -> None:
    survey = [
        shards(
            (0, 10, "Active"),
            (0, 11, "Active"),
            (0, 12, "Active"),
            (1, 10, "Active"),
            (1, 11, "Active"),
            rf=2,
        )
    ]

    plan = plan_rebalance(survey, [10, 11, 12])

    assert plan.operations == (ShardOperation(ShardOperationKind.DROP, "docs", 0, 11),)


def test_rebalance_replicates_a_shard_short_of_the_replication_factor() -> None:
    survey = [shards((0, 10, "Active"), (1, 11, "Active"), (1, 12, "Active"), rf=2)]

    plan = plan_rebalance(survey, [10, 11, 12])

    assert plan.operations == (ShardOperation(ShardOperationKind.REPLICATE, "docs", 0, 10, 11),)


def test_rebalance_moves_replicas_onto_a_new_empty_peer() -> None:
    survey = [shards((0, 10, "Active"), (1, 10, "Active"), (2, 11, "Active"), (3, 11, "Active"))]

    plan = plan_rebalance(survey, [10, 11, 12])

    assert plan.operations == (ShardOperation(ShardOperationKind.MOVE, "docs", 2, 11, 12),)


def test_rebalance_leaves_an_even_spread_alone() -> None:
    survey = [shards((0, 10, "Active"), (1, 11, "Active"), (2, 12, "Active"), (3, 10, "Active"))]

    assert plan_rebalance(survey, [10, 11, 12]) == ShardPlan()


def test_rebalance_skips_collections_that_are_moving_or_hold_a_replica_on_a_dead_peer() -> None:
    moving = shards((0, 10, "Active"), (1, 10, "Active"), (2, 10, "Active"), transfers=1)
    orphaned = CollectionShards("other", (Replica(0, 10, "Active"), Replica(0, 99, "Active")), 1, 0)

    plan = plan_rebalance([moving, orphaned], [10, 11])

    assert plan.operations == ()
    assert plan.blocked == ("docs", "other")
    assert plan.in_flight == 1


def test_rebalance_uses_a_peer_once_across_collections() -> None:
    first = shards((0, 10, "Active"), (1, 10, "Active"), (2, 10, "Active"))
    second = CollectionShards("other", tuple(Replica(s, 10, "Active") for s in range(3)), 1, 0)

    plan = plan_rebalance([first, second], [10, 11])

    assert plan.operations == (ShardOperation(ShardOperationKind.MOVE, "docs", 0, 10, 11),)
