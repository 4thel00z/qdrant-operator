"""Domain entities and value objects.

This module contains pure business logic with no external dependencies.
All entities are immutable dataclasses representing the core domain concepts.
"""

from dataclasses import dataclass
from dataclasses import field
from datetime import datetime
from enum import Enum


class ClusterPhase(Enum):
    """Phase of a QdrantCluster lifecycle."""

    PENDING = "Pending"
    RUNNING = "Running"
    FAILED = "Failed"
    UPGRADING = "Upgrading"
    TERMINATING = "Terminating"


class BackupPhase(Enum):
    """Phase of a QdrantBackup lifecycle."""

    PENDING = "Pending"
    IN_PROGRESS = "InProgress"
    COMPLETED = "Completed"
    FAILED = "Failed"


class RestorePhase(Enum):
    """Phase of a QdrantRestore lifecycle."""

    PENDING = "Pending"
    DOWNLOADING = "Downloading"
    RESTORING = "Restoring"
    INDEXING = "Indexing"
    COMPLETED = "Completed"
    FAILED = "Failed"


class SchedulePhase(Enum):
    """Phase of a QdrantBackupSchedule."""

    ACTIVE = "Active"
    SUSPENDED = "Suspended"


@dataclass(frozen=True)
class ResourceRequirements:
    """Kubernetes resource requirements."""

    cpu: str | None = None
    memory: str | None = None


@dataclass(frozen=True)
class Resources:
    """Container resource requests and limits."""

    requests: ResourceRequirements = field(default_factory=ResourceRequirements)
    limits: ResourceRequirements = field(default_factory=ResourceRequirements)


@dataclass(frozen=True)
class PersistenceSpec:
    """Storage persistence configuration."""

    size: str = "10Gi"
    storage_class: str | None = None
    access_modes: tuple[str, ...] = ("ReadWriteOnce",)


@dataclass(frozen=True)
class SecretRef:
    """Reference to a Kubernetes Secret key."""

    name: str
    key: str
    namespace: str = "default"


@dataclass(frozen=True)
class S3StorageSpec:
    """S3-compatible storage configuration."""

    bucket: str
    credentials_secret_ref: SecretRef
    prefix: str = ""
    region: str = "us-east-1"
    endpoint: str | None = None
    force_path_style: bool = False


@dataclass(frozen=True)
class ClusterRef:
    """Reference to a QdrantCluster."""

    name: str
    namespace: str = "default"


@dataclass(frozen=True)
class BackupRef:
    """Reference to a QdrantBackup."""

    name: str
    namespace: str = "default"


@dataclass(frozen=True)
class RetentionPolicy:
    """Backup retention policy."""

    keep_last: int | None = None
    keep_daily: int | None = None
    keep_weekly: int | None = None
    keep_monthly: int | None = None


@dataclass(frozen=True)
class ClusterSpec:
    """Specification for a QdrantCluster."""

    name: str
    namespace: str
    version: str
    replicas: int = 1
    resources: Resources = field(default_factory=Resources)
    persistence: PersistenceSpec = field(default_factory=PersistenceSpec)
    cluster_enabled: bool = True
    api_key_secret_ref: SecretRef | None = None
    metrics_enabled: bool = False

    @staticmethod
    def from_dict(spec: dict, meta: dict) -> "ClusterSpec":
        """Create ClusterSpec from Kubernetes resource dicts."""
        resources_dict = spec.get("resources", {})
        requests = resources_dict.get("requests", {})
        limits = resources_dict.get("limits", {})

        persistence_dict = spec.get("persistence", {})
        api_key_dict = spec.get("apiKey", {}).get("secretRef")

        return ClusterSpec(
            name=meta["name"],
            namespace=meta["namespace"],
            version=spec["version"],
            replicas=spec.get("replicas", 1),
            resources=Resources(
                requests=ResourceRequirements(
                    cpu=requests.get("cpu"),
                    memory=requests.get("memory"),
                ),
                limits=ResourceRequirements(
                    cpu=limits.get("cpu"),
                    memory=limits.get("memory"),
                ),
            ),
            persistence=PersistenceSpec(
                size=persistence_dict.get("size", "10Gi"),
                storage_class=persistence_dict.get("storageClassName"),
                access_modes=tuple(persistence_dict.get("accessModes", ["ReadWriteOnce"])),
            ),
            cluster_enabled=spec.get("cluster", {}).get("enabled", True),
            api_key_secret_ref=(
                SecretRef(
                    name=api_key_dict["name"],
                    key=api_key_dict["key"],
                )
                if api_key_dict
                else None
            ),
            metrics_enabled=spec.get("metrics", {}).get("enabled", False),
        )


