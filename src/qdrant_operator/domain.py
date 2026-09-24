"""Domain entities and value objects for the Qdrant operator."""

from collections.abc import Callable
from collections.abc import Iterable
from collections.abc import Mapping
from dataclasses import dataclass
from dataclasses import field
from dataclasses import replace
from datetime import UTC
from datetime import datetime
from datetime import timedelta
from enum import StrEnum
from typing import Any
from typing import cast

from croniter import croniter

GROUP = "qdrant.io"
VERSION = "v1alpha1"
API_VERSION = f"{GROUP}/{VERSION}"
SCHEDULE_LABEL = f"{GROUP}/schedule"
QDRANT_HTTP_PORT = 6333
CLUSTER_DOMAIN = "cluster.local"
MANIFEST_KEY = "manifest.json"
AUTO_GENERATED_API_KEY_KEY = "api-key"
TLS_MOUNT_PATH = "/qdrant/tls"

JsonDict = dict[str, Any]


class ClusterPhase(StrEnum):
    PENDING = "Pending"
    RUNNING = "Running"
    FAILED = "Failed"
    UPGRADING = "Upgrading"
    TERMINATING = "Terminating"


class BackupPhase(StrEnum):
    PENDING = "Pending"
    IN_PROGRESS = "InProgress"
    COMPLETED = "Completed"
    FAILED = "Failed"


class RestorePhase(StrEnum):
    PENDING = "Pending"
    DOWNLOADING = "Downloading"
    RESTORING = "Restoring"
    INDEXING = "Indexing"
    COMPLETED = "Completed"
    FAILED = "Failed"


class SchedulePhase(StrEnum):
    ACTIVE = "Active"
    SUSPENDED = "Suspended"


class ConcurrencyPolicy(StrEnum):
    ALLOW = "Allow"
    FORBID = "Forbid"
    REPLACE = "Replace"


class RestorePriority(StrEnum):
    SNAPSHOT = "snapshot"
    REPLICA = "replica"
    NO_SYNC = "no_sync"


class ConditionStatus(StrEnum):
    TRUE = "True"
    FALSE = "False"
    UNKNOWN = "Unknown"


@dataclass(frozen=True)
class ResourceKind:
    """Identifies one of the operator's custom resource kinds."""

    kind: str
    plural: str

    @property
    def group(self) -> str:
        return GROUP

    @property
    def version(self) -> str:
        return VERSION


CLUSTERS = ResourceKind("QdrantCluster", "qdrantclusters")
BACKUPS = ResourceKind("QdrantBackup", "qdrantbackups")
SCHEDULES = ResourceKind("QdrantBackupSchedule", "qdrantbackupschedules")
RESTORES = ResourceKind("QdrantRestore", "qdrantrestores")


@dataclass(frozen=True)
class ResourceRef:
    """Namespaced name of a custom resource."""

    kind: ResourceKind
    name: str
    namespace: str


def join_key(*parts: str) -> str:
    """Join S3 key parts, dropping empty segments and stray slashes."""
    return "/".join(segment for part in parts for segment in part.split("/") if segment)


def format_size(size_bytes: int) -> str:
    size = float(size_bytes)
    for unit in ("B", "KB", "MB", "GB", "TB"):
        if size < 1024:
            return f"{size:.1f}{unit}"
        size /= 1024
    return f"{size:.1f}PB"


def parse_time(value: str | None) -> datetime | None:
    if not value:
        return None
    return datetime.fromisoformat(value.replace("Z", "+00:00"))


def format_time(value: datetime) -> str:
    return value.astimezone(UTC).isoformat().replace("+00:00", "Z")


def release_name(cluster_name: str) -> str:
    return f"qdrant-{cluster_name}"


def chart_version(version: str) -> str:
    return version.removeprefix("v")


def drop_empty(values: Mapping[str, Any]) -> JsonDict:
    """Return a copy without None values, so serialized status dicts stay sparse."""
    return {key: value for key, value in values.items() if value is not None}


@dataclass(frozen=True)
class ResourceRequirements:
    cpu: str | None = None
    memory: str | None = None

    @staticmethod
    def from_dict(data: Mapping[str, Any]) -> "ResourceRequirements":
        return ResourceRequirements(cpu=data.get("cpu"), memory=data.get("memory"))

    def to_dict(self) -> JsonDict:
        return drop_empty({"cpu": self.cpu, "memory": self.memory})


@dataclass(frozen=True)
class Resources:
    requests: ResourceRequirements = field(default_factory=ResourceRequirements)
    limits: ResourceRequirements = field(default_factory=ResourceRequirements)

    @staticmethod
    def from_dict(data: Mapping[str, Any]) -> "Resources":
        return Resources(
            requests=ResourceRequirements.from_dict(data.get("requests", {})),
            limits=ResourceRequirements.from_dict(data.get("limits", {})),
        )

    def to_dict(self) -> JsonDict:
        return {
            key: value
            for key, value in (
                ("requests", self.requests.to_dict()),
                ("limits", self.limits.to_dict()),
            )
            if value
        }


@dataclass(frozen=True)
class ImageSpec:
    repository: str = "docker.io/qdrant/qdrant"
    pull_policy: str = "IfNotPresent"

    @staticmethod
    def from_dict(data: Mapping[str, Any]) -> "ImageSpec":
        return ImageSpec(
            repository=data.get("repository", ImageSpec.repository),
            pull_policy=data.get("pullPolicy", ImageSpec.pull_policy),
        )


@dataclass(frozen=True)
class PersistenceSpec:
    size: str = "10Gi"
    storage_class: str | None = None
    access_modes: tuple[str, ...] = ("ReadWriteOnce",)

    @staticmethod
    def from_dict(data: Mapping[str, Any]) -> "PersistenceSpec":
        return PersistenceSpec(
            size=data.get("size", PersistenceSpec.size),
            storage_class=data.get("storageClassName"),
            access_modes=tuple(data.get("accessModes", PersistenceSpec.access_modes)),
        )

    def to_helm_values(self) -> JsonDict:
        return drop_empty(
            {
                "size": self.size,
                "accessModes": list(self.access_modes),
                "storageClassName": self.storage_class,
            }
        )


@dataclass(frozen=True)
class SnapshotPersistenceSpec:
    enabled: bool = False
    size: str = "10Gi"
    storage_class: str | None = None

    @staticmethod
    def from_dict(data: Mapping[str, Any]) -> "SnapshotPersistenceSpec":
        return SnapshotPersistenceSpec(
            enabled=data.get("enabled", False),
            size=data.get("size", SnapshotPersistenceSpec.size),
            storage_class=data.get("storageClassName"),
        )

    def to_helm_values(self) -> JsonDict:
        return drop_empty(
            {"enabled": self.enabled, "size": self.size, "storageClassName": self.storage_class}
        )


