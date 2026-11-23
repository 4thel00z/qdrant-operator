import pytest

from qdrant_operator.domain import ClusterHealth
from qdrant_operator.domain import ClusterPhase
from qdrant_operator.domain import ConditionStatus
from qdrant_operator.domain import PodRevision
from qdrant_operator.domain import PodState
from qdrant_operator.domain import RolloutSnapshot
from qdrant_operator.domain import RolloutSpec
from qdrant_operator.domain import RolloutStrategy
from qdrant_operator.domain import StatefulSetStatus
from qdrant_operator.domain import hold_partition
from qdrant_operator.domain import next_partition
from qdrant_operator.domain import pod_revisions
from qdrant_operator.domain import rollout_wait
from qdrant_operator.peers import Replica
from qdrant_operator.peers import ShardOperation
from qdrant_operator.peers import ShardOperationKind
from qdrant_operator.usecases import ObserveCluster
from qdrant_operator.usecases import RebalanceShards
from qdrant_operator.usecases import ReconcileCluster
from qdrant_operator.usecases import RecoverDeadPeers
from qdrant_operator.usecases import RolloutInProgressError
from qdrant_operator.usecases import StepRollout
from tests.fakes import FakeHelm
from tests.fakes import FakeKubernetes
from tests.fakes import FakeQdrant
from tests.test_cluster_ops import NS
from tests.test_cluster_ops import PENDING
from tests.test_cluster_ops import RELEASE
from tests.test_cluster_ops import claims
from tests.test_cluster_ops import cluster
from tests.test_cluster_ops import conditions
from tests.test_cluster_ops import peered_qdrant
from tests.test_cluster_ops import rebuilt_pod_qdrant

OLD = "rev-old"
NEW = "rev-new"
SHARD_HEALTH = RolloutStrategy.SHARD_HEALTH
READINESS = RolloutStrategy.READINESS


def snapshot(
    partition: int,
    revisions: tuple[str | None, ...],
    ready: tuple[bool, ...] | None = None,
    problems: tuple[str, ...] = (),
    replicas: int | None = None,
    update_revision: str | None = NEW,
) -> RolloutSnapshot:
    flags = ready or tuple(True for _ in revisions)
    return RolloutSnapshot(
        partition=partition,
        replicas=replicas if replicas is not None else len(revisions),
        update_revision=update_revision,
        pods=tuple(PodRevision(o, r, flags[o]) for o, r in enumerate(revisions)),
        health=ClusterHealth(problems),
    )


def test_rollout_spec_defaults_to_shard_health() -> None:
    assert RolloutSpec.from_dict({}).strategy is SHARD_HEALTH
    assert RolloutSpec.from_dict({"strategy": "Readiness"}).strategy is READINESS


def test_rollout_spec_rejects_unknown_strategy() -> None:
    with pytest.raises(ValueError):
        RolloutSpec.from_dict({"strategy": "Fast"})


def test_cluster_spec_carries_the_rollout() -> None:
    assert cluster(3).rollout.strategy is SHARD_HEALTH
    assert cluster(3, rollout={"strategy": "Readiness"}).rollout.strategy is READINESS


def test_idle_cluster_keeps_the_hold() -> None:
    assert next_partition(SHARD_HEALTH, snapshot(3, (NEW, NEW, NEW))) is None


def test_idle_cluster_restores_a_lost_hold() -> None:
    assert next_partition(SHARD_HEALTH, snapshot(0, (NEW, NEW, NEW))) == 3


def test_scale_up_moves_the_hold_to_the_new_replica_count() -> None:
    assert next_partition(SHARD_HEALTH, snapshot(3, (NEW, NEW, NEW, NEW))) == 4


def test_scale_down_moves_the_hold_to_the_new_replica_count() -> None:
    assert next_partition(SHARD_HEALTH, snapshot(3, (NEW, NEW))) == 2


def test_first_step_releases_the_highest_ordinal() -> None:
    assert next_partition(SHARD_HEALTH, snapshot(3, (OLD, OLD, OLD))) == 2


