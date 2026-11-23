"""Application use cases: orchestrate ports, never touch I/O libraries directly."""

import asyncio
import json
import secrets
import string
from collections.abc import Awaitable
from collections.abc import Callable
from collections.abc import Iterable
from collections.abc import Mapping
from collections.abc import Sequence
from dataclasses import dataclass
from dataclasses import replace
from datetime import UTC
from datetime import datetime
from functools import partial
from typing import Any

from qdrant_operator.domain import API_VERSION
from qdrant_operator.domain import AUTO_GENERATED_API_KEY_KEY
from qdrant_operator.domain import BACKUPS
from qdrant_operator.domain import CLUSTERS
from qdrant_operator.domain import MANIFEST_KEY
from qdrant_operator.domain import TOKEN_SECRET_KEY
from qdrant_operator.domain import URL_SECRET_KEY
from qdrant_operator.domain import AccessKeyPhase
from qdrant_operator.domain import AccessKeySpec
from qdrant_operator.domain import AccessKeyStatus
from qdrant_operator.domain import BackupManifest
from qdrant_operator.domain import BackupPhase
from qdrant_operator.domain import BackupRecord
from qdrant_operator.domain import BackupScheduleSpec
from qdrant_operator.domain import BackupScheduleStatus
from qdrant_operator.domain import BackupSpec
from qdrant_operator.domain import BackupStatus
from qdrant_operator.domain import ClusterConnection
from qdrant_operator.domain import ClusterHealth
from qdrant_operator.domain import ClusterPhase
from qdrant_operator.domain import ClusterRef
from qdrant_operator.domain import ClusterSpec
from qdrant_operator.domain import ClusterStatus
from qdrant_operator.domain import CollectionBackup
from qdrant_operator.domain import CollectionBackupStatus
from qdrant_operator.domain import CollectionMigration
from qdrant_operator.domain import CollectionPhase
from qdrant_operator.domain import CollectionSpec
from qdrant_operator.domain import CollectionStatus
from qdrant_operator.domain import ConcurrencyPolicy
from qdrant_operator.domain import Condition
from qdrant_operator.domain import ConditionStatus
from qdrant_operator.domain import CredentialsSecretRef
from qdrant_operator.domain import DeletionPolicy
from qdrant_operator.domain import InProgressSnapshot
from qdrant_operator.domain import JsonDict
from qdrant_operator.domain import MigrationPhase
from qdrant_operator.domain import MigrationSpec
from qdrant_operator.domain import MigrationStatus
from qdrant_operator.domain import PodRevision
from qdrant_operator.domain import QdrantNode
from qdrant_operator.domain import ReclaimPolicy
from qdrant_operator.domain import ResourceRef
from qdrant_operator.domain import RestoredCollection
from qdrant_operator.domain import RestorePhase
from qdrant_operator.domain import RestoreProgress
from qdrant_operator.domain import RestoreSpec
from qdrant_operator.domain import RestoreStatus
from qdrant_operator.domain import RolloutSnapshot
from qdrant_operator.domain import RolloutStrategy
from qdrant_operator.domain import RolloutWait
from qdrant_operator.domain import S3Credentials
from qdrant_operator.domain import S3StorageSpec
from qdrant_operator.domain import SchedulePhase
from qdrant_operator.domain import SecretRef
from qdrant_operator.domain import SnapshotRecord
from qdrant_operator.domain import StatefulSetStatus
from qdrant_operator.domain import chart_version
from qdrant_operator.domain import create_body_from_config
from qdrant_operator.domain import format_size
from qdrant_operator.domain import group_by_shard_key
from qdrant_operator.domain import hold_partition
from qdrant_operator.domain import indexing_timeout_message
from qdrant_operator.domain import is_subset
from qdrant_operator.domain import join_key
from qdrant_operator.domain import key_fingerprint
from qdrant_operator.domain import next_partition
from qdrant_operator.domain import owner_reference
from qdrant_operator.domain import parse_quantity
from qdrant_operator.domain import parse_time
from qdrant_operator.domain import payload_indexes_from_schema
from qdrant_operator.domain import plan_sync_window
from qdrant_operator.domain import plan_volumes
from qdrant_operator.domain import pod_revisions
from qdrant_operator.domain import point_id_order
from qdrant_operator.domain import released_pods_settled
from qdrant_operator.domain import rollout_wait
from qdrant_operator.domain import set_condition
from qdrant_operator.domain import to_point_struct
from qdrant_operator.domain import values_digest
from qdrant_operator.domain import vector_layout
from qdrant_operator.peers import ClusterTopology
from qdrant_operator.peers import CollectionShards
from qdrant_operator.peers import ShardOperationKind
from qdrant_operator.peers import ShardPlan
from qdrant_operator.peers import dead_peers
from qdrant_operator.peers import departing_peers
from qdrant_operator.peers import plan_evacuation
from qdrant_operator.peers import plan_rebalance
from qdrant_operator.peers import plan_recovery
from qdrant_operator.peers import replaced_peer
from qdrant_operator.peers import surviving_peers
from qdrant_operator.ports import HelmPort
from qdrant_operator.ports import KubernetesPort
from qdrant_operator.ports import QdrantPort
from qdrant_operator.ports import StoragePort
from qdrant_operator.ports import TokenPort

RECENT_BACKUPS_LIMIT = 10
PRESIGNED_URL_TTL_SECONDS = 3600
CRASH_LOG_LINES = 50
HELM_ERROR_CHARS = 2000


class ClusterNotFoundError(LookupError):
    """The referenced QdrantCluster does not exist; callers retry rather than fail."""


class SourceNotReadyError(RuntimeError):
    """The referenced QdrantBackup has not completed yet; callers retry rather than fail."""


class ClusterNotReadyError(RuntimeError):
    """The QdrantCluster does not answer /readyz yet; callers retry rather than fail."""


def now_utc() -> datetime:
    return datetime.now(UTC)


async def read_credentials(kubernetes: KubernetesPort, ref: CredentialsSecretRef) -> S3Credentials:
    return S3Credentials(
        access_key_id=await kubernetes.get_secret_value(ref.access_key_ref()),
        secret_access_key=await kubernetes.get_secret_value(ref.secret_key_ref()),
    )


@dataclass
class ResolveCluster:
    kubernetes: KubernetesPort

    async def execute(self, ref: ClusterRef) -> ClusterConnection:
        body = await self.kubernetes.get_custom_resource(ref.to_resource_ref())
        if not body:
            raise ClusterNotFoundError(f"QdrantCluster {ref.namespace}/{ref.name} not found")
        return await self.connect(ClusterSpec.from_dict(body["spec"], body["metadata"]))

    async def connect(self, spec: ClusterSpec) -> ClusterConnection:
        api_key = await self.read_api_key(spec)
        ca_cert = await self.read_ca_cert(spec)
        return ClusterConnection(
            service=QdrantNode(spec.service_url(), api_key, ca_cert),
            nodes=tuple(QdrantNode(url, api_key, ca_cert) for url in spec.node_urls()),
        )

    async def read_api_key(self, spec: ClusterSpec) -> str | None:
        secret_ref = spec.api_key.resolve_secret_ref(spec.name, spec.namespace)
        if not secret_ref:
            return None
        return await self.kubernetes.get_secret_value(secret_ref)

    async def read_ca_cert(self, spec: ClusterSpec) -> str | None:
        secret_ref = spec.tls.ca_secret_ref(spec.namespace)
        if not secret_ref:
            return None
        try:
            return await self.kubernetes.get_secret_value(secret_ref)
        except KeyError:
            return None


class ScaleDownPendingError(RuntimeError):
    """Peers are still being emptied; the reconcile retries and the status carries the progress."""

    def __init__(self, message: str, status: ClusterStatus) -> None:
        super().__init__(message)
        self.status = status


class RolloutInProgressError(RuntimeError):
    """A pod restart is in progress; the reconcile retries once it is done."""

    def __init__(self, message: str, status: ClusterStatus | None = None) -> None:
        super().__init__(message)
        self.status = status


class ReleaseApplyError(RuntimeError):
    """helm install/upgrade failed; the status carries the error so `kubectl describe` shows it."""

    def __init__(self, message: str, status: ClusterStatus) -> None:
        super().__init__(message)
        self.status = status


class ShardsUnsafeError(RuntimeError):
    """Moving a replica would lose data: no healthy copy exists outside the removed peers."""


def progress_condition(type: str, active: bool, reason: str, message: str) -> Condition:
    return Condition(
        type=type,
        status=ConditionStatus.TRUE if active else ConditionStatus.FALSE,
        last_transition_time=now_utc(),
        reason=reason,
        message=message,
    )


async def survey_collections(qdrant: QdrantPort, node: QdrantNode) -> list[CollectionShards]:
    return [
        await qdrant.collection_shards(node, name) for name in await qdrant.list_collections(node)
    ]