@dataclass(frozen=True)
class SecretRef:
    """Reference to one key of a Kubernetes Secret."""

    name: str
    key: str
    namespace: str

    @staticmethod
    def from_dict(data: Mapping[str, Any], namespace: str) -> "SecretRef":
        return SecretRef(name=data["name"], key=data["key"], namespace=namespace)

    def to_helm_value_from(self) -> JsonDict:
        return {"valueFrom": {"secretKeyRef": {"name": self.name, "key": self.key}}}


@dataclass(frozen=True)
class ApiKeySpec:
    secret_ref: SecretRef | None = None
    auto_generate: bool = False

    @staticmethod
    def from_dict(data: Mapping[str, Any], namespace: str) -> "ApiKeySpec":
        secret_ref = data.get("secretRef")
        return ApiKeySpec(
            secret_ref=SecretRef.from_dict(secret_ref, namespace) if secret_ref else None,
            auto_generate=data.get("autoGenerate", False),
        )

    def to_helm_value(self) -> JsonDict | bool:
        if self.secret_ref:
            return self.secret_ref.to_helm_value_from()
        return self.auto_generate

    def resolve_secret_ref(self, cluster_name: str, namespace: str) -> SecretRef | None:
        """Where the operator reads the key from: the user's secret or the chart-generated one."""
        if self.secret_ref:
            return self.secret_ref
        if not self.auto_generate:
            return None
        return SecretRef(
            name=f"{release_name(cluster_name)}-apikey",
            key=AUTO_GENERATED_API_KEY_KEY,
            namespace=namespace,
        )


@dataclass(frozen=True)
class TlsSpec:
    enabled: bool = False
    secret_name: str | None = None

    @staticmethod
    def from_dict(data: Mapping[str, Any]) -> "TlsSpec":
        return TlsSpec(
            enabled=data.get("enabled", False),
            secret_name=data.get("secretRef", {}).get("name"),
        )

    def ca_secret_ref(self, namespace: str) -> SecretRef | None:
        if not self.enabled or not self.secret_name:
            return None
        return SecretRef(name=self.secret_name, key="ca.crt", namespace=namespace)


@dataclass(frozen=True)
class ServiceSpec:
    type: str = "ClusterIP"
    annotations: Mapping[str, str] = field(default_factory=dict[str, str])

    @staticmethod
    def from_dict(data: Mapping[str, Any]) -> "ServiceSpec":
        return ServiceSpec(
            type=data.get("type", ServiceSpec.type),
            annotations=dict[str, str](data.get("annotations", {})),
        )

    def to_helm_values(self) -> JsonDict:
        return {"type": self.type, "annotations": dict(self.annotations)}


@dataclass(frozen=True)
class ServiceMonitorSpec:
    enabled: bool = False
    interval: str = "30s"
    labels: Mapping[str, str] = field(default_factory=dict[str, str])

    @staticmethod
    def from_dict(data: Mapping[str, Any]) -> "ServiceMonitorSpec":
        return ServiceMonitorSpec(
            enabled=data.get("enabled", False),
            interval=data.get("interval", ServiceMonitorSpec.interval),
            labels=dict[str, str](data.get("labels", {})),
        )


@dataclass(frozen=True)
class MetricsSpec:
    enabled: bool = False
    service_monitor: ServiceMonitorSpec = field(default_factory=ServiceMonitorSpec)

    @staticmethod
    def from_dict(data: Mapping[str, Any]) -> "MetricsSpec":
        return MetricsSpec(
            enabled=data.get("enabled", False),
            service_monitor=ServiceMonitorSpec.from_dict(data.get("serviceMonitor", {})),
        )

    def to_helm_values(self) -> JsonDict:
        monitor_enabled = self.enabled and self.service_monitor.enabled
        return {
            "serviceMonitor": {
                "enabled": monitor_enabled,
                "scrapeInterval": self.service_monitor.interval,
                "additionalLabels": dict(self.service_monitor.labels),
            }
        }


@dataclass(frozen=True)
class SchedulingSpec:
    node_selector: Mapping[str, str] = field(default_factory=dict[str, str])
    tolerations: tuple[JsonDict, ...] = ()
    affinity: JsonDict = field(default_factory=dict[str, Any])

    @staticmethod
    def from_dict(spec: Mapping[str, Any]) -> "SchedulingSpec":
        return SchedulingSpec(
            node_selector=dict[str, str](spec.get("nodeSelector", {})),
            tolerations=tuple[JsonDict, ...](spec.get("tolerations", [])),
            affinity=dict[str, Any](spec.get("affinity", {})),
        )


@dataclass(frozen=True)
class DistributedSpec:
    enabled: bool = True
    p2p_port: int = 6335
    p2p_tls: bool = False

    @staticmethod
    def from_dict(data: Mapping[str, Any]) -> "DistributedSpec":
        p2p = data.get("p2p", {})
        return DistributedSpec(
            enabled=data.get("enabled", True),
            p2p_port=p2p.get("port", DistributedSpec.p2p_port),
            p2p_tls=p2p.get("enableTls", False),
        )


