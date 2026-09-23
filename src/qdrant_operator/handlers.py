"""Kopf handlers: translate Kubernetes events into use case calls and status patches."""

# pyright: reportArgumentType=false

import logging
from collections.abc import Mapping
from datetime import UTC
from datetime import datetime
from typing import Any

import kopf

from qdrant_operator.container import Container
from qdrant_operator.domain import BACKUPS
from qdrant_operator.domain import CLUSTERS
from qdrant_operator.domain import GROUP
from qdrant_operator.domain import RESTORES
from qdrant_operator.domain import SCHEDULES
from qdrant_operator.domain import VERSION
from qdrant_operator.domain import BackupPhase
from qdrant_operator.domain import BackupScheduleSpec
from qdrant_operator.domain import BackupScheduleStatus
from qdrant_operator.domain import BackupSpec
from qdrant_operator.domain import BackupStatus
from qdrant_operator.domain import ClusterSpec
from qdrant_operator.domain import ClusterStatus
from qdrant_operator.domain import ResourceKind
from qdrant_operator.domain import ResourceRef
from qdrant_operator.domain import RestorePhase
from qdrant_operator.domain import RestoreSpec
from qdrant_operator.domain import RestoreStatus
from qdrant_operator.kubernetes_adapter import load_kubernetes_config
from qdrant_operator.usecases import ClusterNotFoundError
from qdrant_operator.usecases import SourceNotReadyError

FINALIZER = f"{GROUP}/finalizer"
CLUSTER_OBSERVE_INTERVAL = 30.0
SCHEDULE_TICK_INTERVAL = 60.0
BACKUP_EXPIRY_INTERVAL = 300.0
RETRY_DELAY_SECONDS = 30.0


def resource_ref(kind: ResourceKind, meta: Mapping[str, Any]) -> ResourceRef:
    return ResourceRef(kind, meta["name"], meta["namespace"])


@kopf.on.startup()
async def configure(settings: kopf.OperatorSettings, **_: Any) -> None:
    settings.persistence.finalizer = FINALIZER
    settings.persistence.progress_storage = kopf.AnnotationsProgressStorage(prefix=GROUP)
    settings.persistence.diffbase_storage = kopf.AnnotationsDiffBaseStorage(
        prefix=GROUP, key="last-applied-spec"
    )
    settings.posting.level = logging.INFO
    await load_kubernetes_config()


@kopf.on.probe(id="ready")
def ready(**_: Any) -> str:
    return "ok"


@kopf.on.create(GROUP, VERSION, CLUSTERS.plural)
@kopf.on.update(GROUP, VERSION, CLUSTERS.plural, field="spec")
@kopf.on.resume(GROUP, VERSION, CLUSTERS.plural)
async def reconcile_cluster(
    spec: kopf.Spec,
    meta: kopf.Meta,
    status: kopf.Status,
    patch: kopf.Patch,
    logger: kopf.Logger,
    **_: Any,
) -> None:
    cluster = ClusterSpec.from_dict(spec, meta)
    current = ClusterStatus.from_dict(status)
    result = await Container().reconcile_cluster().execute(cluster, meta.get("generation"), current)
    patch.status.update(result.to_dict())
    logger.info(f"Helm release {cluster.release_name} applied; phase {result.phase.value}")


@kopf.timer(GROUP, VERSION, CLUSTERS.plural, interval=CLUSTER_OBSERVE_INTERVAL, initial_delay=10.0)
async def observe_cluster(
    spec: kopf.Spec, meta: kopf.Meta, status: kopf.Status, patch: kopf.Patch, **_: Any
) -> None:
    cluster = ClusterSpec.from_dict(spec, meta)
    current = ClusterStatus.from_dict(status)
    result = await Container().observe_cluster().execute(cluster, current)
    if result == current:
        return
    patch.status.update(result.to_dict())


@kopf.on.delete(GROUP, VERSION, CLUSTERS.plural)
async def delete_cluster(spec: kopf.Spec, meta: kopf.Meta, logger: kopf.Logger, **_: Any) -> None:
    cluster = ClusterSpec.from_dict(spec, meta)
    await Container().delete_cluster().execute(cluster)
    logger.info(f"Helm release {cluster.release_name} removed")