def test_next_step_releases_the_next_ordinal_once_the_released_pod_is_updated_and_ready() -> None:
    assert next_partition(SHARD_HEALTH, snapshot(2, (OLD, OLD, NEW))) == 1
    assert next_partition(SHARD_HEALTH, snapshot(1, (OLD, NEW, NEW))) == 0


def test_last_step_is_followed_by_the_hold() -> None:
    assert next_partition(SHARD_HEALTH, snapshot(0, (NEW, NEW, NEW))) == 3


def test_waits_while_the_cluster_is_unhealthy() -> None:
    unhealthy = snapshot(3, (OLD, OLD, OLD), problems=("docs/0@101 is Partial",))

    assert next_partition(SHARD_HEALTH, unhealthy) is None
    wait = rollout_wait(unhealthy)
    assert wait and wait.reason == "WaitingForShards"
    assert "docs/0@101" in wait.message


def test_waits_between_steps_while_shards_recover() -> None:
    recovering = snapshot(2, (OLD, OLD, NEW), problems=("docs/1@102 is Recovery",))

    assert next_partition(SHARD_HEALTH, recovering) is None


def test_waits_while_the_released_pod_still_runs_the_old_revision() -> None:
    restarting = snapshot(2, (OLD, OLD, OLD))

    assert next_partition(SHARD_HEALTH, restarting) is None
    wait = rollout_wait(restarting)
    assert wait and wait.reason == "RestartingPod" and "pod 2" in wait.message


def test_waits_while_the_released_pod_is_not_ready() -> None:
    booting = snapshot(2, (OLD, OLD, NEW), ready=(True, True, False))

    assert next_partition(SHARD_HEALTH, booting) is None
    wait = rollout_wait(booting)
    assert wait and wait.reason == "RestartingPod"


def test_a_missing_released_pod_counts_as_restarting() -> None:
    pods = pod_revisions(RELEASE, 3, [PodState(f"{RELEASE}-0", OLD, True)])

    gone = RolloutSnapshot(2, 3, NEW, pods)

    assert next_partition(SHARD_HEALTH, gone) is None


def test_operator_restart_mid_rollout_resumes_from_the_cluster_state() -> None:
    assert next_partition(SHARD_HEALTH, snapshot(1, (OLD, OLD, NEW))) is None
    assert (
        next_partition(SHARD_HEALTH, snapshot(1, (OLD, NEW, NEW), ready=(True, False, True)))
        is None
    )
    assert next_partition(SHARD_HEALTH, snapshot(1, (OLD, NEW, NEW))) == 0


def test_operator_restart_after_the_released_pod_became_ready_releases_the_next() -> None:
    assert next_partition(SHARD_HEALTH, snapshot(2, (OLD, OLD, NEW))) == 1


def test_operator_restart_with_a_lost_partition_raises_the_hold_only_when_idle() -> None:
    assert next_partition(SHARD_HEALTH, snapshot(0, (OLD, NEW, NEW))) is None
    assert next_partition(SHARD_HEALTH, snapshot(0, (NEW, NEW, NEW))) == 3


def test_a_released_pod_that_is_already_updated_is_skipped() -> None:
    assert next_partition(SHARD_HEALTH, snapshot(3, (OLD, OLD, NEW))) == 1


def test_scale_down_during_a_rollout_clamps_the_partition_to_the_replica_count() -> None:
    pods = snapshot(3, (OLD, OLD), replicas=2)

    assert next_partition(SHARD_HEALTH, pods) == 1


def test_unknown_update_revision_holds() -> None:
    unknown = snapshot(1, (OLD, OLD, OLD), update_revision=None)

    assert next_partition(SHARD_HEALTH, unknown) == 3


def test_readiness_strategy_lifts_a_hold_and_does_nothing_else() -> None:
    assert next_partition(READINESS, snapshot(3, (OLD, OLD, OLD))) == 0
    assert next_partition(READINESS, snapshot(2, (OLD, OLD, NEW))) == 0
    assert next_partition(READINESS, snapshot(0, (OLD, OLD, OLD))) is None
    assert next_partition(READINESS, snapshot(0, (NEW, NEW, NEW))) is None
    assert hold_partition(READINESS, 3) == 0
    assert hold_partition(SHARD_HEALTH, 3) == 3


