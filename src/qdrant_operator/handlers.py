"""Kopf handlers for Qdrant CRDs.

Handlers are the driving adapters that receive events from Kubernetes
and invoke use cases to perform business operations.
"""

import kopf
import structlog

from qdrant_operator.container import Container
from qdrant_operator.domain import (
    BackupPhase,
    BackupScheduleSpec,
    BackupScheduleStatus,
    BackupSpec,
    ClusterSpec,
    RestorePhase,
    RestoreSpec,
    SchedulePhase,
)
from qdrant_operator.domain import SecretRef

log = structlog.get_logger()


@kopf.on.create("qdrant.io", "v1alpha1", "qdrantclusters")
@kopf.on.update("qdrant.io", "v1alpha1", "qdrantclusters")
async def reconcile_cluster(
    spec: dict,
    meta: dict,
    status: dict,
    patch: kopf.Patch,
    **kwargs,
) -> dict:
    """Reconcile a QdrantCluster resource."""
    await log.ainfo("reconciling_cluster", name=meta["name"], namespace=meta["namespace"])

    cluster_spec = ClusterSpec.from_dict(spec, meta)
    container = Container()

    use_case = container.reconcile_cluster()
    result = await use_case.execute(cluster_spec)

    return result.to_dict()


@kopf.on.delete("qdrant.io", "v1alpha1", "qdrantclusters")
async def delete_cluster(
    spec: dict,
    meta: dict,
    **kwargs,
) -> None:
    """Delete a QdrantCluster resource."""
    await log.ainfo("deleting_cluster", name=meta["name"], namespace=meta["namespace"])

    cluster_spec = ClusterSpec.from_dict(spec, meta)
    container = Container()

    use_case = container.delete_cluster()
    await use_case.execute(cluster_spec)


@kopf.on.create("qdrant.io", "v1alpha1", "qdrantbackups")
async def execute_backup(
    spec: dict,
    meta: dict,
    status: dict,
    patch: kopf.Patch,
    **kwargs,
) -> dict:
    """Execute a QdrantBackup."""
    await log.ainfo("executing_backup", name=meta["name"], namespace=meta["namespace"])

    backup_spec = BackupSpec.from_dict(spec, meta)
    container = Container()

    patch.status["phase"] = BackupPhase.IN_PROGRESS.value

    kubernetes = container.kubernetes_adapter()
    endpoint, api_key = await resolve_cluster_connection(
        kubernetes,
        backup_spec.cluster_ref.name,
        backup_spec.cluster_ref.namespace,
    )

    use_case = container.execute_backup(endpoint=endpoint, api_key=api_key)
    result = await use_case.execute(backup_spec)

    return result.to_dict()


@kopf.on.create("qdrant.io", "v1alpha1", "qdrantrestores")
async def execute_restore(
    spec: dict,
    meta: dict,
    status: dict,
    patch: kopf.Patch,
    **kwargs,
) -> dict:
    """Execute a QdrantRestore."""
    await log.ainfo("executing_restore", name=meta["name"], namespace=meta["namespace"])

    restore_spec = RestoreSpec.from_dict(spec, meta)
    container = Container()

    patch.status["phase"] = RestorePhase.DOWNLOADING.value

    kubernetes = container.kubernetes_adapter()
    endpoint, api_key = await resolve_cluster_connection(
        kubernetes,
        restore_spec.target_cluster_ref.name,
        restore_spec.target_cluster_ref.namespace,
    )

    use_case = container.execute_restore(endpoint=endpoint, api_key=api_key)
    result = await use_case.execute(restore_spec)

    return result.to_dict()


@kopf.on.create("qdrant.io", "v1alpha1", "qdrantbackupschedules")
@kopf.on.resume("qdrant.io", "v1alpha1", "qdrantbackupschedules")
async def create_schedule(
    spec: dict,
    meta: dict,
    status: dict,
    patch: kopf.Patch,
    **kwargs,
) -> dict:
    """Initialize a QdrantBackupSchedule."""
    await log.ainfo("creating_schedule", name=meta["name"], namespace=meta["namespace"])

    schedule_spec = BackupScheduleSpec.from_dict(spec, meta)
    container = Container()

    current_status = BackupScheduleStatus(phase=SchedulePhase.ACTIVE)

    use_case = container.process_schedule()
    result = await use_case.execute(schedule_spec, current_status)

    return result.to_dict()


@kopf.timer("qdrant.io", "v1alpha1", "qdrantbackupschedules", interval=60.0)
async def check_schedule(
    spec: dict,
    meta: dict,
    status: dict,
    patch: kopf.Patch,
    **kwargs,
) -> dict | None:
    """Check if a scheduled backup should run."""
    schedule_spec = BackupScheduleSpec.from_dict(spec, meta)

    if schedule_spec.suspend:
        return None

    container = Container()
    current_status = BackupScheduleStatus.from_dict(status)

    use_case = container.process_schedule()
    result = await use_case.execute(schedule_spec, current_status)

    if result.last_backup_name != current_status.last_backup_name:
        await log.ainfo(
            "scheduled_backup_created",
            schedule=meta["name"],
            backup=result.last_backup_name,
        )
        return result.to_dict()

    return None


async def resolve_cluster_connection(
    kubernetes,
    cluster_name: str,
    cluster_namespace: str,
) -> tuple[str, str | None]:
    """Resolve cluster endpoint and API key from QdrantCluster resource."""
    cluster = await kubernetes.get_resource(
        group="qdrant.io",
        version="v1alpha1",
        plural="qdrantclusters",
        name=cluster_name,
        namespace=cluster_namespace,
    )

    if not cluster:
        raise ValueError(f"QdrantCluster {cluster_name} not found in {cluster_namespace}")

    endpoint = await kubernetes.get_service_endpoint(
        name=f"qdrant-{cluster_name}",
        namespace=cluster_namespace,
    )
    api_key_ref = cluster.get("spec", {}).get("apiKey", {}).get("secretRef")

    if not api_key_ref:
        return endpoint, None

    secret_ref = SecretRef(
        name=api_key_ref["name"],
        key=api_key_ref["key"],
        namespace=cluster_namespace,
    )
    api_key = await kubernetes.get_secret_value(secret_ref)

    return endpoint, api_key