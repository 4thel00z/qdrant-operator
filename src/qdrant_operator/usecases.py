"""Use cases implementing application logic.

Use cases orchestrate ports to implement business operations.
They contain no I/O code directly - all external calls go through ports.
"""

from dataclasses import dataclass
from datetime import datetime
from time import timezone

from qdrant_operator.domain import (
    BackupPhase,
    BackupScheduleSpec,
    BackupScheduleStatus,
    BackupSpec,
    BackupStatus,
    ClusterPhase,
    ClusterSpec,
    ClusterStatus,
    CollectionBackupStatus,
    Condition,
    RestorePhase,
    RestoreProgress,
    RestoreSpec,
    RestoreStatus,
    RestoredCollection,
    S3StorageSpec,
    SchedulePhase,
    build_helm_values,
)
from qdrant_operator.ports import HelmPort, KubernetesPort, QdrantPort, StoragePort
from croniter import croniter

QDRANT_HELM_CHART = "qdrant/qdrant"
QDRANT_HELM_REPO = "https://qdrant.github.io/qdrant-helm"


@dataclass
class ReconcileCluster:
    """Use case for reconciling a QdrantCluster."""

    helm: HelmPort
    kubernetes: KubernetesPort

    async def execute(self, spec: ClusterSpec) -> ClusterStatus:
        """Reconcile cluster to desired state."""
        release_name = f"qdrant-{spec.name}"
        values = build_helm_values(spec)

        existing = await self.helm.get_release_status(release_name, spec.namespace)

        if not existing:
            await self.helm.install(
                release_name=release_name,
                namespace=spec.namespace,
                chart=QDRANT_HELM_CHART,
                values=values,
                version=spec.version,
            )
            phase = ClusterPhase.PENDING
        else:
            await self.helm.upgrade(
                release_name=release_name,
                namespace=spec.namespace,
                chart=QDRANT_HELM_CHART,
                values=values,
                version=spec.version,
            )
            phase = ClusterPhase.UPGRADING

        endpoint = await self.kubernetes.get_service_endpoint(
            name=release_name,
            namespace=spec.namespace,
        )

        return ClusterStatus(
            phase=phase,
            replicas=spec.replicas,
            ready_replicas=0,
            helm_release=release_name,
            endpoint=endpoint,
            version=spec.version,
            conditions=[
                Condition(
                    type="Reconciling",
                    status="True",
                    last_transition_time=datetime.now(),
                    reason="HelmReleaseUpdated",
                    message=f"Helm release {release_name} updated",
                )
            ],
        )


@dataclass
class DeleteCluster:
    """Use case for deleting a QdrantCluster."""

    helm: HelmPort

    async def execute(self, spec: ClusterSpec) -> None:
        """Delete cluster resources."""
        release_name = f"qdrant-{spec.name}"
        await self.helm.uninstall(release_name, spec.namespace)