async def node_problems(
    qdrant: QdrantPort, spec: ClusterSpec, ordinal: int, node: QdrantNode
) -> list[str]:
    try:
        topology = await qdrant.cluster_topology(node)
        survey = await survey_collections(qdrant, node)
    except ConnectionError:
        return [f"node {ordinal} does not answer"]
    pod_peers = sorted(
        o for peer in topology.peers if (o := peer.ordinal(spec.release_name)) is not None
    )
    peer_problems = (
        []
        if len(topology.peers) == spec.replicas and pod_peers == list(range(spec.replicas))
        else [f"node {ordinal} lists {len(topology.peers)} peers for {spec.replicas} pods"]
    )
    transfers = [
        f"{shards.collection} has {shards.transfers} shard transfers"
        for shards in survey
        if shards.transfers
    ]
    return [*peer_problems, *transfers, *(p for shards in survey for p in shards.inactive())]


async def assess_health(
    qdrant: QdrantPort, spec: ClusterSpec, nodes: Sequence[QdrantNode], pods: Sequence[PodRevision]
) -> ClusterHealth:
    """Every pod Ready and answering, every shard replica Active, no transfers, all peers joined."""
    unready = tuple(f"pod {pod.ordinal} is not Ready" for pod in pods if not pod.ready)
    if unready or not spec.distributed.enabled:
        return ClusterHealth(unready)
    reports = [await node_problems(qdrant, spec, o, node) for o, node in enumerate(nodes)]
    return ClusterHealth(tuple(dict.fromkeys(problem for report in reports for problem in report)))


def describe_plan(plan: ShardPlan) -> str:
    parts = [f"{len(plan.operations)} shard operations issued"]
    if plan.in_flight:
        parts.append(f"{plan.in_flight} transfers running")
    if plan.blocked:
        parts.append(f"waiting on {', '.join(plan.blocked)}")
    return "; ".join(parts)


@dataclass
class ExpandVolumes:
    """Grow PVCs and recreate the StatefulSet (pods kept) when its volume claims change."""

    kubernetes: KubernetesPort

    async def execute(self, spec: ClusterSpec, rolling: bool = False) -> bool:
        """Returns True when the StatefulSet was deleted, so the helm apply must recreate it.

        Raises RolloutInProgressError instead of recreating while a held rollout is running,
        since the new StatefulSet would lose its partition.
        """
        live = await self.kubernetes.get_statefulset_claims(spec.release_name, spec.namespace)
        if live is None:
            return False
        desired = spec.claim_sizes()
        plan = plan_volumes(desired, live)
        await self.grow_claims(spec, desired)
        if not plan.recreate:
            return False
        if rolling:
            raise RolloutInProgressError("volume claims change while pods are restarting")
        await self.kubernetes.orphan_statefulset(spec.release_name, spec.namespace)
        return True

    async def grow_claims(self, spec: ClusterSpec, desired: Mapping[str, str]) -> None:
        """Patch every PVC that requests less than the spec; never asks for a shrink."""
        claims = await self.kubernetes.list_volume_claims(spec.namespace, spec.volume_selector)
        for template, size in desired.items():
            prefix = f"{template}-{spec.release_name}-"
            for claim in claims:
                if not claim.name.startswith(prefix):
                    continue
                if parse_quantity(claim.size) >= parse_quantity(size):
                    continue
                await self.kubernetes.resize_volume_claim(claim.name, spec.namespace, size)


@dataclass
class DrainPeers:
    """Move shard replicas off the peers a scale-down removes, then remove those peers."""

    qdrant: QdrantPort

    async def execute(self, spec: ClusterSpec, node: QdrantNode) -> ShardPlan:
        """Issue the next operations; an empty plan means the peers were emptied and removed."""
        topology = await self.qdrant.cluster_topology(node)
        leaving = departing_peers(topology, spec.release_name, spec.replicas)
        survey = await survey_collections(self.qdrant, node)
        plan = plan_evacuation(
            survey, leaving, surviving_peers(topology, spec.release_name, spec.replicas)
        )
        if plan.lost:
            lost = ", ".join(loss.describe() for loss in plan.lost)
            raise ShardsUnsafeError(f"no healthy replica to move off the removed peers: {lost}")
        for operation in plan.operations:
            await self.qdrant.apply_shard_operation(node, operation)
        if plan.operations or plan.in_flight or plan.blocked:
            return plan
        for peer_id in sorted(leaving):
            await self.qdrant.remove_peer(node, peer_id, force=False)
        return plan


@dataclass
class GenerateApiKeys:
    """Create the cluster's generated API keys before helm renders them.

    A key the chart already generated (release installed with apiKey: true) is carried over,
    so switching to the operator-generated secret changes neither the key nor the pods.
    """

    kubernetes: KubernetesPort

    async def execute(self, spec: ClusterSpec) -> None:
        wanted = spec.generated_keys()
        if not wanted:
            return
        current = {key: await self.read(spec.generated_key_ref(key)) for key in wanted}
        if all(current.values()):
            return
        body = await self.kubernetes.get_custom_resource(
            ResourceRef(CLUSTERS, spec.name, spec.namespace)
        )
        if not body:
            raise ClusterNotFoundError(f"QdrantCluster {spec.namespace}/{spec.name} not found")
        chart = SecretRef(spec.chart_keys_secret, AUTO_GENERATED_API_KEY_KEY, spec.namespace)
        values = {
            key: current[key] or await self.read(replace(chart, key=key)) or random_key()
            for key in wanted
        }
        owner = {
            "apiVersion": API_VERSION,
            "kind": CLUSTERS.kind,
            "name": spec.name,
            "uid": body["metadata"]["uid"],
        }
        await self.kubernetes.apply_secret(
            spec.generated_keys_secret, spec.namespace, values, owner
        )

    async def read(self, ref: SecretRef) -> str:
        try:
            return await self.kubernetes.get_secret_value(ref)
        except KeyError:
            return ""


def random_key() -> str:
    alphabet = string.ascii_letters + string.digits
    return "".join(secrets.choice(alphabet) for _ in range(32))


def apply_failed(conditions: Iterable[Condition]) -> bool:
    return any(
        c.type == "Progressing"
        and c.status is ConditionStatus.FALSE
        and c.reason == "HelmApplyFailed"
        for c in conditions
    )


def clear_apply_failure(conditions: Iterable[Condition]) -> list[Condition]:
    conditions = list(conditions)
    if not apply_failed(conditions):
        return conditions
    return set_condition(
        conditions,
        progress_condition(
            "Progressing", False, "HelmReleaseApplied", "Helm release matches the desired spec"
        ),
    )