def test_wait_message_lists_at_most_ten_problems() -> None:
    problems = tuple(f"docs/{i}@100 is Dead" for i in range(14))

    wait = rollout_wait(snapshot(3, (OLD, OLD, OLD), problems=problems))

    assert wait
    assert "docs/9@100" in wait.message
    assert "docs/10@100" not in wait.message
    assert wait.message.endswith("and 4 more")


def test_pod_revisions_fills_missing_ordinals_and_ignores_surplus_pods() -> None:
    pods = pod_revisions(
        RELEASE,
        2,
        [PodState(f"{RELEASE}-1", NEW, True), PodState(f"{RELEASE}-5", NEW, True)],
    )

    assert pods == (PodRevision(0, None, False), PodRevision(1, NEW, True))


def test_held_partition_with_pods_on_the_old_revision_counts_as_rolling() -> None:
    held = StatefulSetStatus(3, 3, 0, OLD, NEW, partition=3)

    assert held.rolling


def test_held_partition_with_every_pod_updated_does_not_count_as_rolling() -> None:
    idle = StatefulSetStatus(3, 3, 3, NEW, NEW, partition=3)

    assert not idle.rolling


STS_KEY = (NS, RELEASE)


def set_statefulset(
    kubernetes: FakeKubernetes, partition: int, revisions: tuple[str, ...], current: str = OLD
) -> None:
    updated = len([r for r in revisions if r == NEW])
    kubernetes.statefulsets[STS_KEY] = StatefulSetStatus(
        len(revisions), len(revisions), updated, current, NEW, partition=partition
    )
    kubernetes.pods[NS] = [
        PodState(f"{RELEASE}-{ordinal}", revision, True)
        for ordinal, revision in enumerate(revisions)
    ]


def set_pod(kubernetes: FakeKubernetes, ordinal: int, revision: str, ready: bool) -> None:
    kubernetes.pods[NS] = [
        PodState(p.name, revision, ready) if p.name == f"{RELEASE}-{ordinal}" else p
        for p in kubernetes.pods[NS]
    ]


def partition(kubernetes: FakeKubernetes) -> int:
    return kubernetes.statefulsets[STS_KEY].partition


def stepper(kubernetes: FakeKubernetes, qdrant: FakeQdrant) -> StepRollout:
    return StepRollout(qdrant, kubernetes)


@pytest.fixture
def kubernetes() -> FakeKubernetes:
    fake = FakeKubernetes()
    fake.put_resource(
        {
            "kind": "QdrantCluster",
            "metadata": {"name": "db", "namespace": NS},
            "spec": {"version": "1.16.3"},
        }
    )
    return fake


async def test_step_releases_one_pod_and_reports_progress(kubernetes: FakeKubernetes) -> None:
    set_statefulset(kubernetes, 3, (OLD, OLD, OLD))

    status = await stepper(kubernetes, peered_qdrant(3)).execute(cluster(3), PENDING)

    assert partition(kubernetes) == 2
    assert status.phase is ClusterPhase.UPGRADING
    assert conditions(status)["Progressing"] == (ConditionStatus.TRUE, "RestartingPod")
    message = next(c.message for c in status.conditions if c.type == "Progressing")
    assert "2" in message


async def test_step_waits_for_shards_and_names_the_replicas(kubernetes: FakeKubernetes) -> None:
    set_statefulset(kubernetes, 2, (OLD, OLD, NEW))
    qdrant = peered_qdrant(3)
    qdrant.replicas["docs"] = [Replica(0, 100, "Active"), Replica(1, 102, "Partial")]

    status = await stepper(kubernetes, qdrant).execute(cluster(3), PENDING)

    assert partition(kubernetes) == 2
    assert conditions(status)["Progressing"] == (ConditionStatus.TRUE, "WaitingForShards")
    message = next(c.message for c in status.conditions if c.type == "Progressing")
    assert "docs/1@102 is Partial" in message