@dataclass
class Condition:
    """Kubernetes condition."""

    type: str
    status: str
    last_transition_time: datetime
    reason: str = ""
    message: str = ""

    def to_dict(self) -> dict:
        """Convert to Kubernetes condition dict."""
        return {
            "type": self.type,
            "status": self.status,
            "lastTransitionTime": self.last_transition_time.isoformat(),
            "reason": self.reason,
            "message": self.message,
        }


@dataclass
class ClusterStatus:
    """Status of a QdrantCluster."""

    phase: ClusterPhase
    replicas: int = 0
    ready_replicas: int = 0
    helm_release: str | None = None
    endpoint: str | None = None
    version: str | None = None
    conditions: list[Condition] = field(default_factory=list)
    observed_generation: int | None = None

    def to_dict(self) -> dict:
        """Convert to Kubernetes status dict."""
        return {
            "phase": self.phase.value,
            "replicas": self.replicas,
            "readyReplicas": self.ready_replicas,
            "helmRelease": self.helm_release,
            "endpoint": self.endpoint,
            "version": self.version,
            "conditions": [c.to_dict() for c in self.conditions],
            "observedGeneration": self.observed_generation,
        }


@dataclass(frozen=True)
class Snapshot:
    """Qdrant collection snapshot."""

    name: str
    collection: str
    size_bytes: int
    created_at: datetime


@dataclass(frozen=True)
class BackupSpec:
    """Specification for a QdrantBackup."""

    name: str
    namespace: str
    cluster_ref: ClusterRef
    storage: S3StorageSpec
    collections: tuple[str, ...] = ()
    retention_days: int | None = None

    @staticmethod
    def from_dict(spec: dict, meta: dict) -> "BackupSpec":
        """Create BackupSpec from Kubernetes resource dicts."""
        cluster_ref = spec["clusterRef"]
        s3_spec = spec["storage"]["s3"]
        creds_ref = s3_spec["credentialsSecretRef"]

        return BackupSpec(
            name=meta["name"],
            namespace=meta["namespace"],
            cluster_ref=ClusterRef(
                name=cluster_ref["name"],
                namespace=cluster_ref.get("namespace", "default"),
            ),
            storage=S3StorageSpec(
                bucket=s3_spec["bucket"],
                prefix=s3_spec.get("prefix", ""),
                region=s3_spec.get("region", "us-east-1"),
                endpoint=s3_spec.get("endpoint"),
                force_path_style=s3_spec.get("forcePathStyle", False),
                credentials_secret_ref=SecretRef(
                    name=creds_ref["name"],
                    key=creds_ref.get("accessKeyIdKey", "AWS_ACCESS_KEY_ID"),
                ),
            ),
            collections=tuple(spec.get("collections", [])),
            retention_days=spec.get("retentionDays"),
        )


@dataclass
class CollectionBackupStatus:
    """Status of a single collection backup."""

    name: str
    snapshot_name: str | None = None
    size: str | None = None
    status: str = "Pending"
    error: str | None = None

    def to_dict(self) -> dict:
        """Convert to dict."""
        result: dict = {"name": self.name, "status": self.status}
        if self.snapshot_name:
            result["snapshotName"] = self.snapshot_name
        if self.size:
            result["size"] = self.size
        if self.error:
            result["error"] = self.error
        return result


@dataclass
class BackupStatus:
    """Status of a QdrantBackup."""

    phase: BackupPhase
    start_time: datetime | None = None
    completion_time: datetime | None = None
    s3_path: str | None = None
    total_size: str | None = None
    collections: list[CollectionBackupStatus] = field(default_factory=list)
    error: str | None = None
    conditions: list[Condition] = field(default_factory=list)

    def to_dict(self) -> dict:
        """Convert to Kubernetes status dict."""
        result: dict = {"phase": self.phase.value}
        if self.start_time:
            result["startTime"] = self.start_time.isoformat()
        if self.completion_time:
            result["completionTime"] = self.completion_time.isoformat()
        if self.s3_path:
            result["s3Path"] = self.s3_path
        if self.total_size:
            result["totalSize"] = self.total_size
        if self.collections:
            result["collections"] = [c.to_dict() for c in self.collections]
        if self.error:
            result["error"] = self.error
        if self.conditions:
            result["conditions"] = [c.to_dict() for c in self.conditions]
        return result


