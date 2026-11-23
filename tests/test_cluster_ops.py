from dataclasses import replace

import pytest

from qdrant_operator.domain import ClusterPhase
from qdrant_operator.domain import ClusterSpec
from qdrant_operator.domain import ClusterStatus
from qdrant_operator.domain import ConditionStatus
from qdrant_operator.domain import JsonDict
from qdrant_operator.domain import StatefulSetStatus
from qdrant_operator.peers import Replica
from qdrant_operator.peers import ShardOperationKind
from qdrant_operator.usecases import ObserveCluster
from qdrant_operator.usecases import RebalanceShards
from qdrant_operator.usecases import ReconcileCluster
from qdrant_operator.usecases import RecoverDeadPeers
from qdrant_operator.usecases import ReleaseApplyError
from qdrant_operator.usecases import ScaleDownPendingError
from tests.fakes import FakeHelm
from tests.fakes import FakeKubernetes
from tests.fakes import FakeQdrant

NS = "tenant-a"
RELEASE = "qdrant-db"
PENDING = ClusterStatus(ClusterPhase.PENDING)


def cluster(replicas: int = 3, **spec: object) -> ClusterSpec:
    return ClusterSpec.from_dict(
        {"version": "1.16.3", "replicas": replicas, **spec}, {"name": "db", "namespace": NS}
    )


def pod_url(ordinal: int) -> str:
    return f"http://{RELEASE}-{ordinal}.{RELEASE}-headless.{NS}.svc.cluster.local:6333"


def peer_uri(ordinal: int) -> str:
    return f"http://{RELEASE}-{ordinal}.{RELEASE}-headless.{NS}.svc.cluster.local:6335/"


def claims(pods: int) -> dict[str, str]:
    return {
        f"{template}-{RELEASE}-{ordinal}": "10Gi"
        for template in ("qdrant-storage", "qdrant-snapshots")
        for ordinal in range(pods)
    }


def conditions(status: ClusterStatus) -> dict[str, tuple[ConditionStatus, str]]:
    return {c.type: (c.status, c.reason) for c in status.conditions}


def peered_qdrant(pods: int = 3, shards_per_pod: int = 1) -> FakeQdrant:
    qdrant = FakeQdrant()
    qdrant.add_collection(pod_url(0), "docs")
    qdrant.replication_factors["docs"] = 1
    qdrant.replicas["docs"] = [
        Replica(shard, 100 + shard, "Active") for shard in range(pods * shards_per_pod)
    ]
    for ordinal in range(pods):
        qdrant.peers[100 + ordinal] = peer_uri(ordinal)
        qdrant.node_peer_ids[pod_url(ordinal)] = 100 + ordinal
    return qdrant


def reconciler(helm: FakeHelm, kubernetes: FakeKubernetes, qdrant: FakeQdrant) -> ReconcileCluster:
    return ReconcileCluster(helm, kubernetes, qdrant)


@pytest.fixture
def kubernetes() -> FakeKubernetes:
    fake = FakeKubernetes()
    body: JsonDict = {
        "kind": "QdrantCluster",
        "metadata": {"name": "db", "namespace": NS},
        "spec": {"version": "1.16.3"},
    }
    fake.put_resource(body)
    return fake


async def test_reconcile_skips_helm_when_the_rendered_values_are_unchanged(
    kubernetes: FakeKubernetes,
) -> None:
    helm = FakeHelm()
    use_case = reconciler(helm, kubernetes, FakeQdrant())
    spec = cluster(1)

    first = await use_case.execute(spec, 1, PENDING)
    resumed = await use_case.execute(spec, 1, first)
    changed = await use_case.execute(cluster(1, resources={"limits": {"cpu": "1"}}), 2, resumed)

    assert helm.apply_calls == 2
    assert resumed.phase == first.phase
    assert first.applied_hash and first.applied_hash != changed.applied_hash


