<div align="center">

<img src="https://raw.githubusercontent.com/4thel00z/qdrant-operator/main/assets/logo.png" alt="Qdrant Operator logo" width="160">

# Qdrant Operator

**A Kubernetes operator for managing Qdrant vector database clusters**

[![CI](https://github.com/4thel00z/qdrant-operator/actions/workflows/ci.yml/badge.svg)](https://github.com/4thel00z/qdrant-operator/actions/workflows/ci.yml)
[![PyPI](https://img.shields.io/pypi/v/qdrant-operator.svg)](https://pypi.org/project/qdrant-operator/)
[![Python 3.12+](https://img.shields.io/badge/python-3.12+-blue.svg)](https://www.python.org/downloads/)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Kubernetes](https://img.shields.io/badge/kubernetes-%3E%3D1.26-326ce5.svg)](https://kubernetes.io/)

[Documentation](https://4thel00z.github.io/qdrant-operator/) •
[Features](#features) •
[Installation](#installation) •
[Usage](#usage) •
[Configuration](#configuration)

</div>

---

## Features

- **Cluster Management** - Deploy and manage Qdrant clusters via Helm
- **Automated Backups** - Per-node snapshots streamed to S3-compatible storage, with a manifest
- **Scheduled Backups** - CronJob-style schedules with concurrency and retention policies
- **Disaster Recovery** - Restore collections node-by-node from presigned URLs, with optional remapping
- **Declarative Collections** - `QdrantCollection` keeps vectors, payload indexes and aliases in Git and re-creates what was deleted by hand
- **Least-Privilege Tokens** - `QdrantAccessKey` signs Qdrant JWT tokens per collection and writes them into a Secret, renewing before expiry
- **Live Migrations** - `QdrantMigration` copies collections between clusters (or from Qdrant Cloud) without an S3 round trip
- **Admission Validation** - CEL rules on every CRD reject impossible specs before the operator sees them
- **Async-First** - Built on kopf with fully async adapters for performance

## Installation

### Prerequisites

- Kubernetes cluster (v1.26+)
- Helm 3.x installed
- kubectl configured with cluster access

### Install with Helm (Recommended)

```bash
# From the OCI registry (published on every release)
helm install qdrant-operator oci://ghcr.io/4thel00z/charts/qdrant-operator -n qdrant-system --create-namespace

# Or from the local chart
helm install qdrant-operator ./charts/qdrant-operator

# Or install in a specific namespace
helm install qdrant-operator ./charts/qdrant-operator -n qdrant-system --create-namespace

# Verify installation
kubectl get pods -l app.kubernetes.io/name=qdrant-operator
```

#### Upgrading

Helm installs the CRDs in `crds/` once and never updates them. Before `helm upgrade`, apply the
CRDs of the new version (they are attached to every GitHub release):

```bash
kubectl apply -f manifests/crds/
helm upgrade qdrant-operator oci://ghcr.io/4thel00z/charts/qdrant-operator -n qdrant-system
```

### Configuration

Override default values:

```bash
helm install qdrant-operator ./charts/qdrant-operator \
  --set operator.logLevel=DEBUG \
  --set resources.limits.memory=512Mi
```

Or use a values file:

```bash
helm install qdrant-operator ./charts/qdrant-operator -f my-values.yaml
```

### Install from PyPI
The operator is also a Python package; the `qdrant-operator` command runs it against your kubeconfig,
forwarding any extra flags to `kopf run` (for example `--namespace team-a`).
```bash
uv tool install qdrant-operator   # or: pipx install qdrant-operator
qdrant-operator --help
```
Container images: `ghcr.io/4thel00z/qdrant-operator:<version>` (linux/amd64, linux/arm64, signed provenance).
### Build Docker Image (Optional)

```bash
# Replace with your registry
docker build -t ghcr.io/YOUR_ORG/qdrant-operator:0.1.0 .
docker push ghcr.io/YOUR_ORG/qdrant-operator:0.1.0
```

### Local Development

```bash
# Install dependencies
uv sync

# Apply CRDs to cluster
kubectl apply -f manifests/crds/

# Run operator locally against your kubeconfig
make dev
```

## Usage

### Create a Qdrant Cluster

```yaml
apiVersion: qdrant.io/v1alpha1
kind: QdrantCluster
metadata:
  name: my-qdrant
  namespace: default
spec:
  replicas: 3
  version: v1.16.3
  resources:
    requests:
      cpu: "500m"
      memory: "1Gi"
    limits:
      cpu: "2"
      memory: "4Gi"
  persistence:
    size: 10Gi
    storageClassName: standard
  cluster:
    enabled: true
```

### Create a Backup

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
      prefix: qdrant
      region: us-east-1
      credentialsSecretRef:
        name: s3-credentials
        accessKeyIdKey: AWS_ACCESS_KEY_ID
```

### Schedule Automated Backups

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
      prefix: scheduled
      region: us-east-1
      credentialsSecretRef:
        name: s3-credentials
        accessKeyIdKey: AWS_ACCESS_KEY_ID
  retentionPolicy:
    keepLast: 7
    keepDaily: 30
  concurrencyPolicy: Forbid
```

### Restore from Backup

```yaml
apiVersion: qdrant.io/v1alpha1
kind: QdrantRestore
metadata:
  name: my-restore
  namespace: default
spec:
  backupRef:
    name: my-backup
  targetClusterRef:
    name: my-qdrant-new
  collectionMapping:
    old_collection: new_collection
```

### Declare a Collection

```yaml
apiVersion: qdrant.io/v1alpha1
kind: QdrantCollection
metadata:
  name: articles
  namespace: default
spec:
  clusterRef:
    name: my-qdrant
  collectionName: Articles        # optional; Qdrant names may use characters Kubernetes names cannot
  vectors:
    - name: text
      size: 768
      distance: Cosine
      onDisk: true
    - name: image
      size: 512
      distance: Dot
  sparseVectors:
    - name: bm25
      modifier: idf
  replicationFactor: 2
  optimizers:                     # Qdrant's own field names, passed through
    indexing_threshold: 10000
  payloadIndexes:
    - field: tenant_id
      type: keyword
      params: {is_tenant: true}
    - field: body
      type: text
      params: {tokenizer: word, lowercase: true}
  aliases: [articles-live]
  metadata: {owner: search-team}  # merged into the collection's metadata
  deletionPolicy: Retain          # Delete drops the collection with the resource
```

Custom sharding declares its keys in the same resource; keys are created when missing and never
dropped, because dropping one deletes its points:

```yaml
spec:
  shardingMethod: custom
  shardNumber: 1                  # shards per key
  shardKeys:
    - key: eu
    - key: us
      replicationFactor: 2
```

A single entry without `name` is Qdrant's unnamed vector. Vector size and distance, `shardNumber`
and `shardingMethod` cannot change on a live collection; a mismatch puts the resource into
`phase: Degraded` with a `Ready=False` condition instead of silently ignoring the spec. Everything
else is patched in place, and only fields you declared are compared. Indexes and aliases the
operator did not create are left alone.

### Issue a Scoped Token

Requires `spec.apiKey.jwtRbac: true` on the cluster (tokens are signed with the cluster's API key).

```yaml
apiVersion: qdrant.io/v1alpha1
kind: QdrantAccessKey
metadata:
  name: billing-reader
  namespace: default
spec:
  clusterRef:
    name: my-qdrant
  collections:                    # or: access: r | m  for cluster-wide tokens
    - name: invoices
      access: r                   # r, rw, or prw (points only, no snapshots or indexes)
  subject: billing-service
  ttl: 720h
  renewBefore: 24h                # default: a third of ttl
  secretName: billing-qdrant      # default: metadata.name
```

The Secret gets `token` and `url` keys and is garbage-collected with the resource. The token is
re-issued when the spec changes, the cluster's API key rotates, the Secret disappears, or
`renewBefore` is reached.

### Migrate Between Clusters

```yaml
apiVersion: qdrant.io/v1alpha1
kind: QdrantMigration
metadata:
  name: cloud-to-k8s
  namespace: default
spec:
  source:
    endpoint:                     # or: clusterRef: {name: old-qdrant}
      url: https://xyz.eu-central.aws.cloud.qdrant.io:6333
      apiKeySecretRef: {name: qdrant-cloud, key: api-key}
  targetClusterRef:
    name: my-qdrant
  collections: [articles]         # empty = all
  collectionMapping:
    articles: articles_v2
  batchSize: 500
  batchDelayMs: 0                 # throttle
  ensurePayloadIndexes: true      # also on targets that already exist
  target:                         # for collections the migration creates
    replicationFactor: 2
    # shardKey: tier-a            # route every point to one shard key
    # shardKeyField: tenant       # or route by a payload field
```

Points are scrolled from the source and upserted on the target, so the run is idempotent and a
one-node source can become a three-node target (shard counts follow the target unless set under
`target`). Missing target collections are created from the source configuration, including
payload indexes; existing targets must have the same vector sizes and distances. Custom shard
keys are preserved by default and created on the target as they are met, or routed with
`target.shardKey` / `target.shardKeyField`. Progress and the scroll offset live in `status`, so a
run interrupted by an operator restart resumes where it stopped.

## Configuration

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `KUBECONFIG` | Path to kubeconfig file | In-cluster config |
| `LOG_LEVEL` | Logging level | `INFO` |
| `LOG_FORMAT` | `json` or `text` | `json` |

### S3 Credentials Secret

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: s3-credentials
type: Opaque
stringData:
  AWS_ACCESS_KEY_ID: "your-access-key"
  AWS_SECRET_ACCESS_KEY: "your-secret-key"
```

## Development

```bash
# Install dependencies
uv sync

# Run tests
uv run pytest

# Run integration tests (requires k8s cluster + helm)
make test-integration

# Lint and format
uv run ruff check src tests
uv run ruff format src tests

# Type checking
uv run pyright src tests
```

## Releasing
CI (`.github/workflows/ci.yml`) runs lint, pyright, unit tests, chart lint, a wheel/image build and the
kind-based end-to-end suite on every push and pull request.

Releases are driven by [release-please](https://github.com/googleapis/release-please) from the
conventional commit history, all inside `.github/workflows/release.yml` on pushes to `main`:

1. A release PR is kept up to date that bumps `pyproject.toml` and `Chart.yaml`, refreshes `uv.lock`
   and updates `CHANGELOG.md`.
2. Merging that PR creates the `vX.Y.Z` tag and the GitHub release, and the same run publishes the
   wheel and sdist to PyPI (trusted publishing, no token), pushes the multi-arch image and the Helm
   chart to ghcr.io, and attaches the artifacts and CRDs to the release.

No secrets are involved beyond the default `GITHUB_TOKEN`; the repository setting *Allow GitHub
Actions to create and approve pull requests* must be on for the release PR to appear.
## License

MIT License - see [LICENSE](LICENSE) for details.