async def test_step_waits_while_a_shard_transfer_runs(kubernetes: FakeKubernetes) -> None:
    set_statefulset(kubernetes, 2, (OLD, OLD, NEW))
    qdrant = peered_qdrant(3)
    qdrant.pending.append(ShardOperation(ShardOperationKind.REPLICATE, "docs", 0, 100, 101))

    status = await stepper(kubernetes, qdrant).execute(cluster(3), PENDING)

    assert partition(kubernetes) == 2
    assert conditions(status)["Progressing"][1] == "WaitingForShards"


async def test_step_waits_when_a_node_does_not_answer(kubernetes: FakeKubernetes) -> None:
    set_statefulset(kubernetes, 2, (OLD, OLD, NEW))
    qdrant = peered_qdrant(3)
    del qdrant.node_peer_ids[f"http://{RELEASE}-1.{RELEASE}-headless.{NS}.svc.cluster.local:6333"]

    status = await stepper(kubernetes, qdrant).execute(cluster(3), PENDING)

    assert partition(kubernetes) == 2
    message = next(c.message for c in status.conditions if c.type == "Progressing")
    assert "node 1 does not answer" in message


async def test_step_waits_when_a_peer_is_missing(kubernetes: FakeKubernetes) -> None:
    set_statefulset(kubernetes, 2, (OLD, OLD, NEW))
    qdrant = peered_qdrant(3)
    del qdrant.peers[102]

    status = await stepper(kubernetes, qdrant).execute(cluster(3), PENDING)

    assert partition(kubernetes) == 2
    assert conditions(status)["Progressing"][1] == "WaitingForShards"


async def test_step_waits_when_a_pod_is_not_ready(kubernetes: FakeKubernetes) -> None:
    set_statefulset(kubernetes, 2, (OLD, OLD, NEW))
    set_pod(kubernetes, 2, NEW, False)

    status = await stepper(kubernetes, peered_qdrant(3)).execute(cluster(3), PENDING)

    assert partition(kubernetes) == 2
    assert conditions(status)["Progressing"][1] == "RestartingPod"


async def test_full_rollout_walks_the_ordinals_then_holds(kubernetes: FakeKubernetes) -> None:
    set_statefulset(kubernetes, 3, (OLD, OLD, OLD))
    use_case = stepper(kubernetes, peered_qdrant(3))
    spec = cluster(3)
    status = PENDING

    for ordinal in (2, 1, 0):
        status = await use_case.execute(spec, status)
        assert partition(kubernetes) == ordinal
        set_pod(kubernetes, ordinal, NEW, True)
    await use_case.execute(spec, status)

    assert partition(kubernetes) == 3
    assert kubernetes.partitions == [2, 1, 0, 3]


async def test_idle_cluster_gets_its_hold_and_no_progress_condition(
    kubernetes: FakeKubernetes,
) -> None:
    set_statefulset(kubernetes, 0, (NEW, NEW, NEW), current=NEW)

    status = await stepper(kubernetes, peered_qdrant(3)).execute(cluster(3), PENDING)

    assert partition(kubernetes) == 3
    assert status == PENDING


async def test_idle_cluster_does_not_query_qdrant(kubernetes: FakeKubernetes) -> None:
    set_statefulset(kubernetes, 3, (NEW, NEW, NEW), current=NEW)

    await stepper(kubernetes, FakeQdrant()).execute(cluster(3), PENDING)

    assert kubernetes.partitions == []


async def test_resumed_operator_continues_from_the_cluster_state(
    kubernetes: FakeKubernetes,
) -> None:
    set_statefulset(kubernetes, 1, (OLD, NEW, NEW))

    await stepper(kubernetes, peered_qdrant(3)).execute(cluster(3), PENDING)

    assert partition(kubernetes) == 0