async def test_reconcile_reapplies_when_the_release_is_gone(kubernetes: FakeKubernetes) -> None:
    helm = FakeHelm()
    use_case = reconciler(helm, kubernetes, FakeQdrant())
    first = await use_case.execute(cluster(1), 1, PENDING)
    helm.releases.clear()

    await use_case.execute(cluster(1), 1, first)

    assert helm.apply_calls == 2


async def test_volume_growth_patches_each_claim_and_orphans_the_statefulset(
    kubernetes: FakeKubernetes,
) -> None:
    helm = FakeHelm()
    kubernetes.claim_templates[(NS, RELEASE)] = {"qdrant-storage": "5Gi"}
    kubernetes.volume_claims[NS] = {f"qdrant-storage-{RELEASE}-{i}": "5Gi" for i in range(2)}
    use_case = reconciler(helm, kubernetes, FakeQdrant())
    spec = cluster(2, persistence={"size": "40Gi"})

    status = await use_case.execute(spec, 2, ClusterStatus(ClusterPhase.RUNNING))

    assert kubernetes.resized == [
        (f"qdrant-storage-{RELEASE}-0", "40Gi"),
        (f"qdrant-storage-{RELEASE}-1", "40Gi"),
    ]
    assert kubernetes.orphaned == [(NS, RELEASE)]
    assert helm.apply_calls == 1
    assert status.phase == ClusterPhase.PENDING


async def test_volume_growth_skips_claims_already_large_enough(
    kubernetes: FakeKubernetes,
) -> None:
    kubernetes.claim_templates[(NS, RELEASE)] = {"qdrant-storage": "5Gi"}
    kubernetes.volume_claims[NS] = {f"qdrant-storage-{RELEASE}-0": "80Gi"}
    use_case = reconciler(FakeHelm(), kubernetes, FakeQdrant())

    await use_case.execute(cluster(1, persistence={"size": "40Gi"}), 1, PENDING)

    assert kubernetes.resized == []
    assert kubernetes.orphaned == [(NS, RELEASE)]


async def test_matching_claims_leave_the_statefulset_alone(kubernetes: FakeKubernetes) -> None:
    kubernetes.claim_templates[(NS, RELEASE)] = {"qdrant-storage": "10Gi"}
    kubernetes.volume_claims[NS] = {f"qdrant-storage-{RELEASE}-0": "10Gi"}

    await reconciler(FakeHelm(), kubernetes, FakeQdrant()).execute(cluster(1), 1, PENDING)

    assert kubernetes.orphaned == []
    assert kubernetes.resized == []


async def test_shrinking_a_claim_is_refused(kubernetes: FakeKubernetes) -> None:
    kubernetes.claim_templates[(NS, RELEASE)] = {"qdrant-storage": "40Gi"}
    use_case = reconciler(FakeHelm(), kubernetes, FakeQdrant())

    with pytest.raises(ValueError, match="cannot shrink"):
        await use_case.execute(cluster(1, persistence={"size": "10Gi"}), 1, PENDING)

    assert kubernetes.orphaned == []


async def test_enabling_snapshot_persistence_recreates_the_statefulset(
    kubernetes: FakeKubernetes,
) -> None:
    kubernetes.claim_templates[(NS, RELEASE)] = {"qdrant-storage": "10Gi"}
    kubernetes.volume_claims[NS] = {f"qdrant-storage-{RELEASE}-0": "10Gi"}
    spec = cluster(1, snapshotPersistence={"enabled": True, "size": "100Gi"})

    await reconciler(FakeHelm(), kubernetes, FakeQdrant()).execute(spec, 1, PENDING)

    assert kubernetes.orphaned == [(NS, RELEASE)]


async def test_scale_down_without_drain_flags_shards_on_the_removed_peer(
    kubernetes: FakeKubernetes,
) -> None:
    helm = FakeHelm()
    kubernetes.statefulsets[(NS, RELEASE)] = StatefulSetStatus(3, 3)
    kubernetes.volume_claims[NS] = claims(3)
    qdrant = peered_qdrant()

    status = await reconciler(helm, kubernetes, qdrant).execute(cluster(2), 2, PENDING)

    assert helm.apply_calls == 1
    assert kubernetes.volume_claims[NS] == claims(3)
    assert qdrant.shard_operations == []
    state, reason = conditions(status)["ScaleDownUnsafe"]
    assert (state, reason) == (ConditionStatus.TRUE, "ShardsOnRemovedPeers")
    assert "docs/2" in next(c.message for c in status.conditions if c.type == "ScaleDownUnsafe")