@dataclass(frozen=True)
class BackupScheduleSpec:
    """Specification for a QdrantBackupSchedule."""

    name: str
    namespace: str
    schedule: str
    cluster_ref: ClusterRef
    storage: S3StorageSpec
    collections: tuple[str, ...] = ()
    retention_policy: RetentionPolicy = field(default_factory=RetentionPolicy)
    suspend: bool = False

    @staticmethod
    def from_dict(spec: dict, meta: dict) -> "BackupScheduleSpec":
        """Create BackupScheduleSpec from Kubernetes resource dicts."""
        cluster_ref = spec["clusterRef"]
        s3_spec = spec["storage"]["s3"]
        creds_ref = s3_spec["credentialsSecretRef"]
        retention = spec.get("retentionPolicy", {})

        return BackupScheduleSpec(
            name=meta["name"],
            namespace=meta["namespace"],
            schedule=spec["schedule"],
            cluster_ref=ClusterRef(
                name=cluster_ref["name"],
                namespace=cluster_ref.get("namespace", "default"),
            ),
            storage=S3StorageSpec(
                bucket=s3_spec["bucket"],
                prefix=s3_spec.get("prefix", ""),
                region=s3_spec.get("region", "us-east-1"),
                endpoint=s3_spec.get("endpoint"),
                force_path_style=s3_spec.get("forcePathStyle", False),
                credentials_secret_ref=SecretRef(
                    name=creds_ref["name"],
                    key=creds_ref.get("accessKeyIdKey", "AWS_ACCESS_KEY_ID"),
                ),
            ),
            collections=tuple(spec.get("collections", [])),
            retention_policy=RetentionPolicy(
                keep_last=retention.get("keepLast"),
                keep_daily=retention.get("keepDaily"),
                keep_weekly=retention.get("keepWeekly"),
                keep_monthly=retention.get("keepMonthly"),
            ),
            suspend=spec.get("suspend", False),
        )


@dataclass
class RecentBackup:
    """Recent backup entry for schedule status."""

    name: str
    creation_time: datetime
    completion_time: datetime | None = None
    status: str = "Pending"
    size: str | None = None

    def to_dict(self) -> dict:
        """Convert to dict."""
        result: dict = {
            "name": self.name,
            "creationTime": self.creation_time.isoformat(),
            "status": self.status,
        }
        if self.completion_time:
            result["completionTime"] = self.completion_time.isoformat()
        if self.size:
            result["size"] = self.size
        return result


@dataclass
class BackupScheduleStatus:
    """Status of a QdrantBackupSchedule."""

    phase: SchedulePhase
    last_backup_time: datetime | None = None
    last_backup_name: str | None = None
    last_backup_status: str | None = None
    next_backup_time: datetime | None = None
    active_backup: str | None = None
    recent_backups: list[RecentBackup] = field(default_factory=list)
    conditions: list[Condition] = field(default_factory=list)

    @staticmethod
    def from_dict(status: dict) -> "BackupScheduleStatus":
        """Create from Kubernetes status dict."""
        phase_str = status.get("phase", "Active")
        return BackupScheduleStatus(
            phase=SchedulePhase(phase_str) if phase_str else SchedulePhase.ACTIVE,
            last_backup_time=(
                datetime.fromisoformat(status["lastBackupTime"])
                if status.get("lastBackupTime")
                else None
            ),
            last_backup_name=status.get("lastBackupName"),
            last_backup_status=status.get("lastBackupStatus"),
            next_backup_time=(
                datetime.fromisoformat(status["nextBackupTime"])
                if status.get("nextBackupTime")
                else None
            ),
            active_backup=status.get("activeBackup"),
        )

    def to_dict(self) -> dict:
        """Convert to Kubernetes status dict."""
        result: dict = {"phase": self.phase.value}
        if self.last_backup_time:
            result["lastBackupTime"] = self.last_backup_time.isoformat()
        if self.last_backup_name:
            result["lastBackupName"] = self.last_backup_name
        if self.last_backup_status:
            result["lastBackupStatus"] = self.last_backup_status
        if self.next_backup_time:
            result["nextBackupTime"] = self.next_backup_time.isoformat()
        if self.active_backup:
            result["activeBackup"] = self.active_backup
        if self.recent_backups:
            result["recentBackups"] = [b.to_dict() for b in self.recent_backups]
        if self.conditions:
            result["conditions"] = [c.to_dict() for c in self.conditions]
        return result


@dataclass(frozen=True)
class RestoreSpec:
    """Specification for a QdrantRestore."""

    name: str
    namespace: str
    target_cluster_ref: ClusterRef
    backup_ref: BackupRef | None = None
    source_s3: S3StorageSpec | None = None
    collections: tuple[str, ...] = ()
    collection_mapping: dict[str, str] = field(default_factory=dict)
    wait_for_indexing: bool = True

    @staticmethod
    def from_dict(spec: dict, meta: dict) -> "RestoreSpec":
        """Create RestoreSpec from Kubernetes resource dicts."""
        target_ref = spec["targetClusterRef"]
        backup_ref_dict = spec.get("backupRef")
        source_dict = spec.get("source", {}).get("s3")

        source_s3 = None
        if source_dict:
            creds_ref = source_dict["credentialsSecretRef"]
            source_s3 = S3StorageSpec(
                bucket=source_dict["bucket"],
                prefix=source_dict.get("path", ""),
                region=source_dict.get("region", "us-east-1"),
                endpoint=source_dict.get("endpoint"),
                force_path_style=source_dict.get("forcePathStyle", False),
                credentials_secret_ref=SecretRef(
                    name=creds_ref["name"],
                    key=creds_ref.get("accessKeyIdKey", "AWS_ACCESS_KEY_ID"),
                ),
            )

        return RestoreSpec(
            name=meta["name"],
            namespace=meta["namespace"],
            target_cluster_ref=ClusterRef(
                name=target_ref["name"],
                namespace=target_ref.get("namespace", "default"),
            ),
            backup_ref=(
                BackupRef(
                    name=backup_ref_dict["name"],
                    namespace=backup_ref_dict.get("namespace", "default"),
                )
                if backup_ref_dict
                else None
            ),
            source_s3=source_s3,
            collections=tuple(spec.get("collections", [])),
            collection_mapping=spec.get("collectionMapping", {}),
            wait_for_indexing=spec.get("waitForIndexing", True),
        )


@dataclass
class RestoredCollection:
    """Status of a restored collection."""

    name: str
    original_name: str | None = None
    status: str = "Pending"
    size: str | None = None
    points_count: int | None = None
    error: str | None = None

    def to_dict(self) -> dict:
        """Convert to dict."""
        result: dict = {"name": self.name, "status": self.status}
        if self.original_name:
            result["originalName"] = self.original_name
        if self.size:
            result["size"] = self.size
        if self.points_count is not None:
            result["pointsCount"] = self.points_count
        if self.error:
            result["error"] = self.error
        return result


@dataclass
class RestoreProgress:
    """Progress of a restore operation."""

    collections_total: int = 0
    collections_completed: int = 0
    current_collection: str | None = None
    percentage: int = 0

    def to_dict(self) -> dict:
        """Convert to dict."""
        result: dict = {
            "collectionsTotal": self.collections_total,
            "collectionsCompleted": self.collections_completed,
            "percentage": self.percentage,
        }
        if self.current_collection:
            result["currentCollection"] = self.current_collection
        return result


@dataclass
class RestoreStatus:
    """Status of a QdrantRestore."""

    phase: RestorePhase
    start_time: datetime | None = None
    completion_time: datetime | None = None
    source_backup: str | None = None
    restored_collections: list[RestoredCollection] = field(default_factory=list)
    progress: RestoreProgress = field(default_factory=RestoreProgress)
    error: str | None = None
    conditions: list[Condition] = field(default_factory=list)

    def to_dict(self) -> dict:
        """Convert to Kubernetes status dict."""
        result: dict = {"phase": self.phase.value}
        if self.start_time:
            result["startTime"] = self.start_time.isoformat()
        if self.completion_time:
            result["completionTime"] = self.completion_time.isoformat()
        if self.source_backup:
            result["sourceBackup"] = self.source_backup
        if self.restored_collections:
            result["restoredCollections"] = [c.to_dict() for c in self.restored_collections]
        result["progress"] = self.progress.to_dict()
        if self.error:
            result["error"] = self.error
        if self.conditions:
            result["conditions"] = [c.to_dict() for c in self.conditions]
        return result


def build_helm_values(spec: ClusterSpec) -> dict:
    """Build Helm values dict from ClusterSpec."""
    persistence: dict = {
        "size": spec.persistence.size,
        "accessModes": list(spec.persistence.access_modes),
    }
    if spec.persistence.storage_class:
        persistence = {**persistence, "storageClassName": spec.persistence.storage_class}

    resources: dict = {}
    if spec.resources.requests.cpu or spec.resources.requests.memory:
        requests = {
            k: v
            for k, v in [
                ("cpu", spec.resources.requests.cpu),
                ("memory", spec.resources.requests.memory),
            ]
            if v
        }
        resources = {"requests": requests}

    if spec.resources.limits.cpu or spec.resources.limits.memory:
        limits = {
            k: v
            for k, v in [
                ("cpu", spec.resources.limits.cpu),
                ("memory", spec.resources.limits.memory),
            ]
            if v
        }
        resources = {**resources, "limits": limits}

    tag = spec.version if spec.version.startswith("v") else f"v{spec.version}"
    values: dict = {
        "replicaCount": spec.replicas,
        "image": {"tag": tag},
        "persistence": persistence,
        "config": {"cluster": {"enabled": spec.cluster_enabled}},
    }

    if resources:
        values = {**values, "resources": resources}

    if spec.metrics_enabled:
        values = {**values, "metrics": {"serviceMonitor": {"enabled": True}}}

    return values