async def test_readiness_strategy_only_lifts_the_hold(kubernetes: FakeKubernetes) -> None:
    set_statefulset(kubernetes, 3, (OLD, OLD, OLD))
    spec = cluster(3, rollout={"strategy": "Readiness"})

    status = await stepper(kubernetes, FakeQdrant()).execute(spec, PENDING)

    assert partition(kubernetes) == 0
    assert status == PENDING


async def test_step_does_nothing_before_the_statefulset_exists(
    kubernetes: FakeKubernetes,
) -> None:
    status = await stepper(kubernetes, FakeQdrant()).execute(cluster(3), PENDING)

    assert status == PENDING
    assert kubernetes.partitions == []


async def test_single_non_distributed_replica_rolls_on_readiness_alone(
    kubernetes: FakeKubernetes,
) -> None:
    set_statefulset(kubernetes, 1, (OLD,))
    spec = cluster(1, cluster={"enabled": False})

    await stepper(kubernetes, FakeQdrant()).execute(spec, PENDING)

    assert partition(kubernetes) == 0


def rendered(helm: FakeHelm) -> tuple[str, int]:
    release = helm.releases[(NS, RELEASE)]
    return release["statefulset"], release["partition"]


async def test_reconcile_renders_the_hold_partition_into_the_release(
    kubernetes: FakeKubernetes,
) -> None:
    helm = FakeHelm()
    set_statefulset(kubernetes, 0, (NEW, NEW, NEW), current=NEW)

    await ReconcileCluster(helm, kubernetes, FakeQdrant()).execute(cluster(3), 1, PENDING)

    assert rendered(helm) == (RELEASE, 3)
    assert kubernetes.partitions == []


async def test_reconcile_mid_rollout_renders_the_hold(kubernetes: FakeKubernetes) -> None:
    helm = FakeHelm()
    set_statefulset(kubernetes, 1, (OLD, NEW, NEW))

    await ReconcileCluster(helm, kubernetes, FakeQdrant()).execute(cluster(3), 1, PENDING)

    assert rendered(helm) == (RELEASE, 3)


async def test_reconcile_renders_the_new_replica_count_on_scale_up(
    kubernetes: FakeKubernetes,
) -> None:
    helm = FakeHelm()
    set_statefulset(kubernetes, 3, (NEW, NEW, NEW), current=NEW)

    await ReconcileCluster(helm, kubernetes, FakeQdrant()).execute(cluster(4), 1, PENDING)

    assert rendered(helm) == (RELEASE, 4)


async def test_reconcile_renders_the_hold_on_first_install(kubernetes: FakeKubernetes) -> None:
    helm = FakeHelm()

    await ReconcileCluster(helm, kubernetes, FakeQdrant()).execute(cluster(3), 1, PENDING)

    assert rendered(helm) == (RELEASE, 3)
    assert kubernetes.partitions == []


async def test_reconcile_with_readiness_renders_partition_zero(
    kubernetes: FakeKubernetes,
) -> None:
    helm = FakeHelm()
    set_statefulset(kubernetes, 3, (OLD, OLD, OLD))

    await ReconcileCluster(helm, kubernetes, FakeQdrant()).execute(
        cluster(3, rollout={"strategy": "Readiness"}), 1, PENDING
    )

    assert rendered(helm) == (RELEASE, 0)


async def test_switching_to_shard_health_reaches_the_partition_through_the_step(
    kubernetes: FakeKubernetes,
) -> None:
    set_statefulset(kubernetes, 0, (NEW, NEW, NEW), current=NEW)

    await stepper(kubernetes, peered_qdrant(3)).execute(cluster(3), PENDING)

    assert partition(kubernetes) == 3


async def test_switching_to_readiness_reaches_the_partition_through_the_step(
    kubernetes: FakeKubernetes,
) -> None:
    set_statefulset(kubernetes, 3, (OLD, OLD, OLD))

    await stepper(kubernetes, FakeQdrant()).execute(
        cluster(3, rollout={"strategy": "Readiness"}), PENDING
    )

    assert partition(kubernetes) == 0


