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
| `persistence.size` | `10Gi` | Immutable |
| `persistence.storageClassName` | cluster default | Immutable |
| `persistence.accessModes` | `[ReadWriteOnce]` | |
| `snapshotPersistence.enabled` | `false` | Separate volume for snapshots |
| `snapshotPersistence.size` | `10Gi` | |
| `snapshotPersistence.storageClassName` | cluster default | |
| `cluster.enabled` | `true` | Distributed mode |
| `cluster.p2p.port` | `6335` | |
| `cluster.p2p.enableTls` | `false` | TLS on the inter-node port, reusing `tls.secretRef` |
| `service.type` | `ClusterIP` | `ClusterIP`, `NodePort` or `LoadBalancer` |
| `service.annotations` | `{}` | |
| `apiKey.secretRef.name`, `apiKey.secretRef.key` | | Bring-your-own full-access key. Exclusive with `autoGenerate` |
| `apiKey.autoGenerate` | `false` | Chart generates `qdrant-<name>-apikey` with key `api-key` |
| `apiKey.jwtRbac` | `false` | Qdrant's `service.jwt_rbac`; needed by `QdrantAccessKey`. Requires `secretRef` or `autoGenerate` |
| `readOnlyApiKey` | | Same fields as `apiKey`; never used by the operator |
| `tls.enabled` | `false` | Requires `tls.secretRef.name` |
| `tls.secretRef.name` | | `kubernetes.io/tls` Secret; `ca.crt` in it verifies the operator's own calls |
| `metrics.enabled` | `false` | Together with `serviceMonitor.enabled` renders a ServiceMonitor |
| `metrics.serviceMonitor.enabled` | `false` | |
| `metrics.serviceMonitor.interval` | `30s` | Scrape interval |
| `metrics.serviceMonitor.labels` | `{}` | Added to the ServiceMonitor |
| `nodeSelector`, `tolerations`, `affinity` | | Pod passthroughs |
| `config` | `{}` | Merged over the operator's entries into the chart's `config`, Qdrant's `production.yaml` |

Admission rules: `replicas > 1` needs `cluster.enabled`; `persistence.size`
and `persistence.storageClassName` cannot change; `apiKey` and
`readOnlyApiKey` take `secretRef` or `autoGenerate`, not both;
`apiKey.jwtRbac` needs one of them; `tls.enabled` needs `tls.secretRef.name`.

## Status

| Field | Meaning |
|---|---|
| `phase` | `Pending`, `Upgrading` or `Running` |
| `replicas`, `readyReplicas` | Desired count and the StatefulSet's ready count |
| `version` | `spec.version` as last applied |
| `helmRelease` | `qdrant-<name>` |
| `endpoint` | `http(s)://qdrant-<name>.<namespace>.svc.cluster.local:6333` |
| `conditions[Progressing]` | `HelmReleaseApplied` after each apply, `RolloutComplete` once ready |
| `conditions[Ready]` | `AllReplicasReady` or `ReplicasNotReady`, message `n/m replicas ready` |
| `observedGeneration` | Spec generation the last apply reflects |

## Behavior

On create, spec update and operator start: `helm upgrade --install
qdrant-<name> qdrant --repo https://qdrant.github.io/qdrant-helm --version
<version> --namespace <namespace> --create-namespace --values -` with the
values from [Helm values the operator renders](./helm-values.md) on stdin.
The phase is `Pending` on a first install and `Upgrading` when the release
existed. Every thirty seconds the StatefulSet `qdrant-<name>` is read and
`Running` set once `readyReplicas` reaches `replicas`. On delete: `helm
uninstall --wait`; PVCs are left behind.

Print columns: `PHASE`, `REPLICAS`, `READY`, `VERSION`, `AGE`.