@dataclass
class ReconcileCluster:
    helm: HelmPort
    kubernetes: KubernetesPort
    qdrant: QdrantPort

    async def execute(
        self, spec: ClusterSpec, generation: int | None, current: ClusterStatus
    ) -> ClusterStatus:
        await GenerateApiKeys(self.kubernetes).execute(spec)
        live = await self.kubernetes.get_statefulset_status(spec.release_name, spec.namespace)
        current = await self.prepare_scale_down(spec, live, current)
        held = spec.rollout.strategy is RolloutStrategy.SHARD_HEALTH
        recreated = await ExpandVolumes(self.kubernetes).execute(
            spec, bool(held and live and live.rolling)
        )
        values = spec.to_helm_values()
        digest = values_digest(chart_version(spec.version), values)
        exists = await self.helm.release_exists(spec.release_name, spec.namespace)
        if exists and not recreated and current.applied_hash == digest:
            return replace(
                current,
                conditions=tuple(clear_apply_failure(current.conditions)),
                observed_generation=generation,
            )
        try:
            await self.helm.apply(
                release_name=spec.release_name,
                namespace=spec.namespace,
                chart_version=chart_version(spec.version),
                values=values,
                statefulset=spec.release_name,
                partition=hold_partition(spec.rollout.strategy, spec.replicas),
            )
        except RuntimeError as error:
            message = str(error)[-HELM_ERROR_CHARS:]
            failed = progress_condition("Progressing", False, "HelmApplyFailed", message)
            status = replace(
                current,
                conditions=tuple(set_condition(current.conditions, failed)),
                observed_generation=generation,
            )
            raise ReleaseApplyError(message, status) from error
        if spec.scaling.drain_on_scale_down and live and live.replicas > spec.replicas:
            await self.delete_departed_claims(spec)
        phase = ClusterPhase.UPGRADING if exists else ClusterPhase.PENDING
        conditions = set_condition(
            current.conditions,
            Condition(
                type="Progressing",
                status=ConditionStatus.TRUE,
                last_transition_time=now_utc(),
                reason="HelmReleaseApplied",
                message=f"Helm release {spec.release_name} applied",
            ),
        )
        return replace(
            current,
            phase=phase,
            replicas=spec.replicas,
            helm_release=spec.release_name,
            endpoint=spec.service_url(),
            version=spec.version,
            conditions=tuple(conditions),
            observed_generation=generation,
            applied_hash=digest,
        )

    async def delete_departed_claims(self, spec: ClusterSpec) -> None:
        """Delete the volumes of drained pods: their peers left consensus, so a later scale-up
        must start them empty instead of as a removed peer with no cluster."""
        claims = await self.kubernetes.list_volume_claims(spec.namespace, spec.volume_selector)
        for name in spec.departed_claims(c.name for c in claims):
            await self.kubernetes.delete_volume_claim(name, spec.namespace)

    async def prepare_scale_down(
        self, spec: ClusterSpec, live: StatefulSetStatus | None, current: ClusterStatus
    ) -> ClusterStatus:
        """Drain the peers a scale-down removes, or flag the shards it would strand."""
        if not live or live.replicas <= spec.replicas:
            return current
        if (
            spec.scaling.drain_on_scale_down
            and spec.rollout.strategy is RolloutStrategy.SHARD_HEALTH
            and live.rolling
        ):
            message = "scale-down waits for the pod restarts to finish"
            waiting = progress_condition("ScalingDown", True, "WaitingForRollout", message)
            raise RolloutInProgressError(
                message,
                replace(
                    current,
                    phase=ClusterPhase.UPGRADING,
                    conditions=tuple(set_condition(current.conditions, waiting)),
                ),
            )
        connection = await ResolveCluster(self.kubernetes).connect(spec)
        node = connection.nodes[0]
        if spec.scaling.drain_on_scale_down:
            return await self.drain(spec, node, current)
        return await self.warn_unsafe(spec, node, current)

    async def drain(
        self, spec: ClusterSpec, node: QdrantNode, current: ClusterStatus
    ) -> ClusterStatus:
        try:
            plan = await DrainPeers(self.qdrant).execute(spec, node)
        except ShardsUnsafeError as error:
            blocked = replace(
                current,
                conditions=tuple(
                    set_condition(
                        current.conditions,
                        progress_condition("ScalingDown", True, "ShardsUnsafe", str(error)),
                    )
                ),
            )
            raise ScaleDownPendingError(str(error), blocked) from error
        if not (plan.operations or plan.in_flight or plan.blocked):
            return replace(
                current,
                conditions=tuple(
                    set_condition(
                        current.conditions,
                        progress_condition(
                            "ScalingDown", False, "PeersRemoved", "removed peers are empty"
                        ),
                    )
                ),
            )
        message = describe_plan(plan)
        pending = replace(
            current,
            phase=ClusterPhase.UPGRADING,
            conditions=tuple(
                set_condition(
                    current.conditions,
                    progress_condition("ScalingDown", True, "DrainingPeers", message),
                )
            ),
        )
        raise ScaleDownPendingError(message, pending)

    async def warn_unsafe(
        self, spec: ClusterSpec, node: QdrantNode, current: ClusterStatus
    ) -> ClusterStatus:
        try:
            topology = await self.qdrant.cluster_topology(node)
            survey = await survey_collections(self.qdrant, node)
        except ConnectionError:
            return current
        plan = plan_evacuation(
            survey,
            departing_peers(topology, spec.release_name, spec.replicas),
            surviving_peers(topology, spec.release_name, spec.replicas),
        )
        stranded = [
            f"{o.collection}/{o.shard_id}"
            for o in plan.operations
            if o.kind is ShardOperationKind.MOVE
        ]
        stranded += [loss.describe() for loss in plan.lost]
        if not stranded:
            return current
        message = f"scale-down strands shard replicas: {', '.join(stranded[:10])}"
        conditions = set_condition(
            current.conditions,
            progress_condition("ScaleDownUnsafe", True, "ShardsOnRemovedPeers", message),
        )
        return replace(current, conditions=tuple(conditions))


@dataclass
class StepRollout:
    """Move the StatefulSet partition one pod at a time, each only once the cluster is healthy.

    Level-triggered: the only state is the partition and the pods' revision labels, so a
    restarted operator resumes where it stopped. The Readiness strategy just lifts the partition.
    """

    qdrant: QdrantPort
    kubernetes: KubernetesPort

    async def execute(self, spec: ClusterSpec, current: ClusterStatus) -> ClusterStatus:
        live = await self.kubernetes.get_statefulset_status(spec.release_name, spec.namespace)
        if not live:
            return current
        strategy = spec.rollout.strategy
        if strategy is RolloutStrategy.READINESS:
            await self.apply(spec, live.partition, next_partition(strategy, self.bare(live, spec)))
            return current
        pods = pod_revisions(
            spec.release_name,
            spec.replicas,
            await self.kubernetes.list_pod_states(spec.namespace, spec.pod_label_selector),
        )
        snapshot = replace(self.bare(live, spec), pods=pods)
        if snapshot.behind():
            connection = await ResolveCluster(self.kubernetes).connect(spec)
            health = await assess_health(self.qdrant, spec, connection.nodes, pods)
            snapshot = replace(snapshot, health=health)
        target = next_partition(strategy, snapshot)
        await self.apply(spec, live.partition, target)
        if not snapshot.behind():
            return current
        wait = rollout_wait(snapshot)
        progress = wait or RolloutWait("RestartingPod", f"releasing pod {target}")
        condition = progress_condition("Progressing", True, progress.reason, progress.message)
        return replace(
            current,
            phase=ClusterPhase.UPGRADING,
            conditions=tuple(set_condition(current.conditions, condition)),
        )

    @staticmethod
    def bare(live: StatefulSetStatus, spec: ClusterSpec) -> RolloutSnapshot:
        return RolloutSnapshot(live.partition, spec.replicas, live.update_revision, ())

    async def apply(self, spec: ClusterSpec, partition: int, target: int | None) -> None:
        if target is None or target == partition:
            return
        await self.kubernetes.set_statefulset_partition(spec.release_name, spec.namespace, target)


@dataclass
class ObserveCluster:
    """Lift StatefulSet readiness into the QdrantCluster status."""

    kubernetes: KubernetesPort

    async def execute(self, spec: ClusterSpec, current: ClusterStatus) -> ClusterStatus:
        statefulset = await self.kubernetes.get_statefulset_status(
            spec.release_name, spec.namespace
        )
        ready_replicas = statefulset.ready_replicas if statefulset else 0
        rolling = bool(statefulset and statefulset.rolling)
        ready = ready_replicas >= spec.replicas and not rolling
        phase = ClusterPhase.RUNNING if ready else self.pending_phase(current)
        conditions = set_condition(
            current.conditions,
            Condition(
                type="Ready",
                status=ConditionStatus.TRUE if ready else ConditionStatus.FALSE,
                last_transition_time=now_utc(),
                reason="AllReplicasReady" if ready else "ReplicasNotReady",
                message=self.ready_message(spec, statefulset, ready_replicas),
            ),
        )
        if ready and not apply_failed(current.conditions):
            conditions = set_condition(
                conditions,
                Condition(
                    type="Progressing",
                    status=ConditionStatus.FALSE,
                    last_transition_time=now_utc(),
                    reason="RolloutComplete",
                    message="StatefulSet rollout complete",
                ),
            )
        return replace(
            current,
            phase=phase,
            replicas=spec.replicas,
            ready_replicas=ready_replicas,
            updated_replicas=statefulset.updated_replicas if statefulset else 0,
            current_revision=statefulset.current_revision if statefulset else None,
            update_revision=statefulset.update_revision if statefulset else None,
            endpoint=spec.service_url(),
            conditions=tuple(conditions),
        )

    @staticmethod
    def ready_message(
        spec: ClusterSpec, statefulset: StatefulSetStatus | None, ready_replicas: int
    ) -> str:
        message = f"{ready_replicas}/{spec.replicas} replicas ready"
        if not statefulset or not statefulset.rolling:
            return message
        return f"{message}; rolling update {statefulset.updated_replicas}/{spec.replicas} updated"

    @staticmethod
    def pending_phase(current: ClusterStatus) -> ClusterPhase:
        if current.phase in (ClusterPhase.UPGRADING, ClusterPhase.FAILED):
            return current.phase
        return ClusterPhase.PENDING