async def test_unchanged_reconcile_leaves_a_running_rollout_alone(
    kubernetes: FakeKubernetes,
) -> None:
    helm = FakeHelm()
    use_case = ReconcileCluster(helm, kubernetes, FakeQdrant())
    spec = cluster(3)
    first = await use_case.execute(spec, 1, PENDING)
    set_statefulset(kubernetes, 1, (OLD, NEW, NEW))
    kubernetes.partitions.clear()

    await use_case.execute(spec, 1, first)

    assert partition(kubernetes) == 1
    assert kubernetes.partitions == []


async def test_scale_down_drain_waits_for_a_running_rollout(kubernetes: FakeKubernetes) -> None:
    helm = FakeHelm()
    set_statefulset(kubernetes, 2, (OLD, OLD, NEW))
    qdrant = peered_qdrant(3)
    use_case = ReconcileCluster(helm, kubernetes, qdrant)
    spec = cluster(2, scaling={"drainOnScaleDown": True})

    with pytest.raises(RolloutInProgressError) as raised:
        await use_case.execute(spec, 1, PENDING)

    assert helm.apply_calls == 0
    assert qdrant.shard_operations == []
    assert raised.value.status
    assert conditions(raised.value.status)["ScalingDown"] == (
        ConditionStatus.TRUE,
        "WaitingForRollout",
    )


async def test_volume_recreation_waits_for_a_running_rollout(kubernetes: FakeKubernetes) -> None:
    helm = FakeHelm()
    set_statefulset(kubernetes, 2, (OLD, OLD, NEW))
    kubernetes.claim_templates[STS_KEY] = {"qdrant-storage": "10Gi"}
    kubernetes.volume_claims[NS] = claims(3)
    use_case = ReconcileCluster(helm, kubernetes, FakeQdrant())
    spec = cluster(3, snapshotPersistence={"enabled": True, "size": "5Gi"})

    with pytest.raises(RolloutInProgressError):
        await use_case.execute(spec, 1, PENDING)

    assert kubernetes.orphaned == []
    assert helm.apply_calls == 0


async def test_observe_reports_a_held_rollout_as_not_ready(kubernetes: FakeKubernetes) -> None:
    set_statefulset(kubernetes, 3, (OLD, OLD, OLD))

    status = await ObserveCluster(kubernetes).execute(cluster(3), PENDING)

    assert status.phase is not ClusterPhase.RUNNING
    assert conditions(status)["Ready"][0] is ConditionStatus.FALSE


async def test_observe_reports_complete_once_every_pod_is_updated(
    kubernetes: FakeKubernetes,
) -> None:
    kubernetes.statefulsets[STS_KEY] = StatefulSetStatus(3, 3, 3, NEW, NEW, partition=3)

    status = await ObserveCluster(kubernetes).execute(cluster(3), PENDING)

    assert status.phase is ClusterPhase.RUNNING
    assert conditions(status)["Progressing"] == (ConditionStatus.FALSE, "RolloutComplete")


async def test_rebalance_waits_for_a_held_rollout(kubernetes: FakeKubernetes) -> None:
    set_statefulset(kubernetes, 3, (OLD, OLD, OLD))
    qdrant = peered_qdrant(3)
    qdrant.replicas["docs"] = [Replica(0, 100, "Active")]
    spec = cluster(3, scaling={"rebalance": True})

    rebalanced = await RebalanceShards(qdrant, kubernetes).execute(spec, PENDING)

    assert rebalanced == PENDING
    assert qdrant.shard_operations == []


async def test_held_rollout_with_a_leftover_dead_peer_gets_the_peer_removed(
    kubernetes: FakeKubernetes,
) -> None:
    set_statefulset(kubernetes, 3, (OLD, OLD, OLD))
    qdrant = rebuilt_pod_qdrant()
    spec = cluster(3, scaling={"removeDeadPeers": True})
    recovery = RecoverDeadPeers(qdrant, kubernetes)
    status = PENDING

    for _ in range(4):
        status = await recovery.execute(spec, status)
        qdrant.settle()

    assert qdrant.removed_peers == [(101, True)]
    assert conditions(status)["DeadPeers"] == (ConditionStatus.FALSE, "NoDeadPeers")
    await stepper(kubernetes, qdrant).execute(spec, status)
    assert partition(kubernetes) == 2


