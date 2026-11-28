# Operator configuration

## Chart values

| Value | Default | Description |
|---|---|---|
| `replicaCount` | `1` | Above one only with `ha.enabled`; the chart refuses to render otherwise |
| `ha.enabled` | `false` | Leader election: one replica holds a Lease and reconciles, the rest wait as standbys. Switches the Deployment to `RollingUpdate`. See [High availability](../installation.md#high-availability) |
| `ha.leaseName` | release full name | Name of the Lease |
| `ha.leaseDuration` | `15s` | How long a standby waits before taking over from a leader that stopped renewing; a clean shutdown releases the Lease at once |
| `image.repository` | `ghcr.io/4thel00z/qdrant-operator` | |
| `image.tag` | chart `appVersion` | |
| `image.pullPolicy` | `IfNotPresent` | |
| `serviceAccount.create` | `true` | |
| `serviceAccount.name` | release full name | |
| `watchNamespaces` | `[]` | Namespaces to watch. Empty watches all of them under one ClusterRole; otherwise each entry gets a Role and RoleBinding, see [Installation](../installation.md#watching-only-some-namespaces) |
| `resources` | `100m`/`128Mi` requests, `500m`/`1Gi` limits | Backups, restores and migrations stream through the operator and can run at once (S3 upload parts of 8 MiB, two at a time; Qdrant stream chunks of 1 MiB) |
| `operator.logLevel` | `info` | `debug`, `info`, `warn` or `error` |
| `operator.logFormat` | `json` | `json` or `text` |
| `operator.chartRepo` | public qdrant-helm | Chart repository for mirrors and air-gapped installs; `oci://` registries work too. Sets `--helm-repo` |
| `metrics.enabled` | `true` | Prometheus endpoint, plus a `-metrics` Service |
| `metrics.port` | `9090` | |
| `metrics.serviceMonitor.enabled`, `.interval`, `.labels` | `false`, `30s`, `{}` | Optional ServiceMonitor |

## Command line and environment

`qdrant-operator` takes no arguments, only flags. Every flag has an environment
variable: `QDRANT_OPERATOR_` plus the flag name without the leading dashes, in
upper case, with `-` and `.` as `_`. `--manager-lease-duration` is
`QDRANT_OPERATOR_MANAGER_LEASE_DURATION`. Flags take precedence over the
environment. Durations are Go durations such as `30s` or `10m`; list values are
comma-separated.

| Flag | Environment variable | Default | Description |
|---|---|---|---|
| `--logging-level` | `QDRANT_OPERATOR_LOGGING_LEVEL` | `info` | `debug`, `info`, `warn` or `error` |
| `--logging-format` | `QDRANT_OPERATOR_LOGGING_FORMAT` | `json` | `json` writes one JSON object per line; `text` is for a terminal. Other values are rejected |
| `--manager-namespaces` | `QDRANT_OPERATOR_MANAGER_NAMESPACES` | empty | Namespaces to watch; empty watches all. The chart sets it from `watchNamespaces` |
| `--manager-leader-election` | `QDRANT_OPERATOR_MANAGER_LEADER_ELECTION` | `false` | Elect a leader so several replicas can run |
| `--manager-leader-election-id` | `QDRANT_OPERATOR_MANAGER_LEADER_ELECTION_ID` | `qdrant-operator.qdrant.io` | Name of the leader election Lease; the chart sets it from `ha.leaseName` |
| `--manager-lease-duration` | `QDRANT_OPERATOR_MANAGER_LEASE_DURATION` | `15s` | Leader election lease duration |
| `--manager-health-probe-address` | `QDRANT_OPERATOR_MANAGER_HEALTH_PROBE_ADDRESS` | `:8080` | Address of `/healthz` and `/readyz` |
| `--manager-max-concurrent-one-shots` | `QDRANT_OPERATOR_MANAGER_MAX_CONCURRENT_ONE_SHOTS` | `4` | Parallel backups, restores and migrations per kind; at least 1 |
| `--manager-max-concurrent-levels` | `QDRANT_OPERATOR_MANAGER_MAX_CONCURRENT_LEVELS` | `4` | Parallel reconciles per kind for clusters, schedules, collections and access keys; at least 1 |
| `--metrics-address` | `QDRANT_OPERATOR_METRICS_ADDRESS` | `:9090` | Address of the Prometheus endpoint; empty disables it. The chart passes `0` when `metrics.enabled` is false |
| `--kubernetes-kubeconfig` | `QDRANT_OPERATOR_KUBERNETES_KUBECONFIG` | empty | Kubeconfig path; empty uses the in-cluster config |
| `--helm-chart` | `QDRANT_OPERATOR_HELM_CHART` | `qdrant` | Qdrant chart name |
| `--helm-repo` | `QDRANT_OPERATOR_HELM_REPO` | `https://qdrant.github.io/qdrant-helm` | Qdrant chart repository, `https://` or `oci://` |
| `--helm-timeout` | `QDRANT_OPERATOR_HELM_TIMEOUT` | `10m` | Timeout of one Helm install or upgrade; must be positive |
| `--helm-cache-dir` | `QDRANT_OPERATOR_HELM_CACHE_DIR` | `/tmp/helm` | Directory for downloaded charts; the chart sets `/tmp/helm/charts` |
| `--qdrant-timeout` | `QDRANT_OPERATOR_QDRANT_TIMEOUT` | `30s` | Timeout of one Qdrant API call |
| `--qdrant-snapshot-timeout` | `QDRANT_OPERATOR_QDRANT_SNAPSHOT_TIMEOUT` | `1h` | Timeout of one Qdrant snapshot create, upload or recover |

With no kubeconfig flag the operator uses the in-cluster config and falls back
to the default kubeconfig. The chart also sets `HELM_CACHE_HOME`,
`HELM_CONFIG_HOME` and `HELM_DATA_HOME` to `/tmp/helm/*`, where the Helm SDK
keeps its state on the read-only root filesystem.

For development, `make dev` runs `go run ./cmd/operator --logging-format text`
against your current kubeconfig. Scale an in-cluster operator down first, or
both reconcile the same resources.

## RBAC

Without `watchNamespaces` the chart creates one ClusterRole with every rule
below. With it, only the cluster-scoped rules (PersistentVolumes and Nodes) stay in the
ClusterRole and the rest is a Role in each listed namespace. With `ha.enabled`
a separate Role in the release namespace grants the Lease and Event verbs.

| Resources | Verbs | Why |
|---|---|---|
| `persistentvolumes` | get | Zone of each data volume when a `volumeSnapshot` backup is taken; cluster-scoped |
| `nodes` | list | A Ready node in the snapshot's zone when such a backup is restored; cluster-scoped |
| `qdrant.io` kinds | get, list, watch, patch | The operator watches the seven custom resources and sets finalizers; cluster references are read |
| `qdrantbackups` | create, delete | The schedule creates and prunes backups; a backup deletes itself at `expiresAt` |
| `qdrant.io` `*/status` | get, patch | Status subresources |
| `events` (core and `events.k8s.io`) | create, patch | Phase-change events on handled resources |
| `leases.coordination.k8s.io` | get, create, update | Leader election, with `ha.enabled`; Role in the release namespace |
| `secrets` | get, list, create, update, patch, delete | API keys, TLS CA and bucket credentials are read, access key Secrets are written, Helm keeps its `helm.sh/release.v1` release records and the chart renders Secrets |
| `persistentvolumeclaims` | list, patch, delete | Grow and clean up the data and snapshot volumes |
| `persistentvolumeclaims` | create | Pre-create data volumes from VolumeSnapshots on restore |
| `volumesnapshots.snapshot.storage.k8s.io` | create, get, list, delete | `volumeSnapshot` backups |
| `pods` | list | Readiness and shard health before each partition step of a rollout |
| `pods/log` | get | Crash logs for the status |
| `services`, `configmaps`, `serviceaccounts`, `poddisruptionbudgets`, `servicemonitors` | get, create, patch, delete | What the qdrant chart renders; Helm reads, creates, patches and removes them |
| `statefulsets` | get, create, patch, delete | The qdrant chart's StatefulSet, and the orphan-delete that precedes a volume resize |
| `statefulsets/status` | get | Readiness check |

Helm runs inside the operator pod with the pod's service account, so the
operator needs every permission the chart's objects need. The operator does
not create namespaces; a cluster's namespace is the one its resource lives in.

## Intervals and retries

Reconcilers requeue themselves at these intervals (`Timing` in `internal/controller`):

| What | Interval |
|---|---|
| `QdrantCluster` readiness check | 30 s |
| `QdrantCollection` reconcile | 60 s |
| `QdrantAccessKey` renewal check | 60 s |
| `QdrantBackupSchedule` tick | 60 s |
| `QdrantBackup` expiry check | 300 s, only with `expiresAt` set |
| Dead-peer recovery and rebalance, at most once per | 60 s |
| Re-apply of a failed Helm release, at most once per | 60 s |
| Scale-down poll | 15 s |
| Retry after a missing cluster, Secret or not-yet-completed backup | 30 s |
| Helm install or upgrade timeout | `--helm-timeout`, 10 min |
| Qdrant snapshot create, upload and recover | `--qdrant-snapshot-timeout`, 1 h |
| Other Qdrant calls | `--qdrant-timeout`, 30 s |
| Presigned URL lifetime | 3600 s |
| Wait for a restored collection to turn `green` | poll 5 s |

## Finalizer

`QdrantCluster`, `QdrantBackup` and `QdrantCollection` carry the finalizer
`qdrant.io/finalizer`. Removing it by hand skips the cleanup: the Helm release,
the bucket objects or the Qdrant collection then stay. Access key Secrets are
removed by owner reference either way.

## Events

Every phase change is recorded as an `events.k8s.io` Event on the resource:
`Warning` for `Failed`, `Normal` otherwise, with the status error or message as
the note. Read them with `kubectl describe` or `kubectl get events`.

## Metrics

The operator serves Prometheus metrics on `--metrics-address` (`:9090` by default), the controller-runtime metrics
endpoint:

| Metric | Labels | Meaning |
|---|---|---|
| `qdrant_operator_reconcile_errors_total` | `kind` | Reconcile failures |
| `qdrant_operator_operations_total` | `kind`, `outcome` | Finished backups, restores and migrations |
| `qdrant_operator_operation_duration_seconds` | `kind` | Wall time of the same |
| `qdrant_operator_cluster_phase` | `namespace`, `name`, `phase` | `1` for the cluster's current phase |
