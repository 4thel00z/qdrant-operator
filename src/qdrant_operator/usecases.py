"""Application use cases: orchestrate ports, never touch I/O libraries directly."""

import asyncio
import json
from collections.abc import Awaitable
from collections.abc import Callable
from collections.abc import Mapping
from dataclasses import dataclass
from dataclasses import replace
from datetime import UTC
from datetime import datetime
from functools import partial
from typing import Any

from qdrant_operator.domain import BACKUPS
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
from qdrant_operator.domain import JsonDict
from qdrant_operator.domain import MigrationPhase
from qdrant_operator.domain import MigrationSpec
from qdrant_operator.domain import MigrationStatus
from qdrant_operator.domain import QdrantNode
from qdrant_operator.domain import ResourceRef
from qdrant_operator.domain import RestoredCollection
from qdrant_operator.domain import RestorePhase
from qdrant_operator.domain import RestoreProgress
from qdrant_operator.domain import RestoreSpec
from qdrant_operator.domain import RestoreStatus
from qdrant_operator.domain import S3Credentials
from qdrant_operator.domain import S3StorageSpec
from qdrant_operator.domain import SchedulePhase
from qdrant_operator.domain import SnapshotRecord
from qdrant_operator.domain import chart_version
from qdrant_operator.domain import create_body_from_config
from qdrant_operator.domain import format_size
from qdrant_operator.domain import group_by_shard_key
from qdrant_operator.domain import is_subset
from qdrant_operator.domain import join_key
from qdrant_operator.domain import key_fingerprint
from qdrant_operator.domain import owner_reference
from qdrant_operator.domain import parse_time
from qdrant_operator.domain import payload_indexes_from_schema
from qdrant_operator.domain import set_condition
from qdrant_operator.ports import HelmPort
from qdrant_operator.ports import KubernetesPort
from qdrant_operator.ports import QdrantPort
from qdrant_operator.ports import StoragePort
from qdrant_operator.ports import TokenPort

RECENT_BACKUPS_LIMIT = 10
PROGRESS_EVERY_BATCHES = 20
PRESIGNED_URL_TTL_SECONDS = 3600


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
        spec = ClusterSpec.from_dict(body["spec"], body["metadata"])
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


@dataclass
class ReconcileCluster:
    helm: HelmPort

    async def execute(
        self, spec: ClusterSpec, generation: int | None, current: ClusterStatus
    ) -> ClusterStatus:
        exists = await self.helm.release_exists(spec.release_name, spec.namespace)
        await self.helm.apply(
            release_name=spec.release_name,
            namespace=spec.namespace,
            chart_version=chart_version(spec.version),
            values=spec.to_helm_values(),
        )
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
        )


@dataclass
class ObserveCluster:
    """Lift StatefulSet readiness into the QdrantCluster status."""

    kubernetes: KubernetesPort

    async def execute(self, spec: ClusterSpec, current: ClusterStatus) -> ClusterStatus:
        statefulset = await self.kubernetes.get_statefulset_status(
            spec.release_name, spec.namespace
        )
        ready_replicas = statefulset.ready_replicas if statefulset else 0
        ready = ready_replicas >= spec.replicas
        phase = ClusterPhase.RUNNING if ready else self.pending_phase(current)
        conditions = set_condition(
            current.conditions,
            Condition(
                type="Ready",
                status=ConditionStatus.TRUE if ready else ConditionStatus.FALSE,
                last_transition_time=now_utc(),
                reason="AllReplicasReady" if ready else "ReplicasNotReady",
                message=f"{ready_replicas}/{spec.replicas} replicas ready",
            ),
        )
        if ready:
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
            endpoint=spec.service_url(),
            conditions=tuple(conditions),
        )

    @staticmethod
    def pending_phase(current: ClusterStatus) -> ClusterPhase:
        if current.phase in (ClusterPhase.UPGRADING, ClusterPhase.FAILED):
            return current.phase
        return ClusterPhase.PENDING


@dataclass
class DeleteCluster:
    helm: HelmPort

    async def execute(self, spec: ClusterSpec) -> None:
        if not await self.helm.release_exists(spec.release_name, spec.namespace):
            return
        await self.helm.uninstall(spec.release_name, spec.namespace)


@dataclass
class ExecuteBackup:
    qdrant: QdrantPort
    storage: StoragePort
    kubernetes: KubernetesPort

    async def execute(self, spec: BackupSpec, ref: ResourceRef) -> BackupStatus:
        start_time = now_utc()
        connection = await ResolveCluster(self.kubernetes).execute(spec.cluster_ref)
        credentials = await read_credentials(self.kubernetes, spec.storage.credentials_secret_ref)
        await self.kubernetes.patch_status(
            ref, BackupStatus(phase=BackupPhase.IN_PROGRESS, start_time=start_time).to_dict()
        )

        collections = spec.collections or tuple(
            await self.qdrant.list_collections(connection.service)
        )
        statuses = [
            await self.backup_collection(spec, credentials, connection, collection)
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
        credentials: S3Credentials,
        connection: ClusterConnection,
        collection: str,
    ) -> CollectionBackupStatus:
        try:
            records = [
                await self.backup_node(spec, credentials, node, index, collection)
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
        credentials: S3Credentials,
        node: QdrantNode,
        index: int,
        collection: str,
    ) -> SnapshotRecord:
        """Snapshot one node's shards of a collection and stream them straight into the bucket."""
        snapshot = await self.qdrant.create_snapshot(node, collection)
        key = spec.snapshot_key(collection, index, snapshot.name)
        try:
            size = await self.storage.upload_stream(
                spec.storage,
                credentials,
                key,
                self.qdrant.stream_snapshot(node, collection, snapshot.name),
            )
        finally:
            await self.qdrant.delete_snapshot(node, collection, snapshot.name)
        return SnapshotRecord(
            node_index=index,
            key=key,
            snapshot_name=snapshot.name,
            size_bytes=size,
            checksum=snapshot.checksum,
        )