@dataclass
class RecoverDeadPeers:
    """Re-replicate shards off peers no pod answers for, then remove those peers.

    Two cases count as dead. With every pod answering: a peer whose ordinal now reports another
    peer id, or is beyond the replica count. With pods not answering: a peer that one of them
    names in Qdrant's join error ("peer URI ... already used by peer N"), which is what a pod
    rebuilt on an empty volume logs while it crash-loops. A pod that is merely slow is never
    judged. Nothing is done while the StatefulSet is resizing. Under Readiness nothing is done
    while it rolls either; under ShardHealth only while a pod the StatefulSet controller may
    restart (ordinal at or above the partition) is behind or not Ready, so a held rollout does
    not stall on a leftover dead peer.
    """

    qdrant: QdrantPort
    kubernetes: KubernetesPort

    async def execute(self, spec: ClusterSpec, current: ClusterStatus) -> ClusterStatus:
        statefulset = await self.kubernetes.get_statefulset_status(
            spec.release_name, spec.namespace
        )
        if not statefulset or statefulset.replicas != spec.replicas:
            return current
        if await self.restarting(spec, statefulset):
            return current
        connection = await ResolveCluster(self.kubernetes).connect(spec)
        reports = [await self.topology(node) for node in connection.nodes]
        answering = {ordinal: t for ordinal, t in enumerate(reports) if t}
        if not answering:
            return current
        everyone_ready = len(answering) == spec.replicas
        if everyone_ready and statefulset.ready_replicas < spec.replicas:
            return current
        ordinal, topology = next(iter(answering.items()))
        dead = await self.find_dead(spec, answering, topology)
        if not dead and not everyone_ready:
            return current
        if not dead:
            return self.with_condition(current, False, "NoDeadPeers", "every peer has a pod")
        node = connection.nodes[ordinal]
        plan = plan_recovery(
            await survey_collections(self.qdrant, node),
            dead,
            sorted(t.peer_id for t in answering.values()),
        )
        if plan.lost:
            lost = ", ".join(loss.describe() for loss in plan.lost)
            return self.with_condition(
                current, True, "ShardsLost", f"no healthy replica to restore from: {lost}"
            )
        for operation in plan.operations:
            await self.qdrant.apply_shard_operation(node, operation)
        if plan.operations or plan.in_flight or plan.blocked:
            return self.with_condition(current, True, "RestoringReplicas", describe_plan(plan))
        for peer_id in sorted(dead):
            await self.qdrant.remove_peer(node, peer_id, force=True)
        return self.with_condition(
            current, False, "DeadPeersRemoved", f"removed {len(dead)} dead peers"
        )

    async def restarting(self, spec: ClusterSpec, statefulset: StatefulSetStatus) -> bool:
        if spec.rollout.strategy is RolloutStrategy.READINESS:
            return statefulset.rolling
        pods = pod_revisions(
            spec.release_name,
            spec.replicas,
            await self.kubernetes.list_pod_states(spec.namespace, spec.pod_label_selector),
        )
        return not released_pods_settled(statefulset.partition, statefulset.update_revision, pods)

    async def topology(self, node: QdrantNode) -> ClusterTopology | None:
        try:
            return await self.qdrant.cluster_topology(node)
        except ConnectionError:
            return None

    async def find_dead(
        self,
        spec: ClusterSpec,
        answering: Mapping[int, ClusterTopology],
        topology: ClusterTopology,
    ) -> frozenset[int]:
        silent = [o for o in range(spec.replicas) if o not in answering]
        if not silent:
            pod_peer_ids = {o: t.peer_id for o, t in answering.items()}
            return dead_peers(topology, pod_peer_ids, spec.release_name, spec.replicas)
        known = {p.peer_id for p in topology.peers}
        logs = [
            await self.kubernetes.crash_log(
                f"{spec.release_name}-{o}", spec.namespace, CRASH_LOG_LINES
            )
            for o in silent
        ]
        return frozenset(
            peer_id for log in logs if (peer_id := replaced_peer(log)) and peer_id in known
        )

    @staticmethod
    def with_condition(
        current: ClusterStatus, active: bool, reason: str, message: str
    ) -> ClusterStatus:
        condition = progress_condition("DeadPeers", active, reason, message)
        return replace(current, conditions=tuple(set_condition(current.conditions, condition)))


@dataclass
class RebalanceShards:
    """Bring every collection to its replication factor and spread replicas evenly.

    One step per collection per call, only while the StatefulSet is settled and every pod
    answers; covers a scale-up (new peers start empty), a restore that recovered every
    node's snapshot (extra replicas), and peers that came back empty after a repair.
    """

    qdrant: QdrantPort
    kubernetes: KubernetesPort

    async def execute(self, spec: ClusterSpec, current: ClusterStatus) -> ClusterStatus:
        statefulset = await self.kubernetes.get_statefulset_status(
            spec.release_name, spec.namespace
        )
        settled = (
            statefulset
            and statefulset.replicas == spec.replicas
            and statefulset.ready_replicas >= spec.replicas
            and not statefulset.rolling
        )
        if not settled:
            return current
        connection = await ResolveCluster(self.kubernetes).connect(spec)
        try:
            live = sorted(
                [(await self.qdrant.cluster_topology(n)).peer_id for n in connection.nodes]
            )
            node = connection.nodes[0]
            plan = plan_rebalance(await survey_collections(self.qdrant, node), live)
        except ConnectionError:
            return current
        for operation in plan.operations:
            await self.qdrant.apply_shard_operation(node, operation)
        if plan.operations or plan.in_flight:
            return self.with_condition(current, True, "Rebalancing", describe_plan(plan))
        return self.with_condition(
            current,
            False,
            "Balanced",
            "replicas match the replication factor and are spread evenly",
        )

    @staticmethod
    def with_condition(
        current: ClusterStatus, active: bool, reason: str, message: str
    ) -> ClusterStatus:
        condition = progress_condition("Rebalancing", active, reason, message)
        return replace(current, conditions=tuple(set_condition(current.conditions, condition)))


@dataclass
class DeleteCluster:
    """Uninstall the release; with reclaimPolicy Delete also delete its volume claims."""

    helm: HelmPort
    kubernetes: KubernetesPort

    async def execute(self, spec: ClusterSpec) -> None:
        if await self.helm.release_exists(spec.release_name, spec.namespace):
            await self.helm.uninstall(spec.release_name, spec.namespace)
        if spec.persistence.reclaim_policy is not ReclaimPolicy.DELETE:
            return
        claims = await self.kubernetes.list_volume_claims(spec.namespace, spec.volume_selector)
        for claim in claims:
            await self.kubernetes.delete_volume_claim(claim.name, spec.namespace)


@dataclass
class ExecuteBackup:
    qdrant: QdrantPort
    storage: StoragePort
    kubernetes: KubernetesPort

    async def execute(
        self, spec: BackupSpec, ref: ResourceRef, previous: BackupStatus | None = None
    ) -> BackupStatus:
        start_time = (previous.start_time if previous else None) or now_utc()
        connection = await ResolveCluster(self.kubernetes).execute(spec.cluster_ref)
        credentials = await read_credentials(self.kubernetes, spec.storage.credentials_secret_ref)
        await self.kubernetes.patch_status(
            ref, BackupStatus(phase=BackupPhase.IN_PROGRESS, start_time=start_time).to_dict()
        )

        collections = spec.collections or tuple(
            await self.qdrant.list_collections(connection.service)
        )
        await CleanupBackupSnapshots(self.qdrant, self.kubernetes).delete_leftovers(
            connection,
            collections,
            start_time,
            previous.in_progress_snapshot if previous else None,
        )
        await self.storage.abort_uploads(spec.storage, credentials, spec.root_key)
        statuses = [
            await self.backup_collection(spec, ref, credentials, connection, collection)
            for collection in collections
        ]
        completed = [s for s in statuses if s.status == BackupPhase.COMPLETED]
        failed = [s for s in statuses if s.status == BackupPhase.FAILED]

        manifest = BackupManifest(
            backup_name=spec.name,
            cluster=spec.cluster_ref,
            node_count=len(connection.nodes),
            created_at=start_time,
            collections=tuple(CollectionBackup(s.name, s.snapshots) for s in completed),
        )
        await self.storage.put_object(
            spec.storage,
            credentials,
            join_key(spec.root_key, MANIFEST_KEY),
            json.dumps(manifest.to_dict()).encode(),
        )

        completion_time = now_utc()
        phase = BackupPhase.FAILED if failed else BackupPhase.COMPLETED
        return BackupStatus(
            phase=phase,
            start_time=start_time,
            completion_time=completion_time,
            expires_at=spec.expires_at(completion_time),
            s3_path=spec.storage.uri(spec.name),
            total_size=format_size(manifest.size_bytes),
            collections=tuple(statuses),
            error=failed[0].error if failed else None,
            conditions=(
                Condition(
                    type="Complete",
                    status=ConditionStatus.FALSE if failed else ConditionStatus.TRUE,
                    last_transition_time=completion_time,
                    reason="BackupFailed" if failed else "BackupCompleted",
                    message=f"Backed up {len(completed)}/{len(collections)} collections",
                ),
            ),
        )

    async def backup_collection(
        self,
        spec: BackupSpec,
        ref: ResourceRef,
        credentials: S3Credentials,
        connection: ClusterConnection,
        collection: str,
    ) -> CollectionBackupStatus:
        try:
            records = [
                await self.backup_node(spec, ref, credentials, node, index, collection)
                for index, node in enumerate(connection.nodes)
            ]
        except Exception as error:
            return CollectionBackupStatus(
                name=collection, status=BackupPhase.FAILED, error=str(error)
            )
        return CollectionBackupStatus(
            name=collection, status=BackupPhase.COMPLETED, snapshots=tuple(records)
        )

    async def backup_node(
        self,
        spec: BackupSpec,
        ref: ResourceRef,
        credentials: S3Credentials,
        node: QdrantNode,
        index: int,
        collection: str,
    ) -> SnapshotRecord:
        """Snapshot one node's shards of a collection and stream them straight into the bucket."""
        snapshot = await self.qdrant.create_snapshot(node, collection)
        key = spec.snapshot_key(collection, index, snapshot.name)
        marker = InProgressSnapshot(index, collection, snapshot.name)
        await self.kubernetes.patch_status(ref, {"inProgressSnapshot": marker.to_dict()})
        try:
            size = await self.storage.upload_stream(
                spec.storage,
                credentials,
                key,
                self.qdrant.stream_snapshot(node, collection, snapshot.name),
            )
        finally:
            await self.qdrant.delete_snapshot(node, collection, snapshot.name)
            await self.kubernetes.patch_status(ref, {"inProgressSnapshot": None})
        return SnapshotRecord(
            node_index=index,
            key=key,
            snapshot_name=snapshot.name,
            size_bytes=size,
            checksum=snapshot.checksum,
        )