@dataclass(frozen=True)
class ClusterSpec:
    name: str
    namespace: str
    version: str
    replicas: int = 1
    image: ImageSpec = field(default_factory=ImageSpec)
    resources: Resources = field(default_factory=Resources)
    persistence: PersistenceSpec = field(default_factory=PersistenceSpec)
    snapshot_persistence: SnapshotPersistenceSpec = field(default_factory=SnapshotPersistenceSpec)
    distributed: DistributedSpec = field(default_factory=DistributedSpec)
    service: ServiceSpec = field(default_factory=ServiceSpec)
    api_key: ApiKeySpec = field(default_factory=ApiKeySpec)
    read_only_api_key: ApiKeySpec = field(default_factory=ApiKeySpec)
    tls: TlsSpec = field(default_factory=TlsSpec)
    metrics: MetricsSpec = field(default_factory=MetricsSpec)
    scheduling: SchedulingSpec = field(default_factory=SchedulingSpec)
    config: JsonDict = field(default_factory=dict[str, Any])

    @staticmethod
    def from_dict(spec: Mapping[str, Any], meta: Mapping[str, Any]) -> "ClusterSpec":
        namespace = meta["namespace"]
        return ClusterSpec(
            name=meta["name"],
            namespace=namespace,
            version=spec["version"],
            replicas=spec.get("replicas", 1),
            image=ImageSpec.from_dict(spec.get("image", {})),
            resources=Resources.from_dict(spec.get("resources", {})),
            persistence=PersistenceSpec.from_dict(spec.get("persistence", {})),
            snapshot_persistence=SnapshotPersistenceSpec.from_dict(
                spec.get("snapshotPersistence", {})
            ),
            distributed=DistributedSpec.from_dict(spec.get("cluster", {})),
            service=ServiceSpec.from_dict(spec.get("service", {})),
            api_key=ApiKeySpec.from_dict(spec.get("apiKey", {}), namespace),
            read_only_api_key=ApiKeySpec.from_dict(spec.get("readOnlyApiKey", {}), namespace),
            tls=TlsSpec.from_dict(spec.get("tls", {})),
            metrics=MetricsSpec.from_dict(spec.get("metrics", {})),
            scheduling=SchedulingSpec.from_dict(spec),
            config=dict[str, Any](spec.get("config", {})),
        )

    @property
    def release_name(self) -> str:
        return release_name(self.name)

    @property
    def scheme(self) -> str:
        return "https" if self.tls.enabled else "http"

    def service_url(self) -> str:
        host = f"{self.release_name}.{self.namespace}.svc.{CLUSTER_DOMAIN}"
        return f"{self.scheme}://{host}:{QDRANT_HTTP_PORT}"

    def node_urls(self) -> tuple[str, ...]:
        """One URL per StatefulSet pod via the chart's headless service."""
        headless = f"{self.release_name}-headless.{self.namespace}.svc.{CLUSTER_DOMAIN}"
        return tuple(
            f"{self.scheme}://{self.release_name}-{ordinal}.{headless}:{QDRANT_HTTP_PORT}"
            for ordinal in range(self.replicas)
        )

    def to_helm_values(self) -> JsonDict:
        config = merge_dicts(
            {
                "cluster": {
                    "enabled": self.distributed.enabled,
                    "p2p": {
                        "port": self.distributed.p2p_port,
                        "enable_tls": self.distributed.p2p_tls,
                    },
                },
                "service": {"enable_tls": self.tls.enabled},
            },
            self.tls_config(),
            self.config,
        )
        values: JsonDict = {
            "replicaCount": self.replicas,
            "image": {"repository": self.image.repository, "pullPolicy": self.image.pull_policy},
            "resources": self.resources.to_dict(),
            "persistence": self.persistence.to_helm_values(),
            "snapshotPersistence": self.snapshot_persistence.to_helm_values(),
            "service": self.service.to_helm_values(),
            "apiKey": self.api_key.to_helm_value(),
            "readOnlyApiKey": self.read_only_api_key.to_helm_value(),
            "metrics": self.metrics.to_helm_values(),
            "nodeSelector": dict(self.scheduling.node_selector),
            "tolerations": list(self.scheduling.tolerations),
            "affinity": dict(self.scheduling.affinity),
            "config": config,
        }
        return {**values, **self.tls_volumes()}

    def tls_config(self) -> JsonDict:
        if not self.tls.enabled:
            return {}
        return {"tls": {"cert": f"{TLS_MOUNT_PATH}/tls.crt", "key": f"{TLS_MOUNT_PATH}/tls.key"}}

    def tls_volumes(self) -> JsonDict:
        if not self.tls.enabled or not self.tls.secret_name:
            return {}
        return {
            "additionalVolumes": [{"name": "tls", "secret": {"secretName": self.tls.secret_name}}],
            "additionalVolumeMounts": [
                {"name": "tls", "mountPath": TLS_MOUNT_PATH, "readOnly": True}
            ],
        }


def merge_dicts(*layers: Mapping[str, Any]) -> JsonDict:
    """Deep-merge mappings left to right; later layers win on scalar conflicts."""
    merged: JsonDict = {}
    for layer in layers:
        for key, value in layer.items():
            current = merged.get(key)
            if isinstance(current, Mapping) and isinstance(value, Mapping):
                merged[key] = merge_dicts(cast(JsonDict, current), cast(JsonDict, value))
                continue
            merged[key] = value
    return merged


@dataclass(frozen=True)
class Condition:
    type: str
    status: ConditionStatus
    last_transition_time: datetime
    reason: str = ""
    message: str = ""

    @staticmethod
    def from_dict(data: Mapping[str, Any]) -> "Condition":
        return Condition(
            type=data["type"],
            status=ConditionStatus(data["status"]),
            last_transition_time=parse_time(data.get("lastTransitionTime")) or datetime.now(UTC),
            reason=data.get("reason", ""),
            message=data.get("message", ""),
        )

    def to_dict(self) -> JsonDict:
        return {
            "type": self.type,
            "status": self.status.value,
            "lastTransitionTime": format_time(self.last_transition_time),
            "reason": self.reason,
            "message": self.message,
        }


def set_condition(conditions: Iterable[Condition], update: Condition) -> list[Condition]:
    """Replace the same-typed condition; keep its transition time when the status did not change."""
    existing = {condition.type: condition for condition in conditions}
    previous = existing.get(update.type)
    unchanged = previous is not None and previous.status == update.status
    merged = (
        replace(update, last_transition_time=previous.last_transition_time)
        if previous and unchanged
        else update
    )
    return [*(c for c in existing.values() if c.type != update.type), merged]


def conditions_from_dict(data: Mapping[str, Any]) -> list[Condition]:
    return [Condition.from_dict(item) for item in data.get("conditions", [])]


@dataclass(frozen=True)
class ClusterStatus:
    phase: ClusterPhase
    replicas: int = 0
    ready_replicas: int = 0
    helm_release: str | None = None
    endpoint: str | None = None
    version: str | None = None
    conditions: tuple[Condition, ...] = ()
    observed_generation: int | None = None

    @staticmethod
    def from_dict(data: Mapping[str, Any]) -> "ClusterStatus":
        return ClusterStatus(
            phase=ClusterPhase(data.get("phase", ClusterPhase.PENDING)),
            replicas=data.get("replicas", 0),
            ready_replicas=data.get("readyReplicas", 0),
            helm_release=data.get("helmRelease"),
            endpoint=data.get("endpoint"),
            version=data.get("version"),
            conditions=tuple(conditions_from_dict(data)),
            observed_generation=data.get("observedGeneration"),
        )

    def to_dict(self) -> JsonDict:
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
class StatefulSetStatus:
    replicas: int
    ready_replicas: int


@dataclass(frozen=True)
class Snapshot:
    name: str
    collection: str
    size_bytes: int
    checksum: str | None = None


@dataclass(frozen=True)
class QdrantNode:
    """One reachable Qdrant HTTP endpoint together with what it takes to talk to it."""

    url: str
    api_key: str | None = None
    ca_cert: str | None = None