@dataclass
class ExecuteBackup:
    """Use case for executing a backup."""

    qdrant: QdrantPort
    storage: StoragePort
    kubernetes: KubernetesPort

    async def execute(self, spec: BackupSpec, cluster_endpoint: str) -> BackupStatus:
        """Execute backup of collections to S3."""
        start_time = datetime.now()

        api_key = None
        if spec.cluster_ref:
            pass

        credentials = await self.get_storage_credentials(spec.storage)

        collections = list(spec.collections)
        if not collections:
            collections = await self.qdrant.list_collections(cluster_endpoint, api_key)

        collection_statuses: list[CollectionBackupStatus] = []
        total_size = 0

        for collection in collections:
            try:
                snapshot = await self.qdrant.create_snapshot(
                    cluster_endpoint, collection, api_key
                )

                local_path = f"/tmp/{snapshot.name}"
                await self.qdrant.download_snapshot(
                    cluster_endpoint, collection, snapshot.name, local_path, api_key
                )

                remote_key = f"{spec.storage.prefix}/{spec.name}/{collection}/{snapshot.name}"
                await self.storage.upload_file(
                    spec.storage, credentials, local_path, remote_key
                )

                total_size += snapshot.size_bytes
                collection_statuses.append(
                    CollectionBackupStatus(
                        name=collection,
                        snapshot_name=snapshot.name,
                        size=format_size(snapshot.size_bytes),
                        status="Completed",
                    )
                )

                await self.qdrant.delete_snapshot(
                    cluster_endpoint, collection, snapshot.name, api_key
                )

            except Exception as e:
                collection_statuses.append(
                    CollectionBackupStatus(
                        name=collection,
                        status="Failed",
                        error=str(e),
                    )
                )

        failed = [c for c in collection_statuses if c.status == "Failed"]
        phase = BackupPhase.FAILED if failed else BackupPhase.COMPLETED

        return BackupStatus(
            phase=phase,
            start_time=start_time,
            completion_time=datetime.now(),
            s3_path=f"s3://{spec.storage.bucket}/{spec.storage.prefix}/{spec.name}",
            total_size=format_size(total_size),
            collections=collection_statuses,
            error=failed[0].error if failed else None,
            conditions=[
                Condition(
                    type="Complete",
                    status="True" if phase == BackupPhase.COMPLETED else "False",
                    last_transition_time=datetime.now(),
                    reason="BackupCompleted" if phase == BackupPhase.COMPLETED else "BackupFailed",
                    message=f"Backed up {len(collections) - len(failed)}/{len(collections)} collections",
                )
            ],
        )

    async def get_storage_credentials(self, storage: S3StorageSpec) -> tuple[str, str]:
        """Get S3 credentials from secret."""
        access_key = await self.kubernetes.get_secret_value(storage.credentials_secret_ref)
        secret_key_ref = storage.credentials_secret_ref
        secret_key_ref_for_secret = type(secret_key_ref)(
            name=secret_key_ref.name,
            key="AWS_SECRET_ACCESS_KEY",
            namespace=secret_key_ref.namespace,
        )
        secret_key = await self.kubernetes.get_secret_value(secret_key_ref_for_secret)
        return access_key, secret_key


@dataclass
class ExecuteRestore:
    """Use case for restoring from backup."""

    qdrant: QdrantPort
    storage: StoragePort
    kubernetes: KubernetesPort

    async def execute(self, spec: RestoreSpec, cluster_endpoint: str) -> RestoreStatus:
        """Execute restore of collections from S3."""
        start_time = datetime.now()

        source_path = await self.resolve_source(spec)
        storage = spec.source_s3
        if not storage:
            raise ValueError("No storage configuration found")

        credentials = await self.get_storage_credentials(storage)
        files = await self.storage.list_files(storage, credentials, source_path)

        collections = list(spec.collections) if spec.collections else extract_collections(files)

        restored: list[RestoredCollection] = []
        progress = RestoreProgress(collections_total=len(collections))

        for i, collection in enumerate(collections):
            target_name = spec.collection_mapping.get(collection, collection)
            progress = RestoreProgress(
                collections_total=len(collections),
                collections_completed=i,
                current_collection=target_name,
                percentage=int((i / len(collections)) * 100),
            )

            try:
                snapshot_key = find_snapshot_key(files, collection)
                local_path = f"/tmp/{collection}_restore.snapshot"

                await self.storage.download_file(storage, credentials, snapshot_key, local_path)

                await self.qdrant.recover_from_snapshot(
                    cluster_endpoint, target_name, local_path
                )

                info = await self.qdrant.get_collection_info(cluster_endpoint, target_name)

                restored.append(
                    RestoredCollection(
                        name=target_name,
                        original_name=collection if collection != target_name else None,
                        status="Completed",
                        points_count=info.get("points_count"),
                    )
                )

            except Exception as e:
                restored.append(
                    RestoredCollection(
                        name=target_name,
                        original_name=collection if collection != target_name else None,
                        status="Failed",
                        error=str(e),
                    )
                )

        failed = [r for r in restored if r.status == "Failed"]
        phase = RestorePhase.FAILED if failed else RestorePhase.COMPLETED

        return RestoreStatus(
            phase=phase,
            start_time=start_time,
            completion_time=datetime.now(),
            source_backup=source_path,
            restored_collections=restored,
            progress=RestoreProgress(
                collections_total=len(collections),
                collections_completed=len(collections) - len(failed),
                percentage=100,
            ),
            error=failed[0].error if failed else None,
        )

    async def resolve_source(self, spec: RestoreSpec) -> str:
        """Resolve the backup source path."""
        if spec.backup_ref:
            backup = await self.kubernetes.get_resource(
                group="qdrant.io",
                version="v1alpha1",
                plural="qdrantbackups",
                name=spec.backup_ref.name,
                namespace=spec.backup_ref.namespace,
            )
            if backup and backup.get("status", {}).get("s3Path"):
                return backup["status"]["s3Path"]
        if spec.source_s3:
            return f"{spec.source_s3.prefix}"
        raise ValueError("No valid backup source found")

    async def get_storage_credentials(self, storage: S3StorageSpec) -> tuple[str, str]:
        """Get S3 credentials from secret."""
        access_key = await self.kubernetes.get_secret_value(storage.credentials_secret_ref)
        secret_key_ref = storage.credentials_secret_ref
        secret_key_ref_for_secret = type(secret_key_ref)(
            name=secret_key_ref.name,
            key="AWS_SECRET_ACCESS_KEY",
            namespace=secret_key_ref.namespace,
        )
        secret_key = await self.kubernetes.get_secret_value(secret_key_ref_for_secret)
        return (access_key, secret_key)