@dataclass
class CleanupBackupSnapshots:
    """Delete node snapshots an interrupted backup attempt left behind."""

    qdrant: QdrantPort
    kubernetes: KubernetesPort

    async def execute(self, spec: BackupSpec, status: BackupStatus) -> int:
        if not status.start_time or status.phase == BackupPhase.COMPLETED:
            return 0
        connection = await ResolveCluster(self.kubernetes).execute(spec.cluster_ref)
        collections = spec.collections or tuple(
            await self.qdrant.list_collections(connection.service)
        )
        return await self.delete_leftovers(
            connection, collections, status.start_time, status.in_progress_snapshot
        )

    async def delete_leftovers(
        self,
        connection: ClusterConnection,
        collections: tuple[str, ...],
        start_time: datetime,
        marker: InProgressSnapshot | None,
    ) -> int:
        """Remove snapshots created at or after start_time, plus the one the marker names."""
        targets = [
            (node, collection, name)
            for index, node in enumerate(connection.nodes)
            for collection in collections
            for name in await self.leftover_names(node, index, collection, start_time, marker)
        ]
        for node, collection, name in targets:
            await self.qdrant.delete_snapshot(node, collection, name)
        return len(targets)

    async def leftover_names(
        self,
        node: QdrantNode,
        index: int,
        collection: str,
        start_time: datetime,
        marker: InProgressSnapshot | None,
    ) -> set[str]:
        listed = await self.qdrant.list_snapshots(node, collection)
        names = {s.name for s in listed if s.created_at and s.created_at >= start_time}
        if marker and marker.node_index == index and marker.collection == collection:
            names.add(marker.snapshot)
        return names


@dataclass
class DeleteBackupData:
    storage: StoragePort
    kubernetes: KubernetesPort

    async def execute(self, spec: BackupSpec) -> int:
        """Delete the backup's objects and abort its unfinished uploads; returns objects deleted."""
        credentials = await read_credentials(self.kubernetes, spec.storage.credentials_secret_ref)
        await self.storage.abort_uploads(spec.storage, credentials, spec.root_key)
        return await self.storage.delete_prefix(spec.storage, credentials, spec.root_key)


@dataclass
class ExpireBackup:
    """Delete a QdrantBackup once its retentionDays have passed."""

    kubernetes: KubernetesPort

    async def execute(self, ref: ResourceRef, status: BackupStatus, now: datetime) -> bool:
        if not status.expires_at or status.expires_at > now:
            return False
        await self.kubernetes.delete_custom_resource(ref)
        return True


@dataclass(frozen=True)
class RestoreSource:
    storage: S3StorageSpec
    root_key: str
    description: str


@dataclass
class ExecuteRestore:
    qdrant: QdrantPort
    storage: StoragePort
    kubernetes: KubernetesPort
    indexing_poll_seconds: float = 5.0

    async def execute(self, spec: RestoreSpec, ref: ResourceRef) -> RestoreStatus:
        start_time = now_utc()
        source = await self.resolve_source(spec)
        credentials = await read_credentials(self.kubernetes, source.storage.credentials_secret_ref)
        connection = await ResolveCluster(self.kubernetes).execute(spec.target_cluster_ref)

        await self.kubernetes.patch_status(
            ref,
            RestoreStatus(
                phase=RestorePhase.DOWNLOADING,
                start_time=start_time,
                source_backup=source.description,
            ).to_dict(),
        )
        manifest = BackupManifest.from_dict(
            json.loads(
                await self.storage.get_object(
                    source.storage, credentials, join_key(source.root_key, MANIFEST_KEY)
                )
            )
        )
        if manifest.node_count != len(connection.nodes):
            raise ValueError(
                f"Backup was taken from {manifest.node_count} nodes but target cluster has "
                f"{len(connection.nodes)}; node counts must match"
            )
        collections = spec.select_collections(manifest.collection_names)

        restored: list[RestoredCollection] = []
        for index, collection in enumerate(collections):
            await self.kubernetes.patch_status(
                ref,
                RestoreStatus(
                    phase=RestorePhase.RESTORING,
                    start_time=start_time,
                    source_backup=source.description,
                    restored_collections=tuple(restored),
                    progress=RestoreProgress(len(collections), index, collection),
                ).to_dict(),
            )
            restored.append(
                await self.restore_collection(
                    spec, source, credentials, connection, manifest, collection
                )
            )

        failed = [r for r in restored if r.status == RestorePhase.FAILED]
        completion_time = now_utc()
        return RestoreStatus(
            phase=RestorePhase.FAILED if failed else RestorePhase.COMPLETED,
            start_time=start_time,
            completion_time=completion_time,
            source_backup=source.description,
            restored_collections=tuple(restored),
            progress=RestoreProgress(len(collections), len(collections) - len(failed)),
            error=failed[0].error if failed else None,
            conditions=(
                Condition(
                    type="Complete",
                    status=ConditionStatus.FALSE if failed else ConditionStatus.TRUE,
                    last_transition_time=completion_time,
                    reason="RestoreFailed" if failed else "RestoreCompleted",
                    message=f"Restored {len(restored) - len(failed)}/{len(collections)}",
                ),
            ),
        )

    async def resolve_source(self, spec: RestoreSpec) -> RestoreSource:
        if spec.source_s3:
            return RestoreSource(
                storage=spec.source_s3,
                root_key=spec.source_s3.prefix,
                description=spec.source_s3.uri(),
            )
        if not spec.backup_ref:
            raise ValueError("QdrantRestore needs either spec.backupRef or spec.source.s3")
        body = await self.kubernetes.get_custom_resource(spec.backup_ref.to_resource_ref())
        if not body:
            raise LookupError(
                f"QdrantBackup {spec.backup_ref.namespace}/{spec.backup_ref.name} not found"
            )
        status = BackupStatus.from_dict(body.get("status", {}))
        if status.phase != BackupPhase.COMPLETED:
            raise SourceNotReadyError(
                f"QdrantBackup {spec.backup_ref.name} is {status.phase.value}, not Completed"
            )
        backup = BackupSpec.from_dict(body["spec"], body["metadata"])
        return RestoreSource(
            storage=backup.storage, root_key=backup.root_key, description=spec.backup_ref.name
        )

    async def restore_collection(
        self,
        spec: RestoreSpec,
        source: RestoreSource,
        credentials: S3Credentials,
        connection: ClusterConnection,
        manifest: BackupManifest,
        collection: str,
    ) -> RestoredCollection:
        target = spec.target_name(collection)
        original = collection if collection != target else None
        backup = manifest.collection(collection)
        if not backup:
            return RestoredCollection(
                name=target,
                original_name=original,
                status=RestorePhase.FAILED,
                error=f"Collection {collection} missing from manifest",
            )
        try:
            for record in backup.snapshots:
                await self.recover_node(spec, source, credentials, connection, target, record)
            points = await self.await_indexing(spec, connection.service, target)
        except Exception as error:
            return RestoredCollection(
                name=target, original_name=original, status=RestorePhase.FAILED, error=str(error)
            )
        return RestoredCollection(
            name=target,
            original_name=original,
            status=RestorePhase.COMPLETED,
            size=format_size(backup.size_bytes),
            points_count=points,
        )

    async def recover_node(
        self,
        spec: RestoreSpec,
        source: RestoreSource,
        credentials: S3Credentials,
        connection: ClusterConnection,
        target: str,
        record: SnapshotRecord,
    ) -> None:
        """Hand the node a presigned URL so the snapshot flows bucket → node without touching us."""
        location = await self.storage.presigned_get_url(
            source.storage, credentials, record.key, PRESIGNED_URL_TTL_SECONDS
        )
        await self.qdrant.recover_snapshot(
            connection.nodes[record.node_index], target, location, spec.priority, record.checksum
        )

    async def await_indexing(
        self, spec: RestoreSpec, node: QdrantNode, collection: str
    ) -> int | None:
        deadline = asyncio.get_running_loop().time() + spec.indexing_timeout_seconds
        while True:
            info = await self.qdrant.collection_info(node, collection)
            if not spec.wait_for_indexing or info.get("status") == "green":
                return info.get("points_count")
            if asyncio.get_running_loop().time() >= deadline:
                raise TimeoutError(indexing_timeout_message(spec, collection, info))
            await asyncio.sleep(self.indexing_poll_seconds)