@dataclass(frozen=True)
class ClusterConnection:
    """Resolved addresses of a QdrantCluster: the Service plus every StatefulSet pod."""

    service: QdrantNode
    nodes: tuple[QdrantNode, ...]


@dataclass(frozen=True)
class ClusterRef:
    name: str
    namespace: str

    @staticmethod
    def from_dict(data: Mapping[str, Any], default_namespace: str) -> "ClusterRef":
        return ClusterRef(name=data["name"], namespace=data.get("namespace", default_namespace))

    def to_dict(self) -> JsonDict:
        return {"name": self.name, "namespace": self.namespace}

    def to_resource_ref(self) -> ResourceRef:
        return ResourceRef(CLUSTERS, self.name, self.namespace)


@dataclass(frozen=True)
class CredentialsSecretRef:
    """Secret holding both halves of an S3 credential pair."""

    name: str
    namespace: str
    access_key_id_key: str = "AWS_ACCESS_KEY_ID"
    secret_access_key_key: str = "AWS_SECRET_ACCESS_KEY"

    @staticmethod
    def from_dict(data: Mapping[str, Any], namespace: str) -> "CredentialsSecretRef":
        return CredentialsSecretRef(
            name=data["name"],
            namespace=namespace,
            access_key_id_key=data.get("accessKeyIdKey", CredentialsSecretRef.access_key_id_key),
            secret_access_key_key=data.get(
                "secretAccessKeyKey", CredentialsSecretRef.secret_access_key_key
            ),
        )

    def to_dict(self) -> JsonDict:
        return {
            "name": self.name,
            "accessKeyIdKey": self.access_key_id_key,
            "secretAccessKeyKey": self.secret_access_key_key,
        }

    def access_key_ref(self) -> SecretRef:
        return SecretRef(self.name, self.access_key_id_key, self.namespace)

    def secret_key_ref(self) -> SecretRef:
        return SecretRef(self.name, self.secret_access_key_key, self.namespace)


@dataclass(frozen=True)
class S3Credentials:
    access_key_id: str
    secret_access_key: str


@dataclass(frozen=True)
class S3StorageSpec:
    bucket: str
    credentials_secret_ref: CredentialsSecretRef
    prefix: str = ""
    region: str = "us-east-1"
    endpoint: str | None = None
    force_path_style: bool = False

    @staticmethod
    def from_dict(data: Mapping[str, Any], namespace: str) -> "S3StorageSpec":
        return S3StorageSpec(
            bucket=data["bucket"],
            credentials_secret_ref=CredentialsSecretRef.from_dict(
                data["credentialsSecretRef"], namespace
            ),
            prefix=data.get("path", data.get("prefix", "")),
            region=data.get("region", S3StorageSpec.region),
            endpoint=data.get("endpoint"),
            force_path_style=data.get("forcePathStyle", False),
        )

    def to_dict(self) -> JsonDict:
        return drop_empty(
            {
                "bucket": self.bucket,
                "prefix": self.prefix,
                "region": self.region,
                "endpoint": self.endpoint,
                "forcePathStyle": self.force_path_style,
                "credentialsSecretRef": self.credentials_secret_ref.to_dict(),
            }
        )

    def uri(self, *parts: str) -> str:
        return f"s3://{join_key(self.bucket, self.prefix, *parts)}"


@dataclass(frozen=True)
class BackupSpec:
    name: str
    namespace: str
    cluster_ref: ClusterRef
    storage: S3StorageSpec
    collections: tuple[str, ...] = ()
    retention_days: int | None = None

    @staticmethod
    def from_dict(spec: Mapping[str, Any], meta: Mapping[str, Any]) -> "BackupSpec":
        namespace = meta["namespace"]
        return BackupSpec(
            name=meta["name"],
            namespace=namespace,
            cluster_ref=ClusterRef.from_dict(spec["clusterRef"], namespace),
            storage=S3StorageSpec.from_dict(spec["storage"]["s3"], namespace),
            collections=tuple(spec.get("collections", [])),
            retention_days=spec.get("retentionDays"),
        )

    @property
    def root_key(self) -> str:
        return join_key(self.storage.prefix, self.name)

    def snapshot_key(self, collection: str, node_index: int, snapshot_name: str) -> str:
        return join_key(self.root_key, collection, f"node-{node_index}", snapshot_name)

    def expires_at(self, completion_time: datetime) -> datetime | None:
        if not self.retention_days:
            return None
        return completion_time + timedelta(days=self.retention_days)


@dataclass(frozen=True)
class SnapshotRecord:
    """One stored snapshot: which node it came from and where it lives in the bucket."""

    node_index: int
    key: str
    snapshot_name: str
    size_bytes: int
    checksum: str | None = None

    @staticmethod
    def from_dict(data: Mapping[str, Any]) -> "SnapshotRecord":
        return SnapshotRecord(
            node_index=data["nodeIndex"],
            key=data["key"],
            snapshot_name=data["snapshotName"],
            size_bytes=data["sizeBytes"],
            checksum=data.get("checksum"),
        )

    def to_dict(self) -> JsonDict:
        return drop_empty(
            {
                "nodeIndex": self.node_index,
                "key": self.key,
                "snapshotName": self.snapshot_name,
                "sizeBytes": self.size_bytes,
                "checksum": self.checksum,
            }
        )


@dataclass(frozen=True)
class CollectionBackup:
    name: str
    snapshots: tuple[SnapshotRecord, ...]

    @staticmethod
    def from_dict(data: Mapping[str, Any]) -> "CollectionBackup":
        return CollectionBackup(
            name=data["name"],
            snapshots=tuple(SnapshotRecord.from_dict(s) for s in data.get("snapshots", [])),
        )

    def to_dict(self) -> JsonDict:
        return {"name": self.name, "snapshots": [s.to_dict() for s in self.snapshots]}

    @property
    def size_bytes(self) -> int:
        return sum(s.size_bytes for s in self.snapshots)


@dataclass(frozen=True)
class BackupManifest:
    """Index of a completed backup, stored next to the snapshots so restores need no guessing."""

    backup_name: str
    cluster: ClusterRef
    node_count: int
    created_at: datetime
    collections: tuple[CollectionBackup, ...]

    @staticmethod
    def from_dict(data: Mapping[str, Any]) -> "BackupManifest":
        cluster = data["cluster"]
        return BackupManifest(
            backup_name=data["backupName"],
            cluster=ClusterRef.from_dict(cluster, cluster["namespace"]),
            node_count=data["nodeCount"],
            created_at=parse_time(data["createdAt"]) or datetime.now(UTC),
            collections=tuple(CollectionBackup.from_dict(c) for c in data.get("collections", [])),
        )

    def to_dict(self) -> JsonDict:
        return {
            "backupName": self.backup_name,
            "cluster": self.cluster.to_dict(),
            "nodeCount": self.node_count,
            "createdAt": format_time(self.created_at),
            "collections": [c.to_dict() for c in self.collections],
        }

    def collection(self, name: str) -> CollectionBackup | None:
        return next((c for c in self.collections if c.name == name), None)

    @property
    def collection_names(self) -> tuple[str, ...]:
        return tuple(c.name for c in self.collections)

    @property
    def size_bytes(self) -> int:
        return sum(c.size_bytes for c in self.collections)


@dataclass(frozen=True)
class CollectionBackupStatus:
    name: str
    status: str
    snapshots: tuple[SnapshotRecord, ...] = ()
    error: str | None = None

    def to_dict(self) -> JsonDict:
        return {
            "name": self.name,
            "status": self.status,
            "size": format_size(sum(s.size_bytes for s in self.snapshots)),
            "snapshots": [
                {"node": f"node-{s.node_index}", "snapshotName": s.snapshot_name, "key": s.key}
                for s in self.snapshots
            ],
            "error": self.error,
        }


@dataclass(frozen=True)
class BackupStatus:
    phase: BackupPhase
    start_time: datetime | None = None
    completion_time: datetime | None = None
    expires_at: datetime | None = None
    s3_path: str | None = None
    total_size: str | None = None
    collections: tuple[CollectionBackupStatus, ...] = ()
    error: str | None = None
    conditions: tuple[Condition, ...] = ()

    @staticmethod
    def from_dict(data: Mapping[str, Any]) -> "BackupStatus":
        return BackupStatus(
            phase=BackupPhase(data.get("phase", BackupPhase.PENDING)),
            start_time=parse_time(data.get("startTime")),
            completion_time=parse_time(data.get("completionTime")),
            expires_at=parse_time(data.get("expiresAt")),
            s3_path=data.get("s3Path"),
            total_size=data.get("totalSize"),
            error=data.get("error"),
            conditions=tuple(conditions_from_dict(data)),
        )

    def to_dict(self) -> JsonDict:
        return {
            "phase": self.phase.value,
            "startTime": format_time(self.start_time) if self.start_time else None,
            "completionTime": (format_time(self.completion_time) if self.completion_time else None),
            "expiresAt": format_time(self.expires_at) if self.expires_at else None,
            "s3Path": self.s3_path,
            "totalSize": self.total_size,
            "collections": [c.to_dict() for c in self.collections],
            "error": self.error,
            "conditions": [c.to_dict() for c in self.conditions],
        }

    @property
    def finished(self) -> bool:
        return self.phase in (BackupPhase.COMPLETED, BackupPhase.FAILED)


@dataclass(frozen=True)
class RetentionPolicy:
    keep_last: int | None = None
    keep_daily: int | None = None
    keep_weekly: int | None = None
    keep_monthly: int | None = None

    @staticmethod
    def from_dict(data: Mapping[str, Any]) -> "RetentionPolicy":
        return RetentionPolicy(
            keep_last=data.get("keepLast"),
            keep_daily=data.get("keepDaily"),
            keep_weekly=data.get("keepWeekly"),
            keep_monthly=data.get("keepMonthly"),
        )

    @property
    def configured(self) -> bool:
        return any(
            value is not None
            for value in (self.keep_last, self.keep_daily, self.keep_weekly, self.keep_monthly)
        )

    def expired(self, backups: Iterable["BackupRecord"]) -> list["BackupRecord"]:
        """Backups no bucket of the policy keeps, newest-first grandfather-father-son selection."""
        if not self.configured:
            return []
        ordered = sorted(backups, key=lambda b: b.creation_time, reverse=True)
        keep = {b.name for b in ordered[: self.keep_last or 0]}
        keep |= newest_per_bucket(ordered, self.keep_daily, lambda t: t.strftime("%Y-%m-%d"))
        keep |= newest_per_bucket(ordered, self.keep_weekly, lambda t: t.strftime("%G-W%V"))
        keep |= newest_per_bucket(ordered, self.keep_monthly, lambda t: t.strftime("%Y-%m"))
        return [b for b in ordered if b.name not in keep]


def newest_per_bucket(
    ordered: list["BackupRecord"],
    count: int | None,
    bucket_of: Callable[[datetime], str],
) -> set[str]:
    if not count:
        return set()
    winners: dict[str, str] = {}
    for backup in ordered:
        winners.setdefault(bucket_of(backup.creation_time), backup.name)
    return set(list(winners.values())[:count])


@dataclass(frozen=True)
class BackupRecord:
    """What a schedule needs to know about one of its QdrantBackups."""

    name: str
    creation_time: datetime
    phase: BackupPhase
    completion_time: datetime | None = None
    total_size: str | None = None

    @staticmethod
    def from_resource(body: Mapping[str, Any]) -> "BackupRecord":
        status = BackupStatus.from_dict(body.get("status", {}))
        return BackupRecord(
            name=body["metadata"]["name"],
            creation_time=parse_time(body["metadata"].get("creationTimestamp"))
            or datetime.now(UTC),
            phase=status.phase,
            completion_time=status.completion_time,
            total_size=status.total_size,
        )

    @property
    def finished(self) -> bool:
        return self.phase in (BackupPhase.COMPLETED, BackupPhase.FAILED)

    def to_dict(self) -> JsonDict:
        return {
            "name": self.name,
            "creationTime": format_time(self.creation_time),
            "completionTime": (format_time(self.completion_time) if self.completion_time else None),
            "status": self.phase.value,
            "size": self.total_size,
        }


