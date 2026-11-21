# Running a cluster

A `QdrantCluster` describes one Helm release of the qdrant chart. This
chapter walks the spec top to bottom; the
[reference](../reference/qdrantcluster.md) has every field with its default,
and [Helm values the operator renders](../reference/helm-values.md) shows
what each field becomes.

## Version and image

```yaml
spec:
  version: v1.16.3
  image:
    repository: docker.io/qdrant/qdrant
    pullPolicy: IfNotPresent
```

`version` is required and pins both the Qdrant image tag and the chart
version: the operator passes it, without the leading `v`, as `--version` to
Helm and lets the chart's `appVersion` pick the image tag. The chart and
Qdrant release in lockstep, so every Qdrant release has a chart of the same
number. `image.repository` points at a mirror; the tag always follows
`version`.

## Replicas and distributed mode

```yaml
spec:
  replicas: 3
  cluster:
    enabled: true
    p2p:
      port: 6335
      enableTls: false
```

`cluster.enabled` defaults to `true`, and the API server rejects
`replicas > 1` with `cluster.enabled: false`, because a multi-pod StatefulSet
of stand-alone nodes would not form a cluster. Scaling up adds pods that join
through the p2p port. By default scaling down only lowers the StatefulSet's
replica count: Qdrant does not move shards off a node by itself, and the
operator flags shards that would be stranded in the `ScaleDownUnsafe`
condition. Set `scaling.drainOnScaleDown: true` to have the operator move
every shard replica off the pods being removed (to the remaining peers with
the fewest replicas), wait for the transfers, remove those peers from
consensus and only then lower the count. A replica that is not `Active` and
has no healthy copy elsewhere blocks the drain (`ScalingDown` condition,
reason `ShardsUnsafe`). Once the count is lowered the operator deletes the
PersistentVolumeClaims of the removed pods: their peers have left the cluster,
and a later scale-up that reused such a volume would start a pod that passes
its readiness probe but belongs to no cluster. Without `drainOnScaleDown` the
claims stay, because the removed pods' peers are still members and may hold
the only copy of a shard.

`scaling.rebalance: true` makes the operator keep shard replicas even once
the StatefulSet is settled: it drops replicas beyond a collection's
replication factor (a restore recovers every node's snapshot and can leave
one replica too many), replicates shards that are short of it, and moves
replicas from the busiest to the idlest peer while their counts differ by more
than one, which spreads shards onto pods added by a scale-up. It issues at
most one operation per collection and per peer at a time and reports progress
in the `Rebalancing` condition.

`scaling.removeDeadPeers: true` handles pods rebuilt on an empty volume: when
every pod is ready and a peer is left over at the address of a pod that
reports another peer id, the operator replicates its shards from Active
replicas up to the collections' replication factor and then force-removes the
dead peer (`DeadPeers` condition). A pod whose volume was lost does not get
that far on its own: Qdrant refuses to join with "peer URI ... already used by
peer N which still has shards" and the pod crash-loops. The operator reads
that error from the pod's previous log and repairs peer N the same way, after
which the pod joins. A peer is never called dead because its pod is merely
unreachable or crashing for another reason.

## Deletion

Deleting a `QdrantCluster` uninstalls the Helm release. The PersistentVolumeClaims
stay unless `persistence.reclaimPolicy: Delete` is set, in which case the
operator deletes every PVC of the release as well.

## Availability

A PodDisruptionBudget is created for clusters with more than one replica
(`podDisruptionBudget.maxUnavailable: 1`; set `enabled`, `maxUnavailable` or
`minAvailable` to change it). Pods are spread over nodes with
`topologySpread.hostname` (`maxSkew: 1`, `ScheduleAnyway`); enable
`topologySpread.zone` for zones, or give `topologySpread.constraints` to
replace the generated list (`[]` turns spreading off). `minReadySeconds` makes
the rolling update wait after a pod turns Ready.

## Persistence

```yaml
spec:
  persistence:
    size: 50Gi
    storageClassName: fast-ssd
    accessModes: [ReadWriteOnce]
  snapshotPersistence:
    enabled: true
    size: 50Gi
```