@dataclass
class ProcessSchedule:
    kubernetes: KubernetesPort

    async def execute(
        self,
        spec: BackupScheduleSpec,
        status: BackupScheduleStatus,
        owner: Mapping[str, Any],
        now: datetime,
    ) -> BackupScheduleStatus:
        records = await self.list_backups(spec)
        await self.apply_retention(spec, records)
        active = [r for r in records if not r.finished]
        finished = [r for r in records if r.finished]

        if spec.suspend:
            return self.status_for(SchedulePhase.SUSPENDED, status, records, active, None)

        baseline = status.last_schedule_time or parse_time(
            owner["metadata"].get("creationTimestamp")
        )
        slot = spec.due(now, baseline)
        launched = await self.launch(spec, owner, slot, active) if slot else None
        last_schedule_time = slot if launched else status.last_schedule_time
        next_time = spec.next_slot(now)
        result = self.status_for(SchedulePhase.ACTIVE, status, records, active, next_time)
        if not launched:
            return replace(result, last_schedule_time=last_schedule_time)
        return replace(
            result,
            last_schedule_time=last_schedule_time,
            active_backup=launched,
            last_backup_name=launched if not finished else result.last_backup_name,
        )

    async def list_backups(self, spec: BackupScheduleSpec) -> list[BackupRecord]:
        bodies = await self.kubernetes.list_custom_resources(
            BACKUPS, spec.namespace, spec.label_selector
        )
        return sorted(
            (BackupRecord.from_resource(b) for b in bodies),
            key=lambda r: r.creation_time,
            reverse=True,
        )

    async def apply_retention(self, spec: BackupScheduleSpec, records: list[BackupRecord]) -> None:
        for expired in spec.retention_policy.expired(r for r in records if r.finished):
            await self.kubernetes.delete_custom_resource(
                ResourceRef(BACKUPS, expired.name, spec.namespace)
            )
            records.remove(expired)

    async def launch(
        self,
        spec: BackupScheduleSpec,
        owner: Mapping[str, Any],
        slot: datetime,
        active: list[BackupRecord],
    ) -> str | None:
        if active and spec.concurrency_policy == ConcurrencyPolicy.FORBID:
            return None
        if active and spec.concurrency_policy == ConcurrencyPolicy.REPLACE:
            for record in active:
                await self.kubernetes.delete_custom_resource(
                    ResourceRef(BACKUPS, record.name, spec.namespace)
                )
        name = spec.backup_name(slot)
        await self.kubernetes.create_custom_resource(spec.to_backup_resource(name, owner))
        return name

    @staticmethod
    def status_for(
        phase: SchedulePhase,
        status: BackupScheduleStatus,
        records: list[BackupRecord],
        active: list[BackupRecord],
        next_time: datetime | None,
    ) -> BackupScheduleStatus:
        latest = next((r for r in records if r.finished), None)
        return BackupScheduleStatus(
            phase=phase,
            last_schedule_time=status.last_schedule_time,
            last_backup_time=latest.completion_time if latest else None,
            last_backup_name=latest.name if latest else None,
            last_backup_status=latest.phase.value if latest else None,
            next_backup_time=next_time,
            active_backup=active[0].name if active else None,
            recent_backups=tuple(records[:RECENT_BACKUPS_LIMIT]),
        )


@dataclass
class ReconcileCollection:
    """Make the Qdrant collection, its payload indexes and aliases match the spec.

    Runs on every spec change and on a timer, so a hand-deleted collection or index comes
    back. Only fields the spec declares are compared. A field the operator applied earlier and the
    spec has since dropped goes back to Qdrant's default; the rest is left alone.
    """

    qdrant: QdrantPort
    kubernetes: KubernetesPort

    async def execute(
        self, spec: CollectionSpec, generation: int | None, current: CollectionStatus
    ) -> CollectionStatus:
        connection = await ResolveCluster(self.kubernetes).execute(spec.cluster_ref)
        node = connection.service
        if not await self.qdrant.ready(node):
            raise ClusterNotReadyError(f"QdrantCluster {spec.cluster_ref.name} is not ready")

        name = spec.collection_name
        if not await self.qdrant.collection_exists(node, name):
            await self.qdrant.create_collection(node, name, spec.create_body())
        info = await self.qdrant.collection_info(node, name)
        config = info.get("config", {})

        if not is_subset(spec.immutable_config(), config):
            return self.status_for(
                spec,
                generation,
                current,
                info,
                CollectionPhase.DEGRADED,
                reason="ImmutableFieldMismatch",
                message="vectors, shardNumber or shardingMethod differ from the live collection; "
                "only re-creating the collection can change them",
            )
        resets = spec.resets(config, current.applied_fields)
        update = spec.update_body(config, resets)
        if update:
            await self.qdrant.update_collection(node, name, update)
        reset_names = [f"{key}.{name}" for key, fields in resets.items() for name in fields]

        shard_keys = await self.reconcile_shard_keys(node, spec)
        indexes = await self.reconcile_indexes(
            node, spec, info.get("payload_schema", {}), current.payload_indexes
        )
        aliases = await self.reconcile_aliases(node, spec, current.aliases)
        return replace(
            self.status_for(
                spec,
                generation,
                current,
                info,
                CollectionPhase.READY,
                reason="CollectionReady",
                message=f"Collection {name} matches the spec"
                + (f"; reset to Qdrant defaults: {', '.join(reset_names)}" if reset_names else ""),
            ),
            applied_fields=spec.declared_fields(),
            payload_indexes=indexes,
            aliases=aliases,
            shard_keys=shard_keys,
        )

    async def reconcile_shard_keys(
        self, node: QdrantNode, spec: CollectionSpec
    ) -> tuple[str | int, ...]:
        """Create declared shard keys; never drop one, since that deletes its points."""
        if spec.sharding_method != "custom":
            return ()
        existing = await self.qdrant.list_shard_keys(node, spec.collection_name)
        for shard_key in spec.shard_keys:
            if shard_key.key in existing:
                continue
            await self.qdrant.create_shard_key(node, spec.collection_name, shard_key.to_body())
            existing.append(shard_key.key)
        return tuple(existing)

    async def reconcile_indexes(
        self,
        node: QdrantNode,
        spec: CollectionSpec,
        payload_schema: Mapping[str, Any],
        managed: tuple[str, ...],
    ) -> tuple[str, ...]:
        """Create or replace declared indexes; drop only indexes this resource created earlier."""
        desired = {index.field_name: index for index in spec.payload_indexes}
        for field_name, index in desired.items():
            actual = payload_schema.get(field_name)
            if actual and index.satisfied_by(actual):
                continue
            if actual:
                await self.qdrant.delete_payload_index(node, spec.collection_name, field_name)
            await self.qdrant.create_payload_index(
                node, spec.collection_name, field_name, index.field_schema()
            )
        for field_name in managed:
            if field_name in desired or field_name not in payload_schema:
                continue
            await self.qdrant.delete_payload_index(node, spec.collection_name, field_name)
        return tuple(sorted(desired))

    async def reconcile_aliases(
        self, node: QdrantNode, spec: CollectionSpec, managed: tuple[str, ...]
    ) -> tuple[str, ...]:
        """Point declared aliases here; drop only aliases this resource created earlier."""
        actual = await self.qdrant.list_aliases(node)
        name = spec.collection_name
        actions: list[JsonDict] = [
            {"create_alias": {"collection_name": name, "alias_name": alias}}
            for alias in spec.aliases
            if actual.get(alias) != name
        ] + [
            {"delete_alias": {"alias_name": alias}}
            for alias in managed
            if alias not in spec.aliases and actual.get(alias) == name
        ]
        if actions:
            await self.qdrant.update_aliases(node, actions)
        return tuple(sorted(spec.aliases))

    @staticmethod
    def status_for(
        spec: CollectionSpec,
        generation: int | None,
        current: CollectionStatus,
        info: Mapping[str, Any],
        phase: CollectionPhase,
        reason: str,
        message: str,
    ) -> CollectionStatus:
        ready = phase == CollectionPhase.READY
        return CollectionStatus(
            phase=phase,
            collection_name=spec.collection_name,
            health=info.get("status"),
            points_count=info.get("points_count"),
            indexed_vectors_count=info.get("indexed_vectors_count"),
            segments_count=info.get("segments_count"),
            payload_indexes=current.payload_indexes,
            aliases=current.aliases,
            applied_fields=current.applied_fields,
            error=None if ready else message,
            observed_generation=generation,
            conditions=tuple(
                set_condition(
                    current.conditions,
                    Condition(
                        type="Ready",
                        status=ConditionStatus.TRUE if ready else ConditionStatus.FALSE,
                        last_transition_time=now_utc(),
                        reason=reason,
                        message=message,
                    ),
                )
            ),
        )


@dataclass
class DeleteCollection:
    qdrant: QdrantPort
    kubernetes: KubernetesPort

    async def execute(self, spec: CollectionSpec) -> bool:
        """Drop the collection when the policy says so. Returns whether it was dropped."""
        if spec.deletion_policy == DeletionPolicy.RETAIN:
            return False
        try:
            connection = await ResolveCluster(self.kubernetes).execute(spec.cluster_ref)
        except ClusterNotFoundError:
            return False
        await self.qdrant.delete_collection(connection.service, spec.collection_name)
        return True