async def test_scale_down_drain_moves_shards_then_removes_peers_then_applies(
    kubernetes: FakeKubernetes,
) -> None:
    helm = FakeHelm()
    kubernetes.statefulsets[(NS, RELEASE)] = StatefulSetStatus(3, 3)
    kubernetes.volume_claims[NS] = claims(3)
    qdrant = peered_qdrant()
    qdrant.slow_transfers = True
    use_case = reconciler(helm, kubernetes, qdrant)
    spec = cluster(2, scaling={"drainOnScaleDown": True})

    with pytest.raises(ScaleDownPendingError) as pending:
        await use_case.execute(spec, 2, PENDING)

    assert helm.apply_calls == 0
    assert [(o.kind, o.shard_id, o.peer_id) for o in qdrant.shard_operations] == [
        (ShardOperationKind.MOVE, 2, 102)
    ]
    assert conditions(pending.value.status)["ScalingDown"] == (
        ConditionStatus.TRUE,
        "DrainingPeers",
    )

    with pytest.raises(ScaleDownPendingError):
        await use_case.execute(spec, 2, pending.value.status)
    assert len(qdrant.shard_operations) == 1

    qdrant.settle()
    status = await use_case.execute(spec, 2, pending.value.status)

    assert qdrant.removed_peers == [(102, False)]
    assert 102 not in qdrant.peers
    assert helm.releases[(NS, RELEASE)]["values"]["replicaCount"] == 2
    assert kubernetes.volume_claims[NS] == claims(2)
    assert conditions(status)["ScalingDown"] == (ConditionStatus.FALSE, "PeersRemoved")
    assert sorted(r.peer_id for r in qdrant.replicas["docs"]) == [100, 100, 101]


async def test_scale_down_drain_refuses_to_move_a_replica_that_is_not_active(
    kubernetes: FakeKubernetes,
) -> None:
    helm = FakeHelm()
    kubernetes.statefulsets[(NS, RELEASE)] = StatefulSetStatus(3, 3)
    qdrant = peered_qdrant()
    qdrant.replicas["docs"][2] = Replica(2, 102, "Dead")
    spec = cluster(2, scaling={"drainOnScaleDown": True})

    with pytest.raises(ScaleDownPendingError) as pending:
        await reconciler(helm, kubernetes, qdrant).execute(spec, 2, PENDING)

    assert helm.apply_calls == 0
    assert qdrant.shard_operations == []
    assert conditions(pending.value.status)["ScalingDown"][1] == "ShardsUnsafe"


async def test_scale_up_applies_without_touching_peers(kubernetes: FakeKubernetes) -> None:
    helm = FakeHelm()
    kubernetes.statefulsets[(NS, RELEASE)] = StatefulSetStatus(2, 2)
    qdrant = peered_qdrant(2)

    await reconciler(helm, kubernetes, qdrant).execute(
        cluster(3, scaling={"drainOnScaleDown": True}), 2, PENDING
    )

    assert helm.apply_calls == 1
    assert qdrant.shard_operations == []


def rebuilt_pod_qdrant() -> FakeQdrant:
    """Pod 1 came back on an empty volume: new peer 201 at the URI of dead peer 101."""
    qdrant = peered_qdrant(3)
    qdrant.replication_factors["docs"] = 2
    qdrant.replicas["docs"] = [
        Replica(0, 100, "Active"),
        Replica(0, 101, "Dead"),
        Replica(1, 101, "Dead"),
        Replica(1, 102, "Active"),
    ]
    qdrant.peers[201] = peer_uri(1)
    qdrant.node_peer_ids[pod_url(1)] = 201
    return qdrant