async def test_rollout_waits_on_a_dead_peer_until_it_is_removed(
    kubernetes: FakeKubernetes,
) -> None:
    set_statefulset(kubernetes, 3, (OLD, OLD, OLD))
    qdrant = rebuilt_pod_qdrant()

    status = await stepper(kubernetes, qdrant).execute(cluster(3), PENDING)

    assert partition(kubernetes) == 3
    assert conditions(status)["Progressing"][1] == "WaitingForShards"


@pytest.mark.parametrize(
    ("revisions", "ready"),
    [
        ((OLD, OLD, OLD), (True, True, False)),
        ((OLD, OLD, OLD), (True, True, True)),
    ],
)
async def test_no_recovery_while_a_released_pod_is_mid_restart(
    kubernetes: FakeKubernetes, revisions: tuple[str, ...], ready: tuple[bool, ...]
) -> None:
    set_statefulset(kubernetes, 2, revisions)
    for ordinal, flag in enumerate(ready):
        set_pod(kubernetes, ordinal, revisions[ordinal], flag)
    qdrant = rebuilt_pod_qdrant()
    spec = cluster(3, scaling={"removeDeadPeers": True})

    status = await RecoverDeadPeers(qdrant, kubernetes).execute(spec, PENDING)

    assert status == PENDING
    assert qdrant.shard_operations == []
    assert qdrant.removed_peers == []


async def test_recovery_resumes_once_the_released_pod_is_updated_and_ready(
    kubernetes: FakeKubernetes,
) -> None:
    set_statefulset(kubernetes, 2, (OLD, OLD, NEW))
    qdrant = rebuilt_pod_qdrant()
    spec = cluster(3, scaling={"removeDeadPeers": True})

    status = await RecoverDeadPeers(qdrant, kubernetes).execute(spec, PENDING)

    assert conditions(status)["DeadPeers"][1] == "RestoringReplicas"


async def test_recovery_waits_when_the_statefulset_size_differs_from_the_spec(
    kubernetes: FakeKubernetes,
) -> None:
    set_statefulset(kubernetes, 4, (OLD, OLD, OLD, OLD))
    qdrant = rebuilt_pod_qdrant()

    status = await RecoverDeadPeers(qdrant, kubernetes).execute(
        cluster(3, scaling={"removeDeadPeers": True}), PENDING
    )

    assert status == PENDING
    assert qdrant.removed_peers == []


async def test_readiness_recovery_still_waits_for_a_rolling_statefulset(
    kubernetes: FakeKubernetes,
) -> None:
    set_statefulset(kubernetes, 0, (OLD, OLD, OLD))
    qdrant = rebuilt_pod_qdrant()
    spec = cluster(3, rollout={"strategy": "Readiness"}, scaling={"removeDeadPeers": True})

    status = await RecoverDeadPeers(qdrant, kubernetes).execute(spec, PENDING)

    assert status == PENDING
    assert qdrant.removed_peers == []


async def test_readiness_recovery_runs_once_the_statefulset_settles(
    kubernetes: FakeKubernetes,
) -> None:
    set_statefulset(kubernetes, 0, (NEW, NEW, NEW), current=NEW)
    qdrant = rebuilt_pod_qdrant()
    spec = cluster(3, rollout={"strategy": "Readiness"}, scaling={"removeDeadPeers": True})

    status = await RecoverDeadPeers(qdrant, kubernetes).execute(spec, PENDING)

    assert conditions(status)["DeadPeers"][1] == "RestoringReplicas"


def test_pod_label_selector_names_the_release() -> None:
    assert cluster(3).pod_label_selector == (
        f"app.kubernetes.io/name=qdrant,app.kubernetes.io/instance={RELEASE}"
    )