@kopf.on.create(GROUP, VERSION, BACKUPS.plural)
async def execute_backup(
    spec: kopf.Spec, meta: kopf.Meta, patch: kopf.Patch, logger: kopf.Logger, **_: Any
) -> None:
    backup = BackupSpec.from_dict(spec, meta)
    ref = resource_ref(BACKUPS, meta)
    try:
        result = await Container().execute_backup().execute(backup, ref)
    except (ClusterNotFoundError, KeyError) as error:
        raise kopf.TemporaryError(str(error), delay=RETRY_DELAY_SECONDS) from error
    except Exception as error:
        patch.status.update(BackupStatus(phase=BackupPhase.FAILED, error=str(error)).to_dict())
        raise kopf.PermanentError(f"Backup failed: {error}") from error
    patch.status.update(result.to_dict())
    logger.info(f"Backup {result.phase.value}: {result.s3_path} ({result.total_size})")


@kopf.on.delete(GROUP, VERSION, BACKUPS.plural)
async def delete_backup(spec: kopf.Spec, meta: kopf.Meta, logger: kopf.Logger, **_: Any) -> None:
    backup = BackupSpec.from_dict(spec, meta)
    try:
        deleted = await Container().delete_backup_data().execute(backup)
    except KeyError as error:
        logger.warning(f"Leaving backup data in place, credentials unavailable: {error}")
        return
    logger.info(f"Deleted {deleted} objects under {backup.storage.uri(backup.name)}")


def has_expiry(status: kopf.Status, **_: Any) -> bool:
    return bool(status.get("expiresAt"))


@kopf.timer(
    GROUP,
    VERSION,
    BACKUPS.plural,
    interval=BACKUP_EXPIRY_INTERVAL,
    when=has_expiry,
)
async def expire_backup(
    meta: kopf.Meta, status: kopf.Status, logger: kopf.Logger, **_: Any
) -> None:
    ref = resource_ref(BACKUPS, meta)
    expired = (
        await Container()
        .expire_backup()
        .execute(ref, BackupStatus.from_dict(status), datetime.now(UTC))
    )
    if expired:
        logger.info(f"Backup {meta['name']} passed retentionDays and was deleted")


@kopf.on.create(GROUP, VERSION, RESTORES.plural)
async def execute_restore(
    spec: kopf.Spec, meta: kopf.Meta, patch: kopf.Patch, logger: kopf.Logger, **_: Any
) -> None:
    ref = resource_ref(RESTORES, meta)
    try:
        restore = RestoreSpec.from_dict(spec, meta)
        result = await Container().execute_restore().execute(restore, ref)
    except (ClusterNotFoundError, SourceNotReadyError, KeyError) as error:
        raise kopf.TemporaryError(str(error), delay=RETRY_DELAY_SECONDS) from error
    except Exception as error:
        patch.status.update(RestoreStatus(phase=RestorePhase.FAILED, error=str(error)).to_dict())
        raise kopf.PermanentError(f"Restore failed: {error}") from error
    patch.status.update(result.to_dict())
    logger.info(f"Restore {result.phase.value} from {result.source_backup}")


@kopf.on.create(GROUP, VERSION, SCHEDULES.plural)
@kopf.on.update(GROUP, VERSION, SCHEDULES.plural, field="spec")
@kopf.on.resume(GROUP, VERSION, SCHEDULES.plural)
@kopf.timer(GROUP, VERSION, SCHEDULES.plural, interval=SCHEDULE_TICK_INTERVAL)
async def process_schedule(
    spec: kopf.Spec,
    meta: kopf.Meta,
    status: kopf.Status,
    body: kopf.Body,
    patch: kopf.Patch,
    logger: kopf.Logger,
    **_: Any,
) -> None:
    schedule = BackupScheduleSpec.from_dict(spec, meta)
    current = BackupScheduleStatus.from_dict(status)
    result = (
        await Container().process_schedule().execute(schedule, current, body, datetime.now(UTC))
    )
    if result.active_backup and result.active_backup != current.active_backup:
        logger.info(f"Scheduled backup {result.active_backup} created")
    patch.status.update(result.to_dict())