Each pod gets a PersistentVolumeClaim from the StatefulSet's claim template.
`persistence.size` and `snapshotPersistence.size` can grow but not shrink; the
API server rejects a smaller value. On an increase the operator patches each
PVC, orphan-deletes the StatefulSet (the pods keep running) and lets Helm
recreate it with the new claim template. The storage class must allow volume
expansion, otherwise the PVC patch fails and the reconcile retries.
`storageClassName` stays immutable.

`snapshotPersistence` gives each pod a second volume for the snapshots Qdrant
writes before the operator streams them out. Without it, snapshots are written to
the data volume, so leave headroom equal to your largest collection's shards
on a node, or turn this on. Size it for the snapshot of one node, which is a
full copy of that node's shards: for a 1M point collection of 4096-dimension
float vectors that is more than 10Gi. The default is 10Gi. Switching it on or
off for an existing cluster recreates the StatefulSet the same way a resize
does; pods pick up the new volume when they roll.

## Resources and scheduling

```yaml
spec:
  resources:
    requests: {cpu: "2", memory: 8Gi}
    limits: {cpu: "4", memory: 8Gi}
  nodeSelector:
    workload: vector-search
  tolerations:
    - {key: dedicated, operator: Equal, value: qdrant, effect: NoSchedule}
  affinity:
    podAntiAffinity:
      requiredDuringSchedulingIgnoredDuringExecution:
        - topologyKey: kubernetes.io/hostname
          labelSelector:
            matchLabels: {app.kubernetes.io/name: qdrant}
```

`resources`, `nodeSelector`, `tolerations` and `affinity` pass straight
through to the chart. Qdrant holds its HNSW graphs in memory, so memory
requests should track the indexed vectors; the chart sets no default
requests.

## Service

```yaml
spec:
  service:
    type: LoadBalancer
    annotations:
      service.beta.kubernetes.io/aws-load-balancer-type: nlb
```

The chart creates a Service named `qdrant-<name>` with the HTTP port 6333,
the gRPC port 6334 and, in distributed mode, the p2p port. A headless Service
`qdrant-<name>-headless` gives each pod a stable DNS name; the operator uses
it to reach individual nodes for snapshots. `status.endpoint` holds the
in-cluster HTTP URL of the client Service.

## Metrics

```yaml
spec:
  metrics:
    enabled: true
    serviceMonitor:
      enabled: true
      interval: 15s
      labels: {release: kube-prometheus-stack}
```

Qdrant always serves `/metrics`. What `metrics` controls is the
ServiceMonitor the chart renders for the Prometheus operator: it is created
only when both `metrics.enabled` and `serviceMonitor.enabled` are true, and
the `monitoring.coreos.com` CRDs must exist on the cluster or the Helm
release fails.

## Extra chart configuration

```yaml
spec:
  config:
    storage:
      performance:
        optimizer_cpu_budget: 2
    log_level: INFO
```

`config` is merged into the chart's `config` value, which becomes Qdrant's
`config/production.yaml`. The operator writes `cluster.enabled`, the p2p
settings, `service.enable_tls` and the TLS certificate paths into the same
map first, and your `config` wins on conflicts. Anything Qdrant's
configuration file accepts goes here.

## Status and phases

| Phase | Meaning |
|---|---|
| `Pending` | First Helm install applied, StatefulSet not yet fully ready |
| `Upgrading` | Spec changed on an existing release, rollout in progress |
| `Running` | `readyReplicas` equals `replicas` |

The `Progressing` condition turns `True` with reason `HelmReleaseApplied`
whenever the operator applies the release and `False` with `RolloutComplete`
once the StatefulSet is ready. The `Ready` condition reports
`AllReplicasReady` or `ReplicasNotReady` with the ready count in its message.
The operator re-reads the StatefulSet every thirty seconds.

## Deleting

Deleting a `QdrantCluster` runs `helm uninstall --wait` on its release. The
chart's PersistentVolumeClaims stay behind, as StatefulSet claims do; delete
them yourself once you are sure. Backups referencing the cluster keep their
data in the bucket.
