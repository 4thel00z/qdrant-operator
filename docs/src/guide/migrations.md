# Migrations

A `QdrantMigration` copies collections point by point from any reachable
Qdrant into a cluster the operator manages. The source can be another
`QdrantCluster`, a Qdrant Cloud cluster, or a server outside Kubernetes;
only the target has to be managed. Use it to move into the operator, to
change a cluster's node count or sharding, or to clone data into a staging
cluster.

## From Qdrant Cloud

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: qdrant-cloud
stringData:
  api-key: eyJhbGciOi...
---
apiVersion: qdrant.io/v1alpha1
kind: QdrantMigration
metadata:
  name: from-cloud
spec:
  source:
    endpoint:
      url: https://xyz-example.eu-central.aws.cloud.qdrant.io:6333
      apiKeySecretRef: {name: qdrant-cloud, key: api-key}
  targetClusterRef: {name: my-qdrant}
  collections: [documents, images]
  batchSize: 512
```

```
NAME         SOURCE                                          TARGET      PHASE       PROGRESS   AGE
from-cloud   https://xyz-example.eu-central.aws.cloud...     my-qdrant   Running     37         2m
from-cloud   https://xyz-example.eu-central.aws.cloud...     my-qdrant   Completed   100        9m
```

`endpoint.url` is the HTTP URL of the source. `apiKeySecretRef` and
`caSecretRef` name Secrets in the migration's namespace; the CA is a PEM
bundle for a source behind a private certificate authority.

## Between two managed clusters

```yaml
spec:
  source:
    clusterRef: {name: my-qdrant}
  targetClusterRef: {name: my-qdrant-v2}
  collectionMapping:
    documents: documents_v2
  target:
    replicationFactor: 2
```

With `source.clusterRef` the operator resolves the source's Service, API key
and CA the way it does for backups. Set exactly one of `clusterRef` and
`endpoint`. The spec is immutable.

## What happens

1. The source and target are resolved; the target must answer `/readyz` or
   the migration is retried every thirty seconds.
2. `collections` selects the collections, or every collection the source
   lists. Each one's point count is read, approximately, and the phase moves
   to `Running` with a per-collection entry in the status.
3. For each collection, when the target collection does not exist and
   `createMissing` is true, it is created from the source's configuration:
   vectors, sparse vectors, sharding method, payload on disk, HNSW,
   optimizer, WAL, quantization and strict mode settings, plus every payload
   index the source has. Shard number, replication factor and write
   consistency are left to the target cluster's defaults so a copy from one
   node to three gets three shards, unless `target` overrides them.
4. Points are scrolled from the source in pages of `batchSize` with payload
   and vectors, and upserted on the target with `wait=true`, grouped by shard
   key. Progress is written every twenty batches.
5. `Completed`, or `Failed` when any collection failed.
   `status.collections` carries each one's counts and error.

Upserts are idempotent by point id. If the operator restarts in the middle,
kopf re-runs the handler and points already copied are written again with
the same content, so a migration is safe to repeat and a second migration
with the same spec brings a target up to date.

## Consistency

The copy is not a snapshot. Points written to the source while a collection
is being scrolled may or may not be in the target, and points deleted on the
source are never deleted on the target. Stop writers before the final run,
or run once for the bulk and once more after cutover to pick up the rest.
`pointsTotal` is Qdrant's approximate count, so `percentage` can end short
of or above the real figure while running.

## Choosing between migration and restore

| | `QdrantRestore` | `QdrantMigration` |
|---|---|---|
| Source | A backup in a bucket | A live Qdrant |
| Node count | Must match the backup | Any |
| Sharding | Kept as at backup time | Target decides, or `target` overrides |
| Speed | Snapshot files, fast | Point by point over HTTP |
| Consistency | As of the snapshot | Live, see above |

## Status

| Field | Meaning |
|---|---|
| `phase` | `Pending`, `Running`, `Completed` or `Failed` |
| `source` | `<namespace>/<name>` of the source cluster, or the endpoint URL |
| `collections[]` | Per collection: `name`, `targetName`, `status`, `pointsTotal`, `pointsCopied`, `error` |
| `progress` | `collectionsTotal`, `collectionsCompleted`, `pointsTotal`, `pointsCopied`, `percentage` |
| `conditions[Complete]` | `MigrationCompleted` or `MigrationFailed`, message `Copied n/m` |