def settled(kubernetes: FakeKubernetes, replicas: int = 3) -> None:
    kubernetes.statefulsets[(NS, RELEASE)] = StatefulSetStatus(
        replicas, replicas, partition=replicas
    )


async def test_dead_peer_is_restored_from_active_replicas_then_removed(
    kubernetes: FakeKubernetes,
) -> None:
    settled(kubernetes)
    qdrant = rebuilt_pod_qdrant()
    qdrant.slow_transfers = True
    use_case = RecoverDeadPeers(qdrant, kubernetes)
    spec = cluster(3, scaling={"removeDeadPeers": True})

    restoring = await use_case.execute(spec, PENDING)

    assert conditions(restoring)["DeadPeers"] == (ConditionStatus.TRUE, "RestoringReplicas")
    assert [(o.kind, o.shard_id, o.peer_id) for o in qdrant.shard_operations] == [
        (ShardOperationKind.REPLICATE, 0, 100)
    ]
    assert qdrant.removed_peers == []

    qdrant.settle()
    second = await use_case.execute(spec, restoring)
    assert [(o.shard_id, o.peer_id) for o in qdrant.shard_operations] == [(0, 100), (1, 102)]
    assert qdrant.removed_peers == []

    qdrant.settle()
    done = await use_case.execute(spec, second)

    assert qdrant.removed_peers == [(101, True)]
    assert conditions(done)["DeadPeers"] == (ConditionStatus.FALSE, "DeadPeersRemoved")
    assert sorted((r.shard_id, r.peer_id) for r in qdrant.replicas["docs"]) == [
        (0, 100),
        (0, 201),
        (1, 100),
        (1, 102),
    ]


async def test_dead_peer_with_the_only_copy_is_reported_not_removed(
    kubernetes: FakeKubernetes,
) -> None:
    settled(kubernetes)
    qdrant = rebuilt_pod_qdrant()
    qdrant.replicas["docs"] = [Replica(0, 101, "Active")]

    status = await RecoverDeadPeers(qdrant, kubernetes).execute(cluster(3), PENDING)

    assert conditions(status)["DeadPeers"][1] == "ShardsLost"
    assert qdrant.removed_peers == []


async def test_peers_are_never_called_dead_while_a_pod_is_unreachable_or_not_ready(
    kubernetes: FakeKubernetes,
) -> None:
    qdrant = rebuilt_pod_qdrant()
    use_case = RecoverDeadPeers(qdrant, kubernetes)

    kubernetes.statefulsets[(NS, RELEASE)] = StatefulSetStatus(3, 2)
    not_ready = await use_case.execute(cluster(3), PENDING)
    settled(kubernetes)
    del qdrant.node_peer_ids[pod_url(2)]
    unreachable = await use_case.execute(cluster(3), PENDING)

    assert not_ready == PENDING
    assert unreachable == PENDING
    assert qdrant.shard_operations == []
    assert qdrant.removed_peers == []


async def test_healthy_cluster_reports_no_dead_peers(kubernetes: FakeKubernetes) -> None:
    settled(kubernetes)

    status = await RecoverDeadPeers(peered_qdrant(), kubernetes).execute(cluster(3), PENDING)

    assert conditions(status)["DeadPeers"] == (ConditionStatus.FALSE, "NoDeadPeers")


async def test_observe_reports_a_rolling_update_until_every_pod_is_on_the_new_revision(
    kubernetes: FakeKubernetes,
) -> None:
    use_case = ObserveCluster(kubernetes)
    spec = cluster(3)
    running = ClusterStatus(ClusterPhase.RUNNING)
    kubernetes.statefulsets[(NS, RELEASE)] = StatefulSetStatus(3, 3, 1, "rev-1", "rev-2")

    rolling = await use_case.execute(spec, running)
    kubernetes.statefulsets[(NS, RELEASE)] = StatefulSetStatus(3, 3, 3, "rev-2", "rev-2")
    done = await use_case.execute(spec, rolling)

    assert rolling.phase != ClusterPhase.RUNNING
    assert (rolling.updated_replicas, rolling.update_revision) == (1, "rev-2")
    assert "rolling update 1/3" in next(c.message for c in rolling.conditions if c.type == "Ready")
    assert done.phase == ClusterPhase.RUNNING
    assert replace(done, conditions=()).current_revision == "rev-2"