@dataclass
class ProcessSchedule:
    """Use case for processing backup schedules."""

    kubernetes: KubernetesPort

    async def execute(
        self, spec: BackupScheduleSpec, status: BackupScheduleStatus
    ) -> BackupScheduleStatus:
        """Check schedule and create backup if due."""
        if spec.suspend:
            return BackupScheduleStatus(
                phase=SchedulePhase.SUSPENDED,
                last_backup_time=status.last_backup_time,
                last_backup_name=status.last_backup_name,
                next_backup_time=None,
            )

        now = datetime.now()
        next_run = compute_next_run(spec.schedule, status.last_backup_time)

        if next_run and now >= next_run:
            backup_name = f"{spec.name}-{now.strftime('%Y%m%d-%H%M%S')}"

            backup_body = build_backup_resource(spec, backup_name)
            await self.kubernetes.create_resource(
                group="qdrant.io",
                version="v1alpha1",
                plural="qdrantbackups",
                namespace=spec.namespace,
                body=backup_body,
            )

            return BackupScheduleStatus(
                phase=SchedulePhase.ACTIVE,
                last_backup_time=now,
                last_backup_name=backup_name,
                next_backup_time=compute_next_run(spec.schedule, now),
                active_backup=backup_name,
            )

        return BackupScheduleStatus(
            phase=SchedulePhase.ACTIVE,
            last_backup_time=status.last_backup_time,
            last_backup_name=status.last_backup_name,
            next_backup_time=next_run,
        )


def format_size(size_bytes: int) -> str:
    """Format bytes as human-readable size."""
    for unit in ["B", "KB", "MB", "GB", "TB"]:
        if size_bytes < 1024:
            return f"{size_bytes:.1f}{unit}"
        size_bytes = size_bytes // 1024
    return f"{size_bytes:.1f}PB"


def extract_collections(files: list[str]) -> list[str]:
    """Extract collection names from file paths."""
    collections = set()
    for f in files:
        parts = f.split("/")
        if len(parts) >= 2:
            collections.add(parts[-2])
    return sorted(collections)


def find_snapshot_key(files: list[str], collection: str) -> str:
    """Find the snapshot file key for a collection."""
    for f in files:
        if f"/{collection}/" in f and f.endswith(".snapshot"):
            return f
    raise ValueError(f"No snapshot found for collection {collection}")


def compute_next_run(schedule: str, last_run: datetime | None) -> datetime | None:
    """Compute next scheduled run time."""

    base = last_run or datetime.now()
    cron = croniter(schedule, base)
    return cron.get_next(datetime)


def build_backup_resource(spec: BackupScheduleSpec, backup_name: str) -> dict:
    """Build a QdrantBackup resource body."""
    return {
        "apiVersion": "qdrant.io/v1alpha1",
        "kind": "QdrantBackup",
        "metadata": {
            "name": backup_name,
            "namespace": spec.namespace,
            "labels": {
                "qdrant.io/schedule": spec.name,
            },
        },
        "spec": {
            "clusterRef": {
                "name": spec.cluster_ref.name,
                "namespace": spec.cluster_ref.namespace,
            },
            "storage": {
                "s3": {
                    "bucket": spec.storage.bucket,
                    "prefix": f"{spec.storage.prefix}/{backup_name}",
                    "region": spec.storage.region,
                    "endpoint": spec.storage.endpoint,
                    "forcePathStyle": spec.storage.force_path_style,
                    "credentialsSecretRef": {
                        "name": spec.storage.credentials_secret_ref.name,
                        "accessKeyIdKey": spec.storage.credentials_secret_ref.key,
                    },
                },
            },
            "collections": list(spec.collections) if spec.collections else None,
        },
    }