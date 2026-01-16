# Qdrant Operator Architecture

## Overview

A Kubernetes operator for managing Qdrant vector database deployments, backups, and restores using the kopf framework. The operator wraps the official qdrant-helm chart and adds backup/restore capabilities with S3-compatible storage.

**Design Principles:**
- DDD/Hexagonal Architecture
- Async-first (all I/O operations are async)
- Flat file structure (prefer files over nested folders)
- `typing.Protocol` for port definitions (no `abc.ABC`)
- No underscore-prefixed methods
- Use cases orchestrate adapters (never call adapters directly from handlers)

## Custom Resource Definitions (CRDs)

### QdrantCluster

Manages Qdrant cluster deployments via Helm.

```yaml
apiVersion: qdrant.io/v1alpha1
kind: QdrantCluster
metadata:
  name: my-qdrant
  namespace: default
spec:
  replicas: 3
  version: "1.13.0"
  resources:
    requests:
      memory: "2Gi"
      cpu: "500m"
    limits:
      memory: "4Gi"
      cpu: "2"
  persistence:
    size: "50Gi"
    storageClassName: "standard"
  cluster:
    enabled: true
  apiKey:
    secretRef:
      name: qdrant-api-key
      key: api-key
  metrics:
    enabled: true
status:
  phase: Running | Pending | Failed
  replicas: 3
  readyReplicas: 3
  version: "1.13.0"
  conditions:
    - type: Ready
      status: "True"
      lastTransitionTime: "2024-01-01T00:00:00Z"
```

### QdrantBackup

Creates a point-in-time backup of a QdrantCluster.

```yaml
apiVersion: qdrant.io/v1alpha1
kind: QdrantBackup
metadata:
  name: my-backup
  namespace: default
spec:
  clusterRef:
    name: my-qdrant
  storage:
    s3:
      bucket: my-backups
      prefix: qdrant/
      region: eu-central-1
      endpoint: ""  # Optional, for MinIO etc.
      credentialsSecretRef:
        name: s3-credentials
        accessKeyId: AWS_ACCESS_KEY_ID
        secretAccessKey: AWS_SECRET_ACCESS_KEY
  collections: []  # Empty = all collections, or list specific ones
  retentionDays: 30
status:
  phase: Completed | InProgress | Failed
  startTime: "2024-01-01T00:00:00Z"
  completionTime: "2024-01-01T00:05:00Z"
  s3Path: "s3://my-backups/qdrant/my-backup-20240101-000000/"
  collections:
    - name: collection1
      snapshotName: "collection1-xxx.snapshot"
      size: "1.2GB"
  error: ""
```

### QdrantBackupSchedule

Schedules periodic backups using cron expressions.

```yaml
apiVersion: qdrant.io/v1alpha1
kind: QdrantBackupSchedule
metadata:
  name: daily-backup
  namespace: default
spec:
  schedule: "0 2 * * *"  # Daily at 2 AM
  clusterRef:
    name: my-qdrant
  storage:
    s3:
      bucket: my-backups
      prefix: qdrant/scheduled/
      region: eu-central-1
      credentialsSecretRef:
        name: s3-credentials
  collections: []
  retentionPolicy:
    keepLast: 7
    keepDaily: 30
    keepWeekly: 12
  suspend: false
status:
  lastBackupTime: "2024-01-01T02:00:00Z"
  lastBackupName: "daily-backup-20240101-020000"
  nextBackupTime: "2024-01-02T02:00:00Z"
  phase: Active | Suspended
```

### QdrantRestore

Restores a QdrantCluster from a backup.

```yaml
apiVersion: qdrant.io/v1alpha1
kind: QdrantRestore
metadata:
  name: my-restore
  namespace: default
spec:
  backupRef:
    name: my-backup
  # OR direct S3 path:
  # source:
  #   s3:
  #     bucket: my-backups
  #     path: qdrant/my-backup-20240101-000000/
  targetClusterRef:
    name: my-qdrant-restored
  collections: []  # Empty = all, or specific ones
status:
  phase: Completed | InProgress | Failed
  startTime: "2024-01-01T00:00:00Z"
  completionTime: "2024-01-01T00:10:00Z"
  restoredCollections:
    - name: collection1
      status: Restored
  error: ""
```

## Hexagonal Architecture

```
┌─────────────────────────────────────────────────────────────────────────┐
│                           INFRASTRUCTURE                                 │
│  ┌─────────────────────────────────────────────────────────────────┐    │
│  │                      Driving Adapters                            │    │
│  │  ┌─────────────┐  ┌─────────────┐  ┌─────────────────────────┐  │    │
│  │  │ kopf create │  │ kopf update │  │ kopf timer (schedules)  │  │    │
│  │  │ handlers    │  │ handlers    │  │                         │  │    │
│  │  └──────┬──────┘  └──────┬──────┘  └────────────┬────────────┘  │    │
│  └─────────┼────────────────┼──────────────────────┼───────────────┘    │
│            │                │                      │                     │
│            ▼                ▼                      ▼                     │
│  ┌─────────────────────────────────────────────────────────────────┐    │
│  │                         PORTS (In)                               │    │
│  │              typing.Protocol interfaces                          │    │
│  └─────────────────────────────────────────────────────────────────┘    │
│                                  │                                       │
│                                  ▼                                       │
│  ┌─────────────────────────────────────────────────────────────────┐    │
│  │                        APPLICATION                               │    │
│  │  ┌──────────────────────────────────────────────────────────┐   │    │
│  │  │                      Use Cases                            │   │    │
│  │  │  ┌────────────────┐  ┌────────────────┐                  │   │    │
│  │  │  │ deploy_cluster │  │ create_backup  │                  │   │    │
│  │  │  │ update_cluster │  │ delete_backup  │                  │   │    │
│  │  │  │ delete_cluster │  │ schedule_check │                  │   │    │
│  │  │  └────────────────┘  └────────────────┘                  │   │    │
│  │  │  ┌────────────────┐  ┌────────────────┐                  │   │    │
│  │  │  │ restore_backup │  │ apply_retention│                  │   │    │
│  │  │  └────────────────┘  └────────────────┘                  │   │    │
│  │  └──────────────────────────────────────────────────────────┘   │    │
│  │                                                                  │    │
│  │  ┌──────────────────────────────────────────────────────────┐   │    │
│  │  │                       Domain                              │   │    │
│  │  │  ┌─────────────┐  ┌─────────────┐  ┌─────────────────┐   │   │    │
│  │  │  │ Cluster     │  │ Backup      │  │ BackupSchedule  │   │   │    │
│  │  │  │ (entity)    │  │ (entity)    │  │ (entity)        │   │   │    │
│  │  │  └─────────────┘  └─────────────┘  └─────────────────┘   │   │    │
│  │  │  ┌─────────────┐  ┌─────────────┐                        │   │    │
│  │  │  │ Restore     │  │ Snapshot    │                        │   │    │
│  │  │  │ (entity)    │  │ (value obj) │                        │   │    │
│  │  │  └─────────────┘  └─────────────┘                        │   │    │
│  │  └──────────────────────────────────────────────────────────┘   │    │
│  └─────────────────────────────────────────────────────────────────┘    │
│                                  │                                       │
│                                  ▼                                       │
│  ┌─────────────────────────────────────────────────────────────────┐    │
│  │                        PORTS (Out)                               │    │
│  │              typing.Protocol interfaces                          │    │
│  │  ┌────────────────┐  ┌────────────────┐  ┌────────────────────┐ │    │
│  │  │ HelmPort       │  │ QdrantPort     │  │ StoragePort        │ │    │
│  │  │ KubernetesPort │  │ SchedulerPort  │  │                    │ │    │
│  │  └────────────────┘  └────────────────┘  └────────────────────┘ │    │
│  └─────────────────────────────────────────────────────────────────┘    │
│                                  │                                       │
│                                  ▼                                       │
│  ┌─────────────────────────────────────────────────────────────────┐    │
│  │                      Driven Adapters                             │    │
│  │  ┌────────────┐  ┌────────────┐  ┌────────────┐  ┌───────────┐  │    │
│  │  │ HelmAdapter│  │QdrantAdapter│ │ S3Adapter  │  │K8sAdapter │  │    │
│  │  │ (helm CLI) │  │ (httpx)    │  │ (aioboto3) │  │(kubernetes)│ │    │
│  │  └────────────┘  └────────────┘  └────────────┘  └───────────┘  │    │
│  └─────────────────────────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────────────────────┘
```

## Project Structure (Flat)

```
qdrant-operator/
├── pyproject.toml
├── ARCHITECTURE.md
├── CLAUDE.md
├── README.md
│
├── src/qdrant_operator/
│   ├── __init__.py
│   ├── main.py                    # Entry point, kopf.run()
│   │
│   │  # Domain Layer (entities, value objects)
│   ├── domain.py                  # All domain entities in one file
│   │
│   │  # Ports (interfaces as typing.Protocol)
│   ├── ports.py                   # All port protocols in one file
│   │
│   │  # Application Layer (use cases)
│   ├── usecases.py                # All use cases in one file
│   │
│   │  # Infrastructure - Driven Adapters
│   ├── helm_adapter.py            # Helm CLI adapter
│   ├── qdrant_adapter.py          # Qdrant REST API adapter (httpx)
│   ├── s3_adapter.py              # S3 adapter (aioboto3)
│   ├── kubernetes_adapter.py      # Kubernetes API adapter
│   │
│   │  # Infrastructure - Driving Adapters (kopf handlers)
│   ├── handlers.py                # All kopf handlers in one file
│   │
│   │  # Dependency injection / wiring
│   └── container.py               # Wires adapters to use cases
│
├── manifests/
│   ├── crds/
│   │   ├── qdrantcluster-crd.yaml
│   │   ├── qdrantbackup-crd.yaml
│   │   ├── qdrantbackupschedule-crd.yaml
│   │   └── qdrantrestore-crd.yaml
│   └── operator/
│       ├── deployment.yaml
│       ├── rbac.yaml
│       └── serviceaccount.yaml
│
└── tests/
    ├── conftest.py
    ├── test_domain.py
    ├── test_usecases.py
    └── test_adapters.py
```

## Layer Details

### Domain Layer (`domain.py`)

Pure Python dataclasses representing business entities. No dependencies on infrastructure.

```python
from dataclasses import dataclass
from datetime import datetime
from enum import Enum

class ClusterPhase(Enum):
    PENDING = "Pending"
    RUNNING = "Running"
    FAILED = "Failed"
    UPGRADING = "Upgrading"
    TERMINATING = "Terminating"

@dataclass(frozen=True)
class ClusterSpec:
    name: str
    namespace: str
    replicas: int
    version: str
    storage_size: str
    storage_class: str | None
    cluster_enabled: bool
    # ...

@dataclass
class ClusterStatus:
    phase: ClusterPhase
    replicas: int
    ready_replicas: int
    helm_release: str | None
    endpoint: str | None
    # ...

@dataclass(frozen=True)
class Snapshot:
    name: str
    collection: str
    size_bytes: int
    created_at: datetime
```

### Ports Layer (`ports.py`)

All interfaces defined as `typing.Protocol`. Async methods only.

```python
from typing import Protocol

class HelmPort(Protocol):
    async def install(
        self,
        release_name: str,
        namespace: str,
        chart: str,
        values: dict,
    ) -> None: ...

    async def upgrade(
        self,
        release_name: str,
        namespace: str,
        chart: str,
        values: dict,
    ) -> None: ...

    async def uninstall(self, release_name: str, namespace: str) -> None: ...

    async def get_release_status(
        self,
        release_name: str,
        namespace: str,
    ) -> str | None: ...


class QdrantPort(Protocol):
    async def list_collections(self, endpoint: str, api_key: str | None) -> list[str]: ...

    async def create_snapshot(
        self,
        endpoint: str,
        collection: str,
        api_key: str | None,
    ) -> Snapshot: ...

    async def download_snapshot(
        self,
        endpoint: str,
        collection: str,
        snapshot_name: str,
        api_key: str | None,
    ) -> bytes: ...

    async def delete_snapshot(
        self,
        endpoint: str,
        collection: str,
        snapshot_name: str,
        api_key: str | None,
    ) -> None: ...

    async def recover_from_snapshot(
        self,
        endpoint: str,
        collection: str,
        snapshot_path: str,
        api_key: str | None,
    ) -> None: ...


class StoragePort(Protocol):
    async def upload(self, bucket: str, key: str, data: bytes) -> str: ...
    async def download(self, bucket: str, key: str) -> bytes: ...
    async def delete(self, bucket: str, key: str) -> None: ...
    async def list_keys(self, bucket: str, prefix: str) -> list[str]: ...


class KubernetesPort(Protocol):
    async def get_secret_value(
        self,
        name: str,
        namespace: str,
        key: str,
    ) -> str: ...

    async def update_status(
        self,
        group: str,
        version: str,
        plural: str,
        name: str,
        namespace: str,
        status: dict,
    ) -> None: ...

    async def create_resource(
        self,
        group: str,
        version: str,
        plural: str,
        namespace: str,
        body: dict,
    ) -> None: ...

    async def delete_resource(
        self,
        group: str,
        version: str,
        plural: str,
        name: str,
        namespace: str,
    ) -> None: ...
```