async def test_generated_api_key_is_created_once_and_handed_to_helm_by_reference(
    kubernetes: FakeKubernetes,
) -> None:
    helm = FakeHelm()
    use_case = reconciler(helm, kubernetes, FakeQdrant())
    spec = cluster(1, apiKey={"autoGenerate": True})

    first = await use_case.execute(spec, 1, PENDING)
    key = kubernetes.secrets[(NS, "qdrant-db-generated-keys")]["api-key"]
    await use_case.execute(cluster(2, apiKey={"autoGenerate": True}), 2, first)

    values = helm.releases[(NS, "qdrant-db")]["values"]
    assert len(key) == 32
    assert kubernetes.secrets[(NS, "qdrant-db-generated-keys")]["api-key"] == key
    assert values["apiKey"] == {
        "valueFrom": {"secretKeyRef": {"name": "qdrant-db-generated-keys", "key": "api-key"}}
    }
    assert kubernetes.secret_owners[(NS, "qdrant-db-generated-keys")]["kind"] == "QdrantCluster"


async def test_generated_api_key_carries_over_the_key_the_chart_made(
    kubernetes: FakeKubernetes,
) -> None:
    kubernetes.secrets[(NS, "qdrant-db-apikey")] = {"api-key": "chart-made-key"}

    await reconciler(FakeHelm(), kubernetes, FakeQdrant()).execute(
        cluster(1, apiKey={"autoGenerate": True}), 1, PENDING
    )

    assert kubernetes.secrets[(NS, "qdrant-db-generated-keys")] == {"api-key": "chart-made-key"}


async def test_cluster_without_generated_keys_creates_no_secret(kubernetes: FakeKubernetes) -> None:
    await reconciler(FakeHelm(), kubernetes, FakeQdrant()).execute(cluster(1), 1, PENDING)

    assert (NS, "qdrant-db-generated-keys") not in kubernetes.secrets


def crash_looping_pod_qdrant() -> FakeQdrant:
    """Pod 1 lost its volume and crash-loops: Qdrant refuses to join at dead peer 101's URI."""
    qdrant = rebuilt_pod_qdrant()
    del qdrant.peers[201]
    del qdrant.node_peer_ids[pod_url(1)]
    return qdrant


async def test_a_pod_crash_looping_on_an_empty_volume_gets_its_old_peer_repaired(
    kubernetes: FakeKubernetes,
) -> None:
    kubernetes.statefulsets[(NS, RELEASE)] = StatefulSetStatus(3, 2, partition=3)
    kubernetes.pod_logs[(NS, f"{RELEASE}-1")] = (
        f"peer URI {peer_uri(1)} already used by peer 101 which still has shards"
    )
    qdrant = crash_looping_pod_qdrant()
    use_case = RecoverDeadPeers(qdrant, kubernetes)
    spec = cluster(3, scaling={"removeDeadPeers": True})

    first = await use_case.execute(spec, PENDING)
    second = await use_case.execute(spec, first)
    done = await use_case.execute(spec, second)

    assert [(o.kind, o.shard_id) for o in qdrant.shard_operations] == [
        (ShardOperationKind.REPLICATE, 0),
        (ShardOperationKind.REPLICATE, 1),
    ]
    assert qdrant.removed_peers == [(101, True)]
    assert conditions(done)["DeadPeers"] == (ConditionStatus.FALSE, "DeadPeersRemoved")


async def test_a_silent_pod_without_the_join_error_is_left_alone(
    kubernetes: FakeKubernetes,
) -> None:
    kubernetes.statefulsets[(NS, RELEASE)] = StatefulSetStatus(3, 2)
    kubernetes.pod_logs[(NS, f"{RELEASE}-1")] = "OOMKilled"
    qdrant = crash_looping_pod_qdrant()

    status = await RecoverDeadPeers(qdrant, kubernetes).execute(
        cluster(3, scaling={"removeDeadPeers": True}), PENDING
    )

    assert status == PENDING
    assert qdrant.shard_operations == []


