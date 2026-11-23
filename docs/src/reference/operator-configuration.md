# Operator configuration

## Chart values

| Value | Default | Description |
|---|---|---|
| `replicaCount` | `1` | Keep at one; the Deployment uses `Recreate` |
| `image.repository` | `ghcr.io/4thel00z/qdrant-operator` | |
| `image.tag` | chart `appVersion` | |
| `image.pullPolicy` | `IfNotPresent` | |
| `serviceAccount.create` | `true` | |
| `serviceAccount.name` | release full name | |
| `watchNamespaces` | `[]` | Namespaces to watch. Empty watches all of them under one ClusterRole; otherwise each entry gets a Role and RoleBinding and kopf runs with `--namespace` per entry, see [Installation](../installation.md#watching-only-some-namespaces) |
| `resources` | `100m`/`128Mi` requests, `500m`/`1Gi` limits | Streaming itself is small (8 MiB upload parts, two at a time, 1 MiB reads); the memory goes to the interpreter and kopf state. A migration loop alone held about 320 Mi and a backup about 280 Mi, so concurrent ones need the headroom |
| `operator.logLevel` | `INFO` | loguru level: `DEBUG`, `INFO`, `WARNING`, `ERROR` |
| `operator.logFormat` | `json` | `json` or `text` |
| `operator.chartRepo` | public qdrant-helm | Chart repository for mirrors and air-gapped installs; `oci://` registries work too |
| `metrics.enabled` | `true` | Prometheus endpoint, plus a `-metrics` Service |
| `metrics.port` | `9090` | |
| `metrics.serviceMonitor.enabled`, `.interval`, `.labels` | `false`, `30s`, `{}` | Optional ServiceMonitor |

## Environment variables

| Variable | Default | Description |
|---|---|---|
| `LOG_LEVEL` | `INFO` | |
| `LOG_FORMAT` | `json` | `json` writes one JSON object per line; `text` is for a terminal |
| `QDRANT_CHART_REPO` | `https://qdrant.github.io/qdrant-helm` | Where the qdrant chart is pulled from |
| `WATCH_NAMESPACES` | unset | Comma-separated namespaces to watch; unset watches all. The chart sets it from `watchNamespaces` |
| `METRICS_PORT` | `9090` | Port of the Prometheus endpoint; `0` turns it off |
| `KUBECONFIG` | in-cluster | Used when no service account token is mounted |
| `HELM_CACHE_HOME`, `HELM_CONFIG_HOME`, `HELM_DATA_HOME` | `/tmp/helm/*` in the chart | Where the bundled `helm` keeps its state on the read-only root filesystem |

## Command line

The `qdrant-operator` entry point runs

```sh
kopf run -m qdrant_operator.handlers --all-namespaces --liveness=http://0.0.0.0:8080/healthz
```

and appends any arguments you pass. With `WATCH_NAMESPACES` set, `--namespace`
per entry replaces `--all-namespaces`. Useful ones: `--namespace <ns>` to watch one namespace, `--verbose` for kopf's own debug
output, `--standalone` to skip peering, `--dev` for a short peering priority
when running next to an in-cluster operator.

## RBAC

Without `watchNamespaces` the chart creates one ClusterRole with every rule
below. With it, only the CustomResourceDefinition rule stays in the
ClusterRole and the rest is a Role in each listed namespace, with `kopfpeerings`
in place of `clusterkopfpeerings`.

| Resources | Verbs | Why |
|---|---|---|
| `customresourcedefinitions` | list, watch | kopf checks the CRDs it serves; cluster-scoped |
| `qdrant.io` kinds | get, list, watch, patch | kopf watches the seven custom resources and sets finalizers and annotations; cluster references are read |
| `qdrantbackups` | create, delete | The schedule creates and prunes backups; a backup deletes itself at `expiresAt` |
| `qdrant.io` `*/status` | get, patch | Status subresources |
| `clusterkopfpeerings.kopf.dev` (or `kopfpeerings`) | get, list, watch, patch | kopf peering between operator instances |
| `events` | create | kopf posts events on handled resources |
| `secrets` | get, list, create, update, patch, delete | API keys, TLS CA and bucket credentials are read, access key Secrets are written, Helm keeps its `helm.sh/release.v1` release records and the chart renders Secrets |
| `persistentvolumeclaims` | list, patch, delete | Grow and clean up the data and snapshot volumes |
| `pods` | list | Readiness and shard health before each partition step of a rollout |
| `pods/log` | get | Crash logs for the status |
| `services`, `configmaps`, `serviceaccounts`, `poddisruptionbudgets`, `servicemonitors` | get, create, patch, delete | What the qdrant chart renders; Helm reads, creates, patches and removes them |
| `statefulsets` | get, create, patch, delete | The qdrant chart's StatefulSet, and the orphan-delete that precedes a volume resize |
| `statefulsets/status` | get | Readiness check |

Helm runs inside the operator pod with the pod's service account, so the
operator needs every permission the chart's objects need. The operator does
not create namespaces; a cluster's namespace is the one its resource lives in.

## Timers and retries

| What | Interval |
|---|---|
| `QdrantCluster` readiness check | 30 s, first after 10 s |
| `QdrantCollection` reconcile | 60 s |
| `QdrantAccessKey` renewal check | 60 s |
| `QdrantBackupSchedule` tick | 60 s |
| `QdrantBackup` expiry check | 300 s, only with `expiresAt` set |
| Retry after a missing cluster, Secret or not-yet-completed backup | 30 s |
| `helm` command timeout | 600 s |
| Qdrant snapshot, recovery, collection, index, scroll and upsert calls | 3600 s |
| Other Qdrant calls | 30 s |
| Presigned URL lifetime | 3600 s |
| Wait for a restored collection to turn `green` | poll 5 s, give up after 3600 s |

## Annotations and finalizer

kopf keeps its handler progress in annotations prefixed `qdrant.io/` and the
last applied spec under `qdrant.io/last-applied-spec`. The finalizer on every
resource is `qdrant.io/finalizer`. Removing the finalizer by hand skips the
delete handler: the Helm release, the bucket objects or the Qdrant
collection then stay. Access key Secrets are removed by owner reference
either way.

## Metrics

The operator serves Prometheus metrics on `METRICS_PORT`:

| Metric | Labels | Meaning |
|---|---|---|
| `qdrant_operator_reconcile_errors_total` | `kind` | Handler failures |
| `qdrant_operator_operations_total` | `kind`, `outcome` | Finished backups, restores and migrations |
| `qdrant_operator_operation_duration_seconds` | `kind` | Wall time of the same |
| `qdrant_operator_cluster_phase` | `namespace`, `name`, `phase` | `1` for the cluster's current phase |

## Metrics

The operator serves Prometheus metrics on `METRICS_PORT`:

| Metric | Labels | Meaning |
|---|---|---|
| `qdrant_operator_reconcile_errors_total` | `kind` | Handler failures |
| `qdrant_operator_operations_total` | `kind`, `outcome` | Finished backups, restores and migrations |
| `qdrant_operator_operation_duration_seconds` | `kind` | Wall time of the same |
| `qdrant_operator_cluster_phase` | `namespace`, `name`, `phase` | `1` for the cluster's current phase |