### Use Cases Layer (`usecases.py`)

Orchestrates domain logic and ports. Never calls adapters directly - only through port interfaces.

```python
@dataclass
class DeployClusterUseCase:
    helm: HelmPort
    kubernetes: KubernetesPort

    async def execute(self, spec: ClusterSpec) -> ClusterStatus:
        release_name = f"{spec.namespace}-{spec.name}"
        values = build_helm_values(spec)

        await self.helm.install(
            release_name=release_name,
            namespace=spec.namespace,
            chart="qdrant/qdrant",
            values=values,
        )

        return ClusterStatus(
            phase=ClusterPhase.PENDING,
            replicas=spec.replicas,
            ready_replicas=0,
            helm_release=release_name,
            endpoint=f"{spec.name}.{spec.namespace}.svc:6333",
        )


@dataclass
class CreateBackupUseCase:
    qdrant: QdrantPort
    storage: StoragePort
    kubernetes: KubernetesPort

    async def execute(self, backup_spec: BackupSpec) -> BackupStatus:
        # Get API key from secret
        api_key = await self.kubernetes.get_secret_value(...)

        # Get collections to backup
        collections = backup_spec.collections or await self.qdrant.list_collections(...)

        snapshots = []
        for collection in collections:
            # Create snapshot
            snapshot = await self.qdrant.create_snapshot(...)

            # Download snapshot data
            data = await self.qdrant.download_snapshot(...)

            # Upload to S3
            s3_key = f"{backup_spec.prefix}{collection}/{snapshot.name}"
            await self.storage.upload(backup_spec.bucket, s3_key, data)

            # Clean up snapshot from Qdrant
            await self.qdrant.delete_snapshot(...)

            snapshots.append(snapshot)

        return BackupStatus(phase=BackupPhase.COMPLETED, ...)
```

### Handlers Layer (`handlers.py`)

Kopf handlers are thin adapters that delegate to use cases.

```python
import kopf
from container import get_container

@kopf.on.create("qdrant.io", "v1alpha1", "qdrantclusters")
async def on_cluster_create(spec: dict, meta: dict, **kwargs) -> dict:
    container = get_container()
    cluster_spec = ClusterSpec.from_dict(spec, meta)

    status = await container.deploy_cluster.execute(cluster_spec)

    return status.to_dict()


@kopf.on.delete("qdrant.io", "v1alpha1", "qdrantclusters")
async def on_cluster_delete(spec: dict, meta: dict, **kwargs) -> None:
    container = get_container()
    cluster_spec = ClusterSpec.from_dict(spec, meta)

    await container.delete_cluster.execute(cluster_spec)


@kopf.timer("qdrant.io", "v1alpha1", "qdrantbackupschedules", interval=60.0)
async def on_schedule_timer(spec: dict, meta: dict, status: dict, **kwargs) -> dict:
    container = get_container()
    schedule_spec = BackupScheduleSpec.from_dict(spec, meta)
    current_status = BackupScheduleStatus.from_dict(status)

    new_status = await container.check_schedule.execute(schedule_spec, current_status)

    return new_status.to_dict()
```

### Container (`container.py`)

Dependency injection - wires adapters to use cases.

