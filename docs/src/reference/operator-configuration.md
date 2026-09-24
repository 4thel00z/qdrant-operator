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
| `resources` | `100m`/`128Mi` requests, `500m`/`256Mi` limits | Snapshot streaming is I/O bound and buffers one megabyte at a time |
| `operator.logLevel` | `INFO` | loguru level: `DEBUG`, `INFO`, `WARNING`, `ERROR` |
| `operator.logFormat` | `json` | `json` or `text` |

## Environment variables

| Variable | Default | Description |
|---|---|---|
| `LOG_LEVEL` | `INFO` | |
| `LOG_FORMAT` | `json` | `json` writes one JSON object per line; `text` is for a terminal |
| `KUBECONFIG` | in-cluster | Used when no service account token is mounted |
| `HELM_CACHE_HOME`, `HELM_CONFIG_HOME`, `HELM_DATA_HOME` | `/tmp/helm/*` in the chart | Where the bundled `helm` keeps its state on the read-only root filesystem |

## Command line

The `qdrant-operator` entry point runs

```sh
kopf run -m qdrant_operator.handlers --all-namespaces --liveness=http://0.0.0.0:8080/healthz
```

and appends any arguments you pass, which override the defaults. Useful ones:
`--namespace <ns>` to watch one namespace, `--verbose` for kopf's own debug
output, `--standalone` to skip peering, `--dev` for a short peering priority
when running next to an in-cluster operator.

## RBAC

The chart's ClusterRole grants:

| Resources | Verbs | Why |
|---|---|---|
| `qdrant.io` kinds | all | Watch and update the custom resources; the schedule creates and deletes backups |
| `qdrant.io` `*/status` | get, patch, update | Status subresources |
| `customresourcedefinitions` | list, watch | kopf checks the CRDs it serves |
| `clusterkopfpeerings.kopf.dev` | all | kopf peering between operator instances |
| `events` | create | kopf posts events on handled resources |
| `secrets` | get, list, watch, create, update, patch, delete | API keys, TLS CA and bucket credentials; the chart renders Secrets |
| `services`, `namespaces`, `configmaps`, `serviceaccounts`, `statefulsets`, `poddisruptionbudgets`, `ingresses`, `servicemonitors` | all | Everything the qdrant chart may render |

Helm runs inside the operator pod with the pod's service account, so the
operator needs every permission the chart's objects need.

## Timers and retries

| What | Interval |
|---|---|
| `QdrantCluster` readiness check | 30 s, first after 10 s |
| `QdrantCollection` reconcile | 60 s |
| `QdrantBackupSchedule` tick | 60 s |
| `QdrantBackup` expiry check | 300 s, only with `expiresAt` set |
| Retry after a missing cluster, Secret or not-yet-completed backup | 30 s |
| `helm` command timeout | 600 s |
| Qdrant snapshot, recovery, collection and index calls | 3600 s |
| Other Qdrant calls | 30 s |
| Presigned URL lifetime | 3600 s |
| Wait for a restored collection to turn `green` | poll 5 s, give up after 3600 s |

## Annotations and finalizer

kopf keeps its handler progress in annotations prefixed `qdrant.io/` and the
last applied spec under `qdrant.io/last-applied-spec`. The finalizer on every
resource is `qdrant.io/finalizer`. Removing the finalizer by hand skips the
delete handler: the Helm release, the bucket objects or the Qdrant
collection then stay.
