"""Application use cases: orchestrate ports, never touch I/O libraries directly."""

import asyncio
import json
from collections.abc import Mapping
from dataclasses import dataclass
from dataclasses import replace
from datetime import UTC
from datetime import datetime
from typing import Any

from qdrant_operator.domain import BACKUPS
from qdrant_operator.domain import MANIFEST_KEY
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
from qdrant_operator.domain import ConcurrencyPolicy
from qdrant_operator.domain import Condition
from qdrant_operator.domain import ConditionStatus
from qdrant_operator.domain import CredentialsSecretRef
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
from qdrant_operator.domain import format_size
from qdrant_operator.domain import join_key
from qdrant_operator.domain import parse_time
from qdrant_operator.domain import set_condition
from qdrant_operator.ports import HelmPort
from qdrant_operator.ports import KubernetesPort
from qdrant_operator.ports import QdrantPort
from qdrant_operator.ports import StoragePort

RECENT_BACKUPS_LIMIT = 10
PRESIGNED_URL_TTL_SECONDS = 3600


class ClusterNotFoundError(LookupError):
    """The referenced QdrantCluster does not exist; callers retry rather than fail."""


class SourceNotReadyError(RuntimeError):
    """The referenced QdrantBackup has not completed yet; callers retry rather than fail."""


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