@dataclass
class IssueAccessKey:
    """Sign a Qdrant RBAC token with the cluster's API key and keep it in a Secret.

    The token is re-issued when the spec changes, when the cluster's API key changes, when
    the Secret disappears, or when the renewal time is reached.
    """

    kubernetes: KubernetesPort
    token: TokenPort

    async def execute(
        self,
        spec: AccessKeySpec,
        generation: int | None,
        current: AccessKeyStatus,
        owner: Mapping[str, Any],
        now: datetime,
    ) -> AccessKeyStatus:
        body = await self.kubernetes.get_custom_resource(spec.cluster_ref.to_resource_ref())
        if not body:
            raise ClusterNotFoundError(f"QdrantCluster {spec.cluster_ref.name} not found")
        cluster = ClusterSpec.from_dict(body["spec"], body["metadata"])
        if not cluster.api_key.jwt_rbac:
            return self.blocked(
                current, generation, "JwtRbacDisabled", "cluster spec.apiKey.jwtRbac is false"
            )
        signing_key_ref = cluster.api_key.resolve_secret_ref(cluster.name, cluster.namespace)
        if not signing_key_ref:
            return self.blocked(current, generation, "ApiKeyMissing", "cluster has no apiKey")

        api_key = await self.kubernetes.get_secret_value(signing_key_ref)
        fingerprint = key_fingerprint(api_key)
        if current.token_current(generation, fingerprint, await self.secret_present(spec), now):
            return current

        try:
            await self.kubernetes.apply_secret(
                spec.secret_name,
                spec.namespace,
                {
                    TOKEN_SECRET_KEY: self.token.sign(spec.claims(now), api_key),
                    URL_SECRET_KEY: cluster.service_url(),
                },
                owner_reference(owner),
            )
        except PermissionError as error:
            return self.blocked(current, generation, "SecretNotOwned", str(error))
        return AccessKeyStatus(
            phase=AccessKeyPhase.READY,
            secret_ref=spec.token_secret_ref(),
            issued_at=now,
            expires_at=spec.expires_at(now),
            renew_at=spec.renew_at(now),
            key_fingerprint=fingerprint,
            observed_generation=generation,
            conditions=tuple(
                set_condition(
                    current.conditions,
                    Condition(
                        type="Ready",
                        status=ConditionStatus.TRUE,
                        last_transition_time=now,
                        reason="TokenIssued",
                        message=f"Token written to secret {spec.secret_name}",
                    ),
                )
            ),
        )

    async def secret_present(self, spec: AccessKeySpec) -> bool:
        try:
            await self.kubernetes.get_secret_value(spec.token_secret_ref())
        except KeyError:
            return False
        return True

    @staticmethod
    def blocked(
        current: AccessKeyStatus, generation: int | None, reason: str, message: str
    ) -> AccessKeyStatus:
        return replace(
            current,
            phase=AccessKeyPhase.PENDING,
            error=message,
            observed_generation=generation,
            conditions=tuple(
                set_condition(
                    current.conditions,
                    Condition(
                        type="Ready",
                        status=ConditionStatus.FALSE,
                        last_transition_time=now_utc(),
                        reason=reason,
                        message=message,
                    ),
                )
            ),
        )


def resumed_sync(previous: CollectionMigration) -> dict[str, Any]:
    return {
        "points_updated": previous.points_updated,
        "points_deleted": previous.points_deleted,
        "sync_pass": previous.sync_pass,
        "sync_offset": previous.sync_offset,
        "sync_pass_changes": previous.sync_pass_changes,
    }


@dataclass
class ExecuteMigration:
    """Copy collections point by point from any Qdrant into a managed cluster.

    Scroll pages on the source are upserted on the target, grouped by the shard key each point
    lands under (its source key by default, or a fixed key / payload field from spec.target).
    Missing target collections are created from the source configuration and payload schema,
    shard and replication counts left to the target cluster unless the spec overrides them.
    Progress and the scroll offset are recorded in status every spec.checkpoint_batches batches,
    so a re-run after an interruption resumes there (at most that many batches are copied again);
    upserts are idempotent by point id. pointsTotal is re-read from the source at each checkpoint.
    After a scroll reaches the end, exact source and target counts are compared; while the target
    holds fewer points, up to spec.verify_passes further passes scroll the source from the start
    (the pass number is kept in status so a restart resumes the same pass). A collection is
    Completed only when the target holds at least as many points as the source, otherwise Failed
    with both counts. Connection errors and timeouts propagate so the handler
    retries with backoff instead of recording the collection as failed.

    With spec.sync the verify passes are replaced by sync passes. After the copy, source and
    target are scrolled in point-ID order window by window (one source page and the target
    points in the same ID range); points missing or different on the target, by a hash of the
    canonical JSON of payload and vectors, are upserted again and target points the source no
    longer has are deleted. The pass number and the ID the pass resumes from are checkpointed in
    status like the copy offset. Another pass runs only while the previous one changed something,
    up to spec.sync_max_passes. Afterwards the exact counts decide Completed or Failed.
    """

    qdrant: QdrantPort
    kubernetes: KubernetesPort

    async def execute(
        self, spec: MigrationSpec, ref: ResourceRef, current: MigrationStatus
    ) -> MigrationStatus:
        start_time = current.start_time or now_utc()
        source = await self.resolve_source(spec)
        target = (await ResolveCluster(self.kubernetes).execute(spec.target_cluster_ref)).service
        if not await self.qdrant.ready(target):
            raise ClusterNotReadyError(f"QdrantCluster {spec.target_cluster_ref.name} not ready")

        names = spec.select_collections(await self.qdrant.list_collections(source))
        collections = [await self.plan_collection(spec, source, current, name) for name in names]
        running = MigrationStatus(
            phase=MigrationPhase.RUNNING,
            start_time=start_time,
            source=spec.source.description,
            collections=tuple(collections),
        )
        await self.kubernetes.patch_status(ref, running.to_dict())

        for index, collection in enumerate(collections):
            if collection.status == MigrationPhase.COMPLETED:
                continue
            report = partial(self.report_progress, ref, running, collections, index, source)
            collections[index] = await self.migrate_collection(
                spec, source, target, collection, report
            )
            await self.kubernetes.patch_status(
                ref, replace(running, collections=tuple(collections)).to_dict()
            )

        failed = [c for c in collections if c.status == MigrationPhase.FAILED]
        completion_time = now_utc()
        return MigrationStatus(
            phase=MigrationPhase.FAILED if failed else MigrationPhase.COMPLETED,
            start_time=start_time,
            completion_time=completion_time,
            source=spec.source.description,
            collections=tuple(collections),
            error=failed[0].error if failed else None,
            conditions=(
                Condition(
                    type="Complete",
                    status=ConditionStatus.FALSE if failed else ConditionStatus.TRUE,
                    last_transition_time=completion_time,
                    reason="MigrationFailed" if failed else "MigrationCompleted",
                    message=f"Copied {len(collections) - len(failed)}/{len(collections)}",
                ),
            ),
        )

    async def plan_collection(
        self, spec: MigrationSpec, source: QdrantNode, current: MigrationStatus, name: str
    ) -> CollectionMigration:
        """A fresh entry, or the previous run's so a finished copy is kept and a partial resumes."""
        previous = current.resume_point(name)
        if previous and previous.status == MigrationPhase.COMPLETED:
            return previous
        running = bool(previous and previous.status == MigrationPhase.RUNNING)
        return CollectionMigration(
            name=name,
            target_name=spec.target_name(name),
            status=MigrationPhase.PENDING,
            points_total=await self.qdrant.count_points(source, name),
            points_copied=previous.points_copied if previous else 0,
            offset=previous.offset if previous else None,
            verify_pass=previous.verify_pass if previous and running else 0,
            **(resumed_sync(previous) if previous and running else {}),
        )

    async def report_progress(
        self,
        ref: ResourceRef,
        running: MigrationStatus,
        collections: list[CollectionMigration],
        index: int,
        source: QdrantNode,
        progress: CollectionMigration,
    ) -> None:
        collections[index] = replace(
            progress,
            status=MigrationPhase.RUNNING,
            points_total=await self.qdrant.count_points(source, progress.name),
        )
        await self.kubernetes.patch_status(
            ref, replace(running, collections=tuple(collections)).to_dict()
        )

    async def resolve_source(self, spec: MigrationSpec) -> QdrantNode:
        if spec.source.cluster_ref:
            return (await ResolveCluster(self.kubernetes).execute(spec.source.cluster_ref)).service
        endpoint = spec.source.endpoint
        if not endpoint:
            raise ValueError("QdrantMigration source has neither clusterRef nor endpoint")
        api_key = (
            await self.kubernetes.get_secret_value(endpoint.api_key_ref)
            if endpoint.api_key_ref
            else None
        )
        ca_cert = (
            await self.kubernetes.get_secret_value(endpoint.ca_ref) if endpoint.ca_ref else None
        )
        return QdrantNode(endpoint.url, api_key, ca_cert)

    async def migrate_collection(
        self,
        spec: MigrationSpec,
        source: QdrantNode,
        target: QdrantNode,
        collection: CollectionMigration,
        report: Callable[[CollectionMigration], Awaitable[None]],
    ) -> CollectionMigration:
        try:
            shard_keys = await self.ensure_target_collection(spec, source, target, collection)
            finish = self.copy_and_sync if spec.sync else self.copy_and_verify
            return await finish(spec, source, target, collection, shard_keys, report)
        except (ConnectionError, TimeoutError):
            raise
        except Exception as error:
            return replace(collection, status=MigrationPhase.FAILED, error=str(error))

    async def copy_and_verify(
        self,
        spec: MigrationSpec,
        source: QdrantNode,
        target: QdrantNode,
        collection: CollectionMigration,
        shard_keys: set[Any] | None,
        report: Callable[[CollectionMigration], Awaitable[None]],
    ) -> CollectionMigration:
        """Scroll, compare exact counts, and rescroll from the start while the target is short."""
        current = collection
        while True:
            copied = await self.copy_points(spec, source, target, current, shard_keys, report)
            source_count = await self.qdrant.count_points(source, current.name, exact=True)
            target_count = await self.qdrant.count_points(target, current.target_name, exact=True)
            counted = replace(
                current,
                points_total=source_count,
                points_copied=copied,
                source_count=source_count,
                target_count=target_count,
            )
            if target_count >= source_count:
                return replace(
                    counted,
                    status=MigrationPhase.COMPLETED,
                    points_copied=source_count,
                    offset=None,
                    error=None,
                    message=(
                        f"target has {target_count} points, more than the {source_count} "
                        "on the source"
                    )
                    if target_count > source_count
                    else None,
                )
            if current.verify_pass >= spec.verify_passes:
                passes = (
                    f"{current.verify_pass} verify pass{'' if current.verify_pass == 1 else 'es'}"
                )
                return replace(
                    counted,
                    status=MigrationPhase.FAILED,
                    offset=None,
                    error=(
                        f"target has {target_count} of {source_count} source points after {passes}"
                    ),
                )
            current = replace(
                counted,
                status=MigrationPhase.RUNNING,
                verify_pass=current.verify_pass + 1,
                points_copied=0,
                offset=None,
            )
            await report(current)

    async def copy_and_sync(
        self,
        spec: MigrationSpec,
        source: QdrantNode,
        target: QdrantNode,
        collection: CollectionMigration,
        shard_keys: set[Any] | None,
        report: Callable[[CollectionMigration], Awaitable[None]],
    ) -> CollectionMigration:
        """Copy once, sync until a pass changes nothing or the cap, then compare exact counts."""
        current = collection
        if not current.sync_pass:
            copied = await self.copy_points(spec, source, target, current, shard_keys, report)
            current = replace(
                current,
                points_copied=copied,
                offset=None,
                sync_pass=1,
                sync_offset=None,
                sync_pass_changes=0,
            )
            await report(current)
        while True:
            current = await self.sync_pass(spec, source, target, current, shard_keys, report)
            if not current.sync_pass_changes or current.sync_pass >= spec.sync_max_passes:
                break
            current = replace(
                current, sync_pass=current.sync_pass + 1, sync_offset=None, sync_pass_changes=0
            )
            await report(current)
        source_count = await self.qdrant.count_points(source, current.name, exact=True)
        target_count = await self.qdrant.count_points(target, current.target_name, exact=True)
        counted = replace(
            current,
            points_total=source_count,
            points_copied=source_count,
            source_count=source_count,
            target_count=target_count,
            offset=None,
            sync_offset=None,
        )
        if target_count < source_count:
            passes = f"{current.sync_pass} sync pass{'' if current.sync_pass == 1 else 'es'}"
            return replace(
                counted,
                status=MigrationPhase.FAILED,
                error=f"target has {target_count} of {source_count} source points after {passes}",
            )
        notes = [
            note
            for note in (
                f"target has {target_count} points, more than the {source_count} on the source"
                if target_count > source_count
                else "",
                f"sync pass {current.sync_pass} still changed {current.sync_pass_changes} points"
                if current.sync_pass_changes and spec.sync_max_passes > 1
                else "",
            )
            if note
        ]
        return replace(
            counted,
            status=MigrationPhase.COMPLETED,
            error=None,
            message="; ".join(notes) or None,
        )

    async def sync_pass(
        self,
        spec: MigrationSpec,
        source: QdrantNode,
        target: QdrantNode,
        collection: CollectionMigration,
        shard_keys: set[Any] | None,
        report: Callable[[CollectionMigration], Awaitable[None]],
    ) -> CollectionMigration:
        current = collection
        offset: Any = collection.sync_offset
        windows = 0
        while True:
            points, following = await self.qdrant.scroll_points(
                source, current.name, offset, spec.batch_size
            )
            existing = await self.target_range(
                target, current.target_name, offset, following, spec.batch_size
            )
            changed, stale = plan_sync_window(points, existing)
            routed = self.route_page(spec, changed, shard_keys) if changed else {}
            for shard_key, group in routed.items():
                await self.ensure_shard_key(spec, target, current, shard_keys, shard_key)
                await self.qdrant.upsert_points(target, current.target_name, group, shard_key)
            for start in range(0, len(stale), spec.batch_size):
                await self.qdrant.delete_points(
                    target, current.target_name, stale[start : start + spec.batch_size]
                )
            current = replace(
                current,
                points_updated=current.points_updated + len(changed),
                points_deleted=current.points_deleted + len(stale),
                sync_pass_changes=current.sync_pass_changes + len(changed) + len(stale),
                sync_offset=following,
            )
            windows += 1
            if following is None or not points:
                return replace(current, sync_offset=None)
            offset = following
            if windows % spec.checkpoint_batches == 0:
                await report(current)
            if spec.batch_delay_ms:
                await asyncio.sleep(spec.batch_delay_ms / 1000)

    async def target_range(
        self, target: QdrantNode, name: str, start: Any, stop: Any, limit: int
    ) -> list[JsonDict]:
        """Target points with start <= id < stop in ID order; stop None reads to the end."""
        found: list[JsonDict] = []
        cursor: Any = start
        while True:
            points, cursor = await self.qdrant.scroll_points(target, name, cursor, limit)
            for point in points:
                if stop is not None and point_id_order(point["id"]) >= point_id_order(stop):
                    return found
                found.append(point)
            if cursor is None or not points:
                return found

    async def ensure_target_collection(
        self,
        spec: MigrationSpec,
        source: QdrantNode,
        target: QdrantNode,
        collection: CollectionMigration,
    ) -> set[Any] | None:
        """Create or check the target; returns its shard keys, or None for auto sharding."""
        info = await self.qdrant.collection_info(source, collection.name)
        config: Mapping[str, Any] = info["config"]
        schema: Mapping[str, Any] = info.get("payload_schema", {})
        exists = await self.qdrant.collection_exists(target, collection.target_name)
        if not exists and not spec.create_missing:
            raise LookupError(
                f"Collection {collection.target_name} missing on target and createMissing is false"
            )
        if not exists:
            await self.qdrant.create_collection(
                target, collection.target_name, create_body_from_config(config, spec.target)
            )
        target_config: Mapping[str, Any] = (
            await self.qdrant.collection_info(target, collection.target_name)
        )["config"]
        if not is_subset(vector_layout(config), vector_layout(target_config)):
            raise ValueError(
                f"Collection {collection.target_name} on the target has different vector "
                "sizes or distances than the source"
            )
        if not exists or spec.ensure_payload_indexes:
            target_schema: Mapping[str, Any] = (
                await self.qdrant.collection_info(target, collection.target_name)
            ).get("payload_schema", {})
            for field_name, field_schema in payload_indexes_from_schema(schema):
                if field_name in target_schema:
                    continue
                await self.qdrant.create_payload_index(
                    target, collection.target_name, field_name, field_schema
                )
        if target_config.get("params", {}).get("sharding_method") != "custom":
            return None
        return set(await self.qdrant.list_shard_keys(target, collection.target_name))

    async def ensure_shard_key(
        self,
        spec: MigrationSpec,
        target: QdrantNode,
        collection: CollectionMigration,
        shard_keys: set[Any] | None,
        shard_key: Any,
    ) -> None:
        """Create a shard key on a custom-sharded target the first time a point needs it."""
        if shard_keys is None or shard_key is None or shard_key in shard_keys:
            return
        await self.qdrant.create_shard_key(
            target,
            collection.target_name,
            {"shard_key": shard_key, **spec.target.shard_key_params()},
        )
        shard_keys.add(shard_key)

    @staticmethod
    def route_page(
        spec: MigrationSpec, points: list[JsonDict], shard_keys: set[Any] | None
    ) -> dict[Any, list[JsonDict]]:
        """Per-key groups for a custom-sharded target; one keyless upsert otherwise."""
        if shard_keys is None:
            return {None: [to_point_struct(p) for p in points]}
        return group_by_shard_key(points, spec.target)

    async def copy_points(
        self,
        spec: MigrationSpec,
        source: QdrantNode,
        target: QdrantNode,
        collection: CollectionMigration,
        shard_keys: set[Any] | None,
        report: Callable[[CollectionMigration], Awaitable[None]],
    ) -> int:
        offset: Any = collection.offset
        copied = collection.points_copied if collection.offset is not None else 0
        batches = 0
        while True:
            points, offset = await self.qdrant.scroll_points(
                source, collection.name, offset, spec.batch_size
            )
            for shard_key, group in self.route_page(spec, points, shard_keys).items():
                await self.ensure_shard_key(spec, target, collection, shard_keys, shard_key)
                await self.qdrant.upsert_points(target, collection.target_name, group, shard_key)
            copied += len(points)
            batches += 1
            if offset is None or not points:
                return copied
            if batches % spec.checkpoint_batches == 0:
                await report(replace(collection, points_copied=copied, offset=offset))
            if spec.batch_delay_ms:
                await asyncio.sleep(spec.batch_delay_ms / 1000)
