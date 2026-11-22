# Upgrades and configuration changes

## What a spec change does

Every change to a `QdrantCluster` spec runs `helm upgrade --install` again
with the values derived from the new spec. Helm then does what it always
does: the StatefulSet is patched, and the StatefulSet controller rolls pods
one at a time, highest ordinal first, waiting for each to become ready. The
resource reports `Upgrading` with `Progressing: True` until every replica is
ready again.

Fields that change the pod template roll the pods: `version`, `image`,
`resources`, `config`, `tls`, `apiKey`, `readOnlyApiKey`, `nodeSelector`,
`tolerations`, `affinity`. Changes to `service`,
`metrics` and `replicas` do not restart existing pods.

## What cannot change

`persistence.storageClassName` is rejected by the API server, and so is a
smaller `persistence.size` or `snapshotPersistence.size`. Larger sizes are
applied by the operator (see [persistence](./cluster.md#persistence)). To
change the storage class, take a [backup](./backups.md) and
[restore](./restore.md) into a new cluster.

`replicas > 1` with `cluster.enabled: false` is rejected as well.

Enabling or disabling `snapshotPersistence` changes the StatefulSet's claim
templates; the operator recreates the StatefulSet (pods kept) before the
Helm upgrade.

## Operator restarts

The operator stores a digest of the chart version and rendered values in
`status.appliedHash`. When it restarts it runs the Helm upgrade only for
clusters whose digest changed, so a restart no longer adds a Helm revision to
every cluster or rolls pods. Clusters created before this version have no
digest and get one more upgrade.

With `apiKey.autoGenerate` the operator, not the chart, generates the key: it
writes it into the Secret `qdrant-<name>-generated-keys` and passes it to the
chart by reference. The chart's own generator calls `randAlphaNum` once per
render, so its `checksum/secret` annotation never matched the stored key and
the first upgrade after install rolled every pod. A key the chart already
generated in `qdrant-<name>-apikey` is copied over, so existing clients keep
working; the switch rolls the pods once.

## Rolling progress

`status.updatedReplicas`, `currentRevision` and `updateRevision` mirror the
StatefulSet, the `Ready` condition reports `rolling update N/M updated` and
the phase stays out of `Running` until every pod runs the new revision. Under
write load each pod takes minutes to become Ready; set `minReadySeconds` to
pace the roll and keep client retries on for the UNAVAILABLE errors a stopping
pod produces.

## Rollout strategy

```yaml
spec:
  rollout:
    strategy: ShardHealth   # default; Readiness = plain StatefulSet RollingUpdate
```

A pod only being Ready does not mean its shard replicas have caught up: after
a restart they stay `Partial` or `Recovery` while the other replicas take the
write load. With `ShardHealth` the operator holds the StatefulSet's
`updateStrategy.rollingUpdate.partition` at `spec.replicas`, so a template
change restarts nothing by itself, then lowers it one ordinal at a time. It
releases the next pod (highest ordinal first) only when the previous one is
updated and Ready and the cluster is healthy: every pod Ready and answering,
every shard replica of every collection `Active`, no shard transfers, and
every pod listed as a peer. The `Progressing` condition reads
`WaitingForShards` (naming the replicas as `collection/shard@peer`) or
`RestartingPod`, and the phase is `Upgrading` until the last pod is updated.

The state lives in the StatefulSet (partition and each pod's
`controller-revision-hash`), so an operator restart resumes the rollout. While
it runs, scale-down draining and volume recreation wait for it to finish. A
dead peer left behind by an earlier incident keeps the rollout waiting until
`scaling.removeDeadPeers` removes it, which it may do while the rollout is held.
Choose `Readiness` to get the previous behaviour; it also resets a partition
left by `ShardHealth`. The operator needs `get`, `list` and `watch` on pods,
which the chart's ClusterRole grants.

The partition is part of every Helm apply: the operator runs `helm upgrade` with
a post-renderer (`qdrant-operator-postrender`, shipped in the operator image)
that sets the StatefulSet's `updateStrategy` to the held partition, so a
recreated StatefulSet and a template change never restart a pod before its
turn. Re-applying the release in the middle of a rollout resets the hold, and
the operator releases the pods again one by one.

## Upgrading Qdrant

```sh
kubectl patch qc my-qdrant --type merge -p '{"spec":{"version":"v1.16.4"}}'
```

Take a `QdrantBackup` first. Because `version` is also the chart version,
read the chart's release notes along with Qdrant's: a chart release can
change defaults or rename values that your `config` relies on. Qdrant
supports rolling upgrades between adjacent minor versions in distributed
mode; check the Qdrant release notes before skipping versions or crossing a
major one. Downgrades are not guaranteed.

## Scaling

Raising `replicas` adds pods that join the cluster through the p2p port and
start empty. Qdrant does not rebalance existing shards onto them by itself;
set `scaling.rebalance: true` to have the operator move replicas onto them.
Lowering `replicas` removes the highest ordinals; with
`scaling.drainOnScaleDown: true` the operator moves their shards away first,
otherwise the collections holding them turn yellow or red.

A `QdrantCollection` with `shardNumber` unset lets Qdrant pick the shard
count at creation time from the node count then, and the number never
changes afterwards.

## Upgrading the operator

```sh
kubectl apply -f manifests/crds/
helm upgrade qdrant-operator oci://ghcr.io/4thel00z/charts/qdrant-operator \
  --namespace qdrant-system --version <version>
```

Helm installs CRDs on first install only, so apply them from the new release
before upgrading the chart. The operator's Deployment uses the `Recreate`
strategy: the old pod stops before the new one starts, and handlers that were
in flight, such as a running backup, are re-run by the new pod.

On startup the new operator resumes every resource. For `QdrantCluster` that
means one `helm upgrade --install` per cluster with the values the new
version renders. A version that renders the same values changes nothing; one
that renders different values rolls the pods once.
