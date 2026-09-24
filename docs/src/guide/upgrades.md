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
`tolerations`, `affinity`, `snapshotPersistence`. Changes to `service`,
`metrics` and `replicas` do not restart existing pods.

## What cannot change

`persistence.size` and `persistence.storageClassName` are rejected by the
API server, because a StatefulSet's volume claim template cannot change.
Expand the PVCs in place if the storage class allows it, or take a
[backup](./backups.md) and [restore](./restore.md) into a new cluster.

`replicas > 1` with `cluster.enabled: false` is rejected as well.

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
use the cluster API to move shards or set `replicationFactor` on your
collections so new replicas are created. Lowering `replicas` removes the
highest ordinals; move their shards away first, or the collections holding
them turn yellow or red.

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