@dataclass(frozen=True)
class BackupScheduleSpec:
    name: str
    namespace: str
    schedule: str
    cluster_ref: ClusterRef
    storage: S3StorageSpec
    collections: tuple[str, ...] = ()
    retention_policy: RetentionPolicy = field(default_factory=RetentionPolicy)
    suspend: bool = False
    concurrency_policy: ConcurrencyPolicy = ConcurrencyPolicy.FORBID
    starting_deadline_seconds: int | None = None

    @staticmethod
    def from_dict(spec: Mapping[str, Any], meta: Mapping[str, Any]) -> "BackupScheduleSpec":
        namespace = meta["namespace"]
        return BackupScheduleSpec(
            name=meta["name"],
            namespace=namespace,
            schedule=spec["schedule"],
            cluster_ref=ClusterRef.from_dict(spec["clusterRef"], namespace),
            storage=S3StorageSpec.from_dict(spec["storage"]["s3"], namespace),
            collections=tuple(spec.get("collections", [])),
            retention_policy=RetentionPolicy.from_dict(spec.get("retentionPolicy", {})),
            suspend=spec.get("suspend", False),
            concurrency_policy=ConcurrencyPolicy(spec.get("concurrencyPolicy", "Forbid")),
            starting_deadline_seconds=spec.get("startingDeadlineSeconds"),
        )

    @property
    def label_selector(self) -> str:
        return f"{SCHEDULE_LABEL}={self.name}"

    def backup_name(self, at: datetime) -> str:
        return f"{self.name}-{at.strftime('%Y%m%d-%H%M%S')}"

    def previous_slot(self, now: datetime) -> datetime:
        return croniter(self.schedule, now).get_prev(datetime)

    def next_slot(self, now: datetime) -> datetime:
        return croniter(self.schedule, now).get_next(datetime)

    def due(self, now: datetime, baseline: datetime | None) -> datetime | None:
        """The missed slot after `baseline` (last run or creation), or None when up to date."""
        slot = self.previous_slot(now)
        if baseline and baseline >= slot:
            return None
        if self.starting_deadline_seconds is None:
            return slot
        if now - slot > timedelta(seconds=self.starting_deadline_seconds):
            return None
        return slot

    def to_backup_resource(self, backup_name: str, owner: Mapping[str, Any]) -> JsonDict:
        """Body of the QdrantBackup this schedule spawns; owned by the schedule for GC."""
        spec: JsonDict = {
            "clusterRef": self.cluster_ref.to_dict(),
            "storage": {"s3": self.storage.to_dict()},
        }
        if self.collections:
            spec["collections"] = list(self.collections)
        return {
            "apiVersion": API_VERSION,
            "kind": BACKUPS.kind,
            "metadata": {
                "name": backup_name,
                "namespace": self.namespace,
                "labels": {SCHEDULE_LABEL: self.name},
                "ownerReferences": [owner_reference(owner)],
            },
            "spec": spec,
        }


def owner_reference(owner: Mapping[str, Any]) -> JsonDict:
    metadata = owner["metadata"]
    return {
        "apiVersion": owner["apiVersion"],
        "kind": owner["kind"],
        "name": metadata["name"],
        "uid": metadata["uid"],
        "controller": True,
        "blockOwnerDeletion": True,
    }


@dataclass(frozen=True)
class BackupScheduleStatus:
    phase: SchedulePhase
    last_schedule_time: datetime | None = None
    last_backup_time: datetime | None = None
    last_backup_name: str | None = None
    last_backup_status: str | None = None
    next_backup_time: datetime | None = None
    active_backup: str | None = None
    recent_backups: tuple[BackupRecord, ...] = ()
    conditions: tuple[Condition, ...] = ()

    @staticmethod
    def from_dict(data: Mapping[str, Any]) -> "BackupScheduleStatus":
        return BackupScheduleStatus(
            phase=SchedulePhase(data.get("phase", SchedulePhase.ACTIVE)),
            last_schedule_time=parse_time(data.get("lastScheduleTime")),
            last_backup_time=parse_time(data.get("lastBackupTime")),
            last_backup_name=data.get("lastBackupName"),
            last_backup_status=data.get("lastBackupStatus"),
            next_backup_time=parse_time(data.get("nextBackupTime")),
            active_backup=data.get("activeBackup"),
            conditions=tuple(conditions_from_dict(data)),
        )

    def to_dict(self) -> JsonDict:
        return {
            "phase": self.phase.value,
            "lastScheduleTime": (
                format_time(self.last_schedule_time) if self.last_schedule_time else None
            ),
            "lastBackupTime": (
                format_time(self.last_backup_time) if self.last_backup_time else None
            ),
            "lastBackupName": self.last_backup_name,
            "lastBackupStatus": self.last_backup_status,
            "nextBackupTime": (
                format_time(self.next_backup_time) if self.next_backup_time else None
            ),
            "activeBackup": self.active_backup,
            "recentBackups": [b.to_dict() for b in self.recent_backups],
            "conditions": [c.to_dict() for c in self.conditions],
        }


@dataclass(frozen=True)
class BackupRef:
    name: str
    namespace: str

    @staticmethod
    def from_dict(data: Mapping[str, Any], default_namespace: str) -> "BackupRef":
        return BackupRef(name=data["name"], namespace=data.get("namespace", default_namespace))

    def to_resource_ref(self) -> ResourceRef:
        return ResourceRef(BACKUPS, self.name, self.namespace)


@dataclass(frozen=True)
class RestoreSpec:
    name: str
    namespace: str
    target_cluster_ref: ClusterRef
    backup_ref: BackupRef | None = None
    source_s3: S3StorageSpec | None = None
    collections: tuple[str, ...] = ()
    collection_mapping: Mapping[str, str] = field(default_factory=dict[str, str])
    priority: RestorePriority = RestorePriority.SNAPSHOT
    wait_for_indexing: bool = True

    @staticmethod
    def from_dict(spec: Mapping[str, Any], meta: Mapping[str, Any]) -> "RestoreSpec":
        namespace = meta["namespace"]
        backup_ref = spec.get("backupRef")
        source_s3 = spec.get("source", {}).get("s3")
        if not backup_ref and not source_s3:
            raise ValueError("QdrantRestore needs either spec.backupRef or spec.source.s3")
        return RestoreSpec(
            name=meta["name"],
            namespace=namespace,
            target_cluster_ref=ClusterRef.from_dict(spec["targetClusterRef"], namespace),
            backup_ref=BackupRef.from_dict(backup_ref, namespace) if backup_ref else None,
            source_s3=S3StorageSpec.from_dict(source_s3, namespace) if source_s3 else None,
            collections=tuple(spec.get("collections", [])),
            collection_mapping=dict[str, str](spec.get("collectionMapping", {})),
            priority=RestorePriority(spec.get("priority", RestorePriority.SNAPSHOT)),
            wait_for_indexing=spec.get("waitForIndexing", True),
        )

    def target_name(self, collection: str) -> str:
        return self.collection_mapping.get(collection, collection)

    def select_collections(self, available: Iterable[str]) -> tuple[str, ...]:
        names = tuple(available)
        if not self.collections:
            return names
        missing = [c for c in self.collections if c not in names]
        if missing:
            raise ValueError(f"Collections not present in backup: {', '.join(missing)}")
        return self.collections