@dataclass
class DeleteBackupData:
    storage: StoragePort
    kubernetes: KubernetesPort

    async def execute(self, spec: BackupSpec) -> int:
        credentials = await read_credentials(self.kubernetes, spec.storage.credentials_secret_ref)
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
    indexing_timeout_seconds: float = 3600.0

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
        deadline = asyncio.get_running_loop().time() + self.indexing_timeout_seconds
        while True:
            info = await self.qdrant.collection_info(node, collection)
            if not spec.wait_for_indexing or info.get("status") == "green":
                return info.get("points_count")
            if asyncio.get_running_loop().time() >= deadline:
                raise TimeoutError(f"Collection {collection} did not turn green in time")
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
    back. Only fields the spec declares are compared; the rest stays Qdrant's default.
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
        if not is_subset(spec.mutable_config(), config):
            await self.qdrant.update_collection(node, name, spec.update_body())

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
                message=f"Collection {name} matches the spec",
            ),
            payload_indexes=indexes,
            aliases=aliases,
        )

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

        await self.kubernetes.apply_secret(
            spec.secret_name,
            spec.namespace,
            {
                TOKEN_SECRET_KEY: self.token.sign(spec.claims(now), api_key),
                URL_SECRET_KEY: cluster.service_url(),
            },
            owner_reference(owner),
        )
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


@dataclass
class ExecuteMigration:
    """Copy collections point by point from any Qdrant into a managed cluster.

    Scroll pages on the source are upserted on the target (grouped by shard key). Missing
    target collections are created from the source configuration and payload schema, with
    shard and replication counts left to the target cluster unless the spec overrides them.
    Upserts are idempotent by point id, so a re-run after an operator restart is safe.
    """

    qdrant: QdrantPort
    kubernetes: KubernetesPort

    async def execute(self, spec: MigrationSpec, ref: ResourceRef) -> MigrationStatus:
        start_time = now_utc()
        source = await self.resolve_source(spec)
        target = (await ResolveCluster(self.kubernetes).execute(spec.target_cluster_ref)).service
        if not await self.qdrant.ready(target):
            raise ClusterNotReadyError(f"QdrantCluster {spec.target_cluster_ref.name} not ready")

        names = spec.select_collections(await self.qdrant.list_collections(source))
        collections = [
            CollectionMigration(
                name=name,
                target_name=spec.target_name(name),
                status=MigrationPhase.PENDING,
                points_total=await self.qdrant.count_points(source, name),
            )
            for name in names
        ]
        running = MigrationStatus(
            phase=MigrationPhase.RUNNING,
            start_time=start_time,
            source=spec.source.description,
            collections=tuple(collections),
        )
        await self.kubernetes.patch_status(ref, running.to_dict())

        for index, collection in enumerate(collections):
            report = partial(self.report_progress, ref, running, collections, index)
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

    async def report_progress(
        self,
        ref: ResourceRef,
        running: MigrationStatus,
        collections: list[CollectionMigration],
        index: int,
        copied: int,
    ) -> None:
        collections[index] = replace(
            collections[index], status=MigrationPhase.RUNNING, points_copied=copied
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
        report: Callable[[int], Awaitable[None]],
    ) -> CollectionMigration:
        try:
            await self.ensure_target_collection(spec, source, target, collection)
            copied = await self.copy_points(spec, source, target, collection, report)
        except Exception as error:
            return replace(collection, status=MigrationPhase.FAILED, error=str(error))
        return replace(collection, status=MigrationPhase.COMPLETED, points_copied=copied)

    async def ensure_target_collection(
        self,
        spec: MigrationSpec,
        source: QdrantNode,
        target: QdrantNode,
        collection: CollectionMigration,
    ) -> None:
        if await self.qdrant.collection_exists(target, collection.target_name):
            return
        if not spec.create_missing:
            raise LookupError(
                f"Collection {collection.target_name} missing on target and createMissing is false"
            )
        info = await self.qdrant.collection_info(source, collection.name)
        await self.qdrant.create_collection(
            target, collection.target_name, create_body_from_config(info["config"], spec.target)
        )
        for field_name, schema in payload_indexes_from_schema(info.get("payload_schema", {})):
            await self.qdrant.create_payload_index(
                target, collection.target_name, field_name, schema
            )

    async def copy_points(
        self,
        spec: MigrationSpec,
        source: QdrantNode,
        target: QdrantNode,
        collection: CollectionMigration,
        report: Callable[[int], Awaitable[None]],
    ) -> int:
        offset: Any = None
        copied = 0
        batches = 0
        while True:
            points, offset = await self.qdrant.scroll_points(
                source, collection.name, offset, spec.batch_size
            )
            for shard_key, group in group_by_shard_key(points).items():
                await self.qdrant.upsert_points(target, collection.target_name, group, shard_key)
            copied += len(points)
            batches += 1
            if batches % PROGRESS_EVERY_BATCHES == 0:
                await report(copied)
            if offset is None or not points:
                return copied