```python
from dataclasses import dataclass

@dataclass
class Container:
    deploy_cluster: DeployClusterUseCase
    update_cluster: UpdateClusterUseCase
    delete_cluster: DeleteClusterUseCase
    create_backup: CreateBackupUseCase
    delete_backup: DeleteBackupUseCase
    check_schedule: CheckScheduleUseCase
    restore_backup: RestoreBackupUseCase
    apply_retention: ApplyRetentionUseCase


def create_container() -> Container:
    # Create adapters
    helm = HelmAdapter()
    qdrant = QdrantAdapter()
    s3 = S3Adapter()
    kubernetes = KubernetesAdapter()

    # Wire use cases
    return Container(
        deploy_cluster=DeployClusterUseCase(helm=helm, kubernetes=kubernetes),
        update_cluster=UpdateClusterUseCase(helm=helm, kubernetes=kubernetes),
        delete_cluster=DeleteClusterUseCase(helm=helm, kubernetes=kubernetes),
        create_backup=CreateBackupUseCase(qdrant=qdrant, storage=s3, kubernetes=kubernetes),
        delete_backup=DeleteBackupUseCase(storage=s3),
        check_schedule=CheckScheduleUseCase(kubernetes=kubernetes),
        restore_backup=RestoreBackupUseCase(qdrant=qdrant, storage=s3, kubernetes=kubernetes),
        apply_retention=ApplyRetentionUseCase(storage=s3, kubernetes=kubernetes),
    )


_container: Container | None = None

def get_container() -> Container:
    global _container
    if _container is None:
        _container = create_container()
    return _container
```

## Async Patterns (kopf)

All handlers and adapters are async. Key rules:

1. **Never use blocking calls** - no `time.sleep()`, no sync HTTP requests
2. **Use `await` for all I/O** - httpx, aioboto3, kubernetes-asyncio
3. **Async handlers get full stack traces** - better debugging than sync handlers

```python
# GOOD - async adapter
class QdrantAdapter:
    def __init__(self) -> None:
        self.client = httpx.AsyncClient()

    async def create_snapshot(self, endpoint: str, collection: str, api_key: str | None) -> Snapshot:
        headers = {"api-key": api_key} if api_key else {}
        response = await self.client.post(
            f"{endpoint}/collections/{collection}/snapshots",
            headers=headers,
        )
        response.raise_for_status()
        return Snapshot.from_response(response.json())

# BAD - blocking call in async context
class QdrantAdapter:
    async def create_snapshot(self, ...):
        response = requests.post(...)  # BLOCKS THE EVENT LOOP!
```

## Dependencies

| Package | Purpose |
|---------|---------|
| kopf | Kubernetes operator framework |
| kubernetes-asyncio | Async K8s client |
| httpx | Async HTTP client for Qdrant REST API |
| pydantic | CRD spec/status validation |
| aioboto3 | Async S3 client |
| croniter | Cron expression parsing |
| structlog | Structured logging |

## Workflows

### Cluster Creation

```
Handler (on_cluster_create)
    │
    ▼
DeployClusterUseCase.execute()
    │
    ├── HelmPort.install()  ──────────▶  HelmAdapter  ──▶  helm CLI
    │
    └── return ClusterStatus(phase=PENDING)
```

### Backup Workflow

```
Handler (on_backup_create)
    │
    ▼
CreateBackupUseCase.execute()
    │
    ├── KubernetesPort.get_secret_value()  ──▶  K8sAdapter
    │
    ├── QdrantPort.list_collections()  ──────▶  QdrantAdapter  ──▶  Qdrant API
    │
    └── For each collection:
        ├── QdrantPort.create_snapshot()
        ├── QdrantPort.download_snapshot()
        ├── StoragePort.upload()  ───────────▶  S3Adapter  ──▶  S3/MinIO
        └── QdrantPort.delete_snapshot()
```

### Scheduled Backup

```
Timer (every 60s on BackupSchedule resources)
    │
    ▼
CheckScheduleUseCase.execute()
    │
    ├── Check if backup is due (croniter)
    │
    ├── If due: KubernetesPort.create_resource()  ──▶  Create QdrantBackup CR
    │
    └── ApplyRetentionUseCase.execute()  ──▶  Delete old backups per policy
```

## Status Conditions

All CRDs use standard Kubernetes conditions:

| Type | Description |
|------|-------------|
| Ready | Resource is fully operational |
| Progressing | Resource is being created/updated |
| Degraded | Resource is partially operational |
| Failed | Resource has failed |

## RBAC Requirements

The operator ServiceAccount needs:
- `get`, `list`, `watch`, `create`, `update`, `patch`, `delete` on all CRDs
- `get`, `list`, `watch` on Secrets (for credentials)
- `get`, `list`, `watch`, `create`, `update`, `delete` on StatefulSets, Services, ConfigMaps
- `get`, `list`, `watch` on Pods (for status)