# Helm values the operator renders

Each `QdrantCluster` becomes one `helm upgrade --install` of the `qdrant`
chart from `https://qdrant.github.io/qdrant-helm`, at chart version
`spec.version` without its leading `v`. The values below are passed on
stdin; everything the spec does not cover keeps the chart's default for that
version.

| Spec field | Chart value |
|---|---|
| `replicas` | `replicaCount` |
| `image.repository`, `image.pullPolicy` | `image.repository`, `image.pullPolicy`. No `image.tag`, so the chart's `appVersion` applies |
| `resources` | `resources` |
| `persistence.size`, `accessModes`, `storageClassName` | `persistence.size`, `persistence.accessModes`, `persistence.storageClassName` |
| `snapshotPersistence` | `snapshotPersistence.enabled`, `.size`, `.storageClassName` |
| `service.type`, `service.annotations` | `service.type`, `service.annotations` |
| `apiKey.autoGenerate` | `apiKey: true` or `false` |
| `apiKey.secretRef` | `apiKey: {valueFrom: {secretKeyRef: {name, key}}}` |
| `readOnlyApiKey` | `readOnlyApiKey`, same encoding |
| `apiKey.jwtRbac` | `config.service.jwt_rbac` |
| `metrics` | `metrics.serviceMonitor.enabled` (true only when both `metrics.enabled` and `serviceMonitor.enabled`), `.scrapeInterval`, `.additionalLabels` |
| `nodeSelector`, `tolerations`, `affinity` | Same names |
| `cluster.enabled` | `config.cluster.enabled` |
| `cluster.p2p.port`, `cluster.p2p.enableTls` | `config.cluster.p2p.port`, `config.cluster.p2p.enable_tls` |
| `tls.enabled` | `config.service.enable_tls`, and `config.tls.cert` / `config.tls.key` set to `/qdrant/tls/tls.crt` and `/qdrant/tls/tls.key` |
| `tls.secretRef.name` | `additionalVolumes: [{name: tls, secret: {secretName}}]` and `additionalVolumeMounts: [{name: tls, mountPath: /qdrant/tls, readOnly: true}]` |
| `config` | Deep-merged over the `config` entries above; the spec wins on conflicts |

## Names the chart produces

For a `QdrantCluster` named `my-qdrant` in namespace `prod`:

| Object | Name |
|---|---|
| Helm release | `qdrant-my-qdrant` |
| StatefulSet | `qdrant-my-qdrant` |
| Pods | `qdrant-my-qdrant-0`, `qdrant-my-qdrant-1`, … |
| Client Service | `qdrant-my-qdrant.prod.svc.cluster.local`, ports 6333 HTTP, 6334 gRPC, 6335 p2p |
| Headless Service | `qdrant-my-qdrant-headless.prod.svc.cluster.local` |
| Pod DNS | `qdrant-my-qdrant-<i>.qdrant-my-qdrant-headless.prod.svc.cluster.local` |
| Generated API key Secret | `qdrant-my-qdrant-apikey`, key `api-key` |
| PersistentVolumeClaims | Created by the StatefulSet from the chart's claim template |

The operator relies on these names when it talks to Qdrant: the client
Service for listing collections and reconciling `QdrantCollection`
resources, the pod names for snapshots and recovery.

## What the operator does not expose

Chart values without a spec field, among them `ingress`, `podDisruptionBudget`,
`sidecarContainers`, `additionalVolumes` beyond the TLS mount, `env`,
`livenessProbe` and friends, `updateStrategy`, `serviceAccount` and
`podSecurityContext`, keep their chart defaults. `config` reaches Qdrant's
configuration file only, not the chart's other values. If you need one of
them, open an issue with the use case.