@dataclass(frozen=True)
class RestoredCollection:
    name: str
    status: str
    original_name: str | None = None
    size: str | None = None
    points_count: int | None = None
    error: str | None = None

    def to_dict(self) -> JsonDict:
        return {
            "name": self.name,
            "originalName": self.original_name,
            "status": self.status,
            "size": self.size,
            "pointsCount": self.points_count,
            "error": self.error,
        }


@dataclass(frozen=True)
class RestoreProgress:
    collections_total: int = 0
    collections_completed: int = 0
    current_collection: str | None = None

    @property
    def percentage(self) -> int:
        if not self.collections_total:
            return 0
        return round(100 * self.collections_completed / self.collections_total)

    def to_dict(self) -> JsonDict:
        return {
            "collectionsTotal": self.collections_total,
            "collectionsCompleted": self.collections_completed,
            "currentCollection": self.current_collection,
            "percentage": self.percentage,
        }


@dataclass(frozen=True)
class RestoreStatus:
    phase: RestorePhase
    start_time: datetime | None = None
    completion_time: datetime | None = None
    source_backup: str | None = None
    restored_collections: tuple[RestoredCollection, ...] = ()
    progress: RestoreProgress = field(default_factory=RestoreProgress)
    error: str | None = None
    conditions: tuple[Condition, ...] = ()

    def to_dict(self) -> JsonDict:
        return {
            "phase": self.phase.value,
            "startTime": format_time(self.start_time) if self.start_time else None,
            "completionTime": (format_time(self.completion_time) if self.completion_time else None),
            "sourceBackup": self.source_backup,
            "restoredCollections": [c.to_dict() for c in self.restored_collections],
            "progress": self.progress.to_dict(),
            "error": self.error,
            "conditions": [c.to_dict() for c in self.conditions],
        }


class CollectionPhase(StrEnum):
    PENDING = "Pending"
    READY = "Ready"
    DEGRADED = "Degraded"
    FAILED = "Failed"


class DeletionPolicy(StrEnum):
    RETAIN = "Retain"
    DELETE = "Delete"


COLLECTIONS = ResourceKind("QdrantCollection", "qdrantcollections")
UNNAMED_VECTOR = ""


def is_subset(desired: Any, actual: Any) -> bool:
    """True when every value the spec declares is already what Qdrant reports.

    Keys the spec leaves out are Qdrant's business; a False the spec asks for matches an
    unset (null) value, which is how Qdrant reports booleans it never stored.
    """
    if isinstance(desired, Mapping):
        if not isinstance(actual, Mapping):
            return False
        actual_map = cast(Mapping[str, Any], actual)
        return all(
            is_subset(value, actual_map.get(key))
            for key, value in cast(Mapping[str, Any], desired).items()
            if value is not None
        )
    if desired is False and actual is None:
        return True
    return bool(desired == actual)


@dataclass(frozen=True)
class VectorSpec:
    """One dense vector space; `name` is None for Qdrant's single unnamed vector."""

    size: int
    distance: str
    name: str | None = None
    on_disk: bool | None = None
    datatype: str | None = None
    multivector: JsonDict | None = None
    hnsw: JsonDict = field(default_factory=dict[str, Any])
    quantization: JsonDict = field(default_factory=dict[str, Any])

    @staticmethod
    def from_dict(data: Mapping[str, Any]) -> "VectorSpec":
        return VectorSpec(
            size=data["size"],
            distance=data["distance"],
            name=data.get("name"),
            on_disk=data.get("onDisk"),
            datatype=data.get("datatype"),
            multivector=data.get("multivector"),
            hnsw=dict[str, Any](data.get("hnsw", {})),
            quantization=dict[str, Any](data.get("quantization", {})),
        )

    @property
    def key(self) -> str:
        return self.name or UNNAMED_VECTOR

    def immutable_params(self) -> JsonDict:
        return drop_empty(
            {"size": self.size, "distance": self.distance, "datatype": self.datatype}
            | ({"multivector_config": self.multivector} if self.multivector else {})
        )

    def mutable_params(self) -> JsonDict:
        return drop_empty(
            {
                "on_disk": self.on_disk,
                "hnsw_config": self.hnsw or None,
                "quantization_config": self.quantization or None,
            }
        )

    def to_params(self) -> JsonDict:
        return self.immutable_params() | self.mutable_params()


@dataclass(frozen=True)
class SparseVectorSpec:
    name: str
    modifier: str | None = None
    index: JsonDict = field(default_factory=dict[str, Any])

    @staticmethod
    def from_dict(data: Mapping[str, Any]) -> "SparseVectorSpec":
        return SparseVectorSpec(
            name=data["name"],
            modifier=data.get("modifier"),
            index=dict[str, Any](data.get("index", {})),
        )

    def to_params(self) -> JsonDict:
        return drop_empty({"modifier": self.modifier, "index": self.index or None})


@dataclass(frozen=True)
class PayloadIndexSpec:
    field_name: str
    type: str
    params: JsonDict = field(default_factory=dict[str, Any])

    @staticmethod
    def from_dict(data: Mapping[str, Any]) -> "PayloadIndexSpec":
        return PayloadIndexSpec(
            field_name=data["field"],
            type=data["type"],
            params=dict[str, Any](data.get("params", {})),
        )

    def field_schema(self) -> str | JsonDict:
        if not self.params:
            return self.type
        return {"type": self.type, **self.params}

    def satisfied_by(self, actual: Mapping[str, Any]) -> bool:
        """Compare against one entry of Qdrant's `payload_schema` (data_type, params, points)."""
        if actual.get("data_type") != self.type:
            return False
        return is_subset(self.params, actual.get("params") or {})