async def test_rebalance_spreads_shards_onto_a_new_peer_after_scale_up(
    kubernetes: FakeKubernetes,
) -> None:
    settled(kubernetes)
    qdrant = peered_qdrant(3)
    qdrant.replicas["docs"] = [Replica(s, 100 + s % 2, "Active") for s in range(4)]
    use_case = RebalanceShards(qdrant, kubernetes)
    spec = cluster(3, scaling={"rebalance": True})

    moving = await use_case.execute(spec, PENDING)
    balanced = await use_case.execute(spec, moving)

    assert conditions(moving)["Rebalancing"] == (ConditionStatus.TRUE, "Rebalancing")
    assert [(o.kind, o.peer_id, o.target_peer_id) for o in qdrant.shard_operations] == [
        (ShardOperationKind.MOVE, 101, 102)
    ]
    assert conditions(balanced)["Rebalancing"] == (ConditionStatus.FALSE, "Balanced")


async def test_rebalance_evens_out_replicas_left_uneven_by_a_restore(
    kubernetes: FakeKubernetes,
) -> None:
    settled(kubernetes)
    qdrant = peered_qdrant(3)
    qdrant.replication_factors["docs"] = 2
    qdrant.replicas["docs"] = [
        Replica(0, 100, "Active"),
        Replica(0, 102, "Active"),
        Replica(1, 101, "Active"),
        Replica(1, 102, "Active"),
        Replica(2, 101, "Active"),
        Replica(2, 102, "Active"),
    ]
    use_case = RebalanceShards(qdrant, kubernetes)
    spec = cluster(3, scaling={"rebalance": True})

    def per_peer() -> dict[int, int]:
        return {p: sum(r.peer_id == p for r in qdrant.replicas["docs"]) for p in (100, 101, 102)}

    assert per_peer() == {100: 1, 101: 2, 102: 3}
    moving = await use_case.execute(spec, PENDING)
    balanced = await use_case.execute(spec, moving)
    again = await use_case.execute(spec, balanced)

    assert [(o.kind, o.shard_id, o.peer_id, o.target_peer_id) for o in qdrant.shard_operations] == [
        (ShardOperationKind.MOVE, 1, 102, 100)
    ]
    assert per_peer() == {100: 2, 101: 2, 102: 2}
    assert conditions(moving)["Rebalancing"] == (ConditionStatus.TRUE, "Rebalancing")
    assert conditions(balanced)["Rebalancing"] == (ConditionStatus.FALSE, "Balanced")
    assert conditions(again)["Rebalancing"] == (ConditionStatus.FALSE, "Balanced")
    assert len(qdrant.shard_operations) == 1


async def test_rebalance_waits_while_the_statefulset_is_not_settled(
    kubernetes: FakeKubernetes,
) -> None:
    kubernetes.statefulsets[(NS, RELEASE)] = StatefulSetStatus(3, 2)
    qdrant = peered_qdrant(3)
    qdrant.replicas["docs"] = [Replica(s, 100, "Active") for s in range(3)]

    status = await RebalanceShards(qdrant, kubernetes).execute(cluster(3), PENDING)

    assert status == PENDING
    assert qdrant.shard_operations == []


async def test_a_failed_helm_apply_is_reported_in_the_status(kubernetes: FakeKubernetes) -> None:
    helm = FakeHelm(apply_error="helm upgrade failed: StatefulSet.apps is invalid")

    with pytest.raises(ReleaseApplyError) as failed:
        await reconciler(helm, kubernetes, FakeQdrant()).execute(cluster(3), 4, PENDING)

    assert conditions(failed.value.status)["Progressing"] == (
        ConditionStatus.FALSE,
        "HelmApplyFailed",
    )
    assert failed.value.status.observed_generation == 4
    assert failed.value.status.applied_hash == PENDING.applied_hash
