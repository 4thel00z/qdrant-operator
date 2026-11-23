# QdrantCluster

`qdrantclusters.qdrant.io`, short name `qc`, namespaced. One Helm release of
the qdrant chart, named `qdrant-<name>`.

## Spec

| Field | Default | Description |
|---|---|---|
| `version` | required | Qdrant version and chart version, `v1.16.3` or `1.16.3` |
| `replicas` | `1` | StatefulSet replicas. More than one requires `cluster.enabled` |
| `image.repository` | `docker.io/qdrant/qdrant` | The tag always follows `version` |
| `image.pullPolicy` | `IfNotPresent` | `Always`, `IfNotPresent` or `Never` |
| `resources.requests`, `resources.limits` | `{}` | `cpu` and `memory` |
| `persistence.size` | `10Gi` | Can grow, not shrink |
| `persistence.storageClassName` | cluster default | Immutable |
| `persistence.accessModes` | `[ReadWriteOnce]` | |
| `persistence.reclaimPolicy` | `Retain` | `Delete` removes the cluster's PVCs (data and snapshots) when the `QdrantCluster` is deleted |
| `snapshotPersistence.enabled` | `false` | Separate volume for snapshots; a change recreates the StatefulSet |
| `snapshotPersistence.size` | `10Gi` | Can grow, not shrink. Size it for one node's full snapshot |
| `snapshotPersistence.storageClassName` | cluster default | |
| `cluster.enabled` | `true` | Distributed mode |
| `cluster.p2p.port` | `6335` | |
| `cluster.p2p.enableTls` | `false` | TLS on the inter-node port, reusing `tls.secretRef` |
| `service.type` | `ClusterIP` | `ClusterIP`, `NodePort` or `LoadBalancer` |
| `service.annotations` | `{}` | |
| `apiKey.secretRef.name`, `apiKey.secretRef.key` | | Bring-your-own full-access key. Exclusive with `autoGenerate` |
| `apiKey.autoGenerate` | `false` | Operator generates the key into `qdrant-<name>-generated-keys` (key `api-key`), owned by the cluster |
| `apiKey.jwtRbac` | `false` | Qdrant's `service.jwt_rbac`; needed by `QdrantAccessKey`. Requires `secretRef` or `autoGenerate` |
| `readOnlyApiKey` | | Same fields as `apiKey`; never used by the operator |
| `tls.enabled` | `false` | Requires `tls.secretRef.name` |
| `tls.secretRef.name` | | `kubernetes.io/tls` Secret |
| `tls.caKey` | `ca.crt` | Key in that Secret holding the CA; verifies the operator's own calls and, with `cluster.p2p.enableTls`, Qdrant's peers |
| `metrics.enabled` | `false` | Together with `serviceMonitor.enabled` renders a ServiceMonitor |
| `metrics.serviceMonitor.enabled` | `false` | |
| `metrics.serviceMonitor.interval` | `30s` | Scrape interval |
| `metrics.serviceMonitor.labels` | `{}` | Added to the ServiceMonitor |
| `nodeSelector`, `tolerations`, `affinity` | | Pod passthroughs |
| `podDisruptionBudget.enabled` | on when `replicas > 1` | |
| `podDisruptionBudget.maxUnavailable` | `1` | Integer or percentage; exclusive with `minAvailable` |
| `podDisruptionBudget.minAvailable` | | Integer or percentage |
| `topologySpread.hostname.enabled` | `true` | Generated only when `replicas > 1`; also `maxSkew` (`1`) and `whenUnsatisfiable` (`ScheduleAnyway`) |
| `topologySpread.zone.enabled` | `false` | Same sub-fields as `hostname` |
| `topologySpread.constraints` | | Replaces the generated constraints; `labelSelector` defaults to the cluster's pods; `[]` disables |
| `minReadySeconds` | `0` | Wait after a pod is Ready before the rolling update continues |
| `rollout.strategy` | `ShardHealth` | `ShardHealth`: the operator restarts one pod at a time and only while every shard replica is Active; `Readiness`: plain StatefulSet RollingUpdate |
| `scaling.drainOnScaleDown` | `false` | Move shards off pods before removing them |
| `scaling.removeDeadPeers` | `false` | Restore replicas and remove peers left by rebuilt pods |
| `scaling.rebalance` | `false` | Bring each collection to its replication factor and spread replicas evenly over the peers |
| `config` | `{}` | Merged over the operator's entries into the chart's `config`, Qdrant's `production.yaml` |

Admission rules: `replicas > 1` needs `cluster.enabled`; `persistence.size`
and `snapshotPersistence.size` cannot shrink and `persistence.storageClassName`
cannot change (the size rules use CEL `quantity()`, Kubernetes 1.29 or newer); `apiKey` and
`readOnlyApiKey` take `secretRef` or `autoGenerate`, not both;
`apiKey.jwtRbac` needs one of them; `tls.enabled` needs `tls.secretRef.name`.

## Status

| Field | Meaning |
|---|---|
| `phase` | `Pending`, `Upgrading` or `Running` |
| `replicas`, `readyReplicas` | Desired count and the StatefulSet's ready count |
| `updatedReplicas`, `currentRevision`, `updateRevision` | Rolling update progress, from the StatefulSet |
| `appliedHash` | Digest of the last applied chart version and values |
| `version` | `spec.version` as last applied |
| `helmRelease` | `qdrant-<name>` |
| `endpoint` | `http(s)://qdrant-<name>.<namespace>.svc.cluster.local:6333` |
| `conditions[Progressing]` | `HelmReleaseApplied` after each apply, `RolloutComplete` once ready |
| `conditions[Ready]` | `AllReplicasReady` or `ReplicasNotReady`, message `n/m replicas ready` |
| `observedGeneration` | Spec generation the last apply reflects |

## Behavior

On create, spec update and operator start: `helm upgrade --install
qdrant-<name> qdrant --repo https://qdrant.github.io/qdrant-helm --version
<version> --namespace <namespace> --values -` with the
values from [Helm values the operator renders](./helm-values.md) on stdin.
The phase is `Pending` on a first install and `Upgrading` when the release
existed. Every thirty seconds the StatefulSet `qdrant-<name>` is read and
`Running` set once `readyReplicas` reaches `replicas`. On delete: `helm
uninstall --wait`; PVCs are left behind.

Print columns: `PHASE`, `REPLICAS`, `READY`, `VERSION`, `AGE`.