@dataclass(frozen=True)
class CollectionSpec:
    name: str
    namespace: str
    cluster_ref: ClusterRef
    collection_name: str
    vectors: tuple[VectorSpec, ...] = ()
    sparse_vectors: tuple[SparseVectorSpec, ...] = ()
    shard_number: int | None = None
    sharding_method: str | None = None
    replication_factor: int | None = None
    write_consistency_factor: int | None = None
    read_fan_out_factor: int | None = None
    on_disk_payload: bool | None = None
    hnsw: JsonDict = field(default_factory=dict[str, Any])
    optimizers: JsonDict = field(default_factory=dict[str, Any])
    quantization: JsonDict = field(default_factory=dict[str, Any])
    wal: JsonDict = field(default_factory=dict[str, Any])
    strict_mode: JsonDict = field(default_factory=dict[str, Any])
    payload_indexes: tuple[PayloadIndexSpec, ...] = ()
    aliases: tuple[str, ...] = ()
    deletion_policy: DeletionPolicy = DeletionPolicy.RETAIN

    @staticmethod
    def from_dict(spec: Mapping[str, Any], meta: Mapping[str, Any]) -> "CollectionSpec":
        namespace = meta["namespace"]
        vectors = tuple(VectorSpec.from_dict(v) for v in spec.get("vectors", []))
        sparse = tuple(SparseVectorSpec.from_dict(v) for v in spec.get("sparseVectors", []))
        if not vectors and not sparse:
            raise ValueError("QdrantCollection needs spec.vectors or spec.sparseVectors")
        if len(vectors) > 1 and any(not v.name for v in vectors):
            raise ValueError("An unnamed vector must be the only vector")
        return CollectionSpec(
            name=meta["name"],
            namespace=namespace,
            cluster_ref=ClusterRef.from_dict(spec["clusterRef"], namespace),
            collection_name=spec.get("collectionName") or meta["name"],
            vectors=vectors,
            sparse_vectors=sparse,
            shard_number=spec.get("shardNumber"),
            sharding_method=spec.get("shardingMethod"),
            replication_factor=spec.get("replicationFactor"),
            write_consistency_factor=spec.get("writeConsistencyFactor"),
            read_fan_out_factor=spec.get("readFanOutFactor"),
            on_disk_payload=spec.get("onDiskPayload"),
            hnsw=dict[str, Any](spec.get("hnsw", {})),
            optimizers=dict[str, Any](spec.get("optimizers", {})),
            quantization=dict[str, Any](spec.get("quantization", {})),
            wal=dict[str, Any](spec.get("wal", {})),
            strict_mode=dict[str, Any](spec.get("strictMode", {})),
            payload_indexes=tuple(
                PayloadIndexSpec.from_dict(i) for i in spec.get("payloadIndexes", [])
            ),
            aliases=tuple(spec.get("aliases", [])),
            deletion_policy=DeletionPolicy(spec.get("deletionPolicy", DeletionPolicy.RETAIN)),
        )

    @property
    def single_unnamed(self) -> bool:
        return len(self.vectors) == 1 and not self.vectors[0].name

    def vectors_config(self, params: Callable[[VectorSpec], JsonDict]) -> JsonDict | None:
        """Qdrant VectorsConfig: bare params for the unnamed vector, else a name-to-params map."""
        if not self.vectors:
            return None
        if self.single_unnamed:
            return params(self.vectors[0]) or None
        return {v.key: params(v) for v in self.vectors if params(v)} or None

    def sparse_config(self) -> JsonDict | None:
        if not self.sparse_vectors:
            return None
        return {v.name: v.to_params() for v in self.sparse_vectors}

    def collection_params(self) -> JsonDict:
        return drop_empty(
            {
                "replication_factor": self.replication_factor,
                "write_consistency_factor": self.write_consistency_factor,
                "read_fan_out_factor": self.read_fan_out_factor,
                "on_disk_payload": self.on_disk_payload,
            }
        )

    def create_body(self) -> JsonDict:
        """Body of PUT /collections/{name}."""
        return drop_empty(
            {
                "vectors": self.vectors_config(VectorSpec.to_params),
                "sparse_vectors": self.sparse_config(),
                "shard_number": self.shard_number,
                "sharding_method": self.sharding_method,
                "hnsw_config": self.hnsw or None,
                "optimizers_config": self.optimizers or None,
                "quantization_config": self.quantization or None,
                "wal_config": self.wal or None,
                "strict_mode_config": self.strict_mode or None,
                **self.collection_params(),
            }
        )

    def update_body(self) -> JsonDict:
        """Body of PATCH /collections/{name}: only what Qdrant lets a live collection change."""
        vector_diffs = {v.key: v.mutable_params() for v in self.vectors if v.mutable_params()}
        return drop_empty(
            {
                "vectors": vector_diffs or None,
                "sparse_vectors": self.sparse_config(),
                "params": self.collection_params() or None,
                "hnsw_config": self.hnsw or None,
                "optimizers_config": self.optimizers or None,
                "quantization_config": self.quantization or None,
                "strict_mode_config": self.strict_mode or None,
            }
        )

    def immutable_config(self) -> JsonDict:
        """The part of GET /collections/{name} `config` that only a re-creation can change."""
        return {
            "params": drop_empty(
                {
                    "vectors": self.vectors_config(VectorSpec.immutable_params),
                    "shard_number": self.shard_number,
                    "sharding_method": self.sharding_method,
                }
            )
        }

    def mutable_config(self) -> JsonDict:
        """The part of `config` the operator keeps in line with PATCH; note optimizer_config."""
        return drop_empty(
            {
                "params": drop_empty(
                    {
                        "vectors": self.vectors_config(VectorSpec.mutable_params),
                        "sparse_vectors": self.sparse_config(),
                        **self.collection_params(),
                    }
                )
                or None,
                "hnsw_config": self.hnsw or None,
                "optimizer_config": self.optimizers or None,
                "quantization_config": self.quantization or None,
                "strict_mode_config": self.strict_mode or None,
            }
        )


@dataclass(frozen=True)
class CollectionStatus:
    phase: CollectionPhase
    collection_name: str | None = None
    health: str | None = None
    points_count: int | None = None
    indexed_vectors_count: int | None = None
    segments_count: int | None = None
    payload_indexes: tuple[str, ...] = ()
    aliases: tuple[str, ...] = ()
    error: str | None = None
    observed_generation: int | None = None
    conditions: tuple[Condition, ...] = ()

    @staticmethod
    def from_dict(data: Mapping[str, Any]) -> "CollectionStatus":
        return CollectionStatus(
            phase=CollectionPhase(data.get("phase", CollectionPhase.PENDING)),
            collection_name=data.get("collectionName"),
            health=data.get("health"),
            points_count=data.get("pointsCount"),
            indexed_vectors_count=data.get("indexedVectorsCount"),
            segments_count=data.get("segmentsCount"),
            payload_indexes=tuple(data.get("payloadIndexes", [])),
            aliases=tuple(data.get("aliases", [])),
            error=data.get("error"),
            observed_generation=data.get("observedGeneration"),
            conditions=tuple(conditions_from_dict(data)),
        )

    def to_dict(self) -> JsonDict:
        return {
            "phase": self.phase.value,
            "collectionName": self.collection_name,
            "health": self.health,
            "pointsCount": self.points_count,
            "indexedVectorsCount": self.indexed_vectors_count,
            "segmentsCount": self.segments_count,
            "payloadIndexes": list(self.payload_indexes),
            "aliases": list(self.aliases),
            "error": self.error,
            "observedGeneration": self.observed_generation,
            "conditions": [c.to_dict() for c in self.conditions],
        }
