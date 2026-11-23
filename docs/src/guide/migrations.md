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
   key. Progress is written every `checkpointBatches` batches (default
   twenty, so up to 5120 points at the default batch size are copied again
   after a crash).
5. When the scroll reaches the end, exact point counts of the source and
   target are compared. While the target has fewer points, up to
   `verifyPasses` (default one) further passes scroll the source from the
   start and upsert again, which picks up points written during the copy.
   A collection is `Completed` only when the target holds at least as many
   points as the source (`message` says so when it holds more); otherwise it
   is `Failed` with an error such as `target has 879183 of 1000000 source
   points after 1 verify pass`. `sourceCount` and `targetCount` in the
   collection status carry the exact figures. `pointsTotal` is refreshed from
   the source at every checkpoint.
6. `Completed`, or `Failed` when any collection failed.
   `status.collections` carries each one's counts and error.

With `sync: true`, step 5 is replaced by the [sync pass](#sync-pass).

Connection errors and timeouts are retried with exponential backoff (30s
doubling to ten minutes) and keep the recorded offset; they do not fail the
migration. A `Failed` migration can be re-run, resuming from the offsets in
its status, by changing an annotation:

```sh
kubectl annotate qdrantmigration move qdrant.io/retry="$(date +%s)" --overwrite
```

Upserts are idempotent by point id. If the operator restarts in the middle,
kopf re-runs the handler and points already copied are written again with
the same content, so a migration is safe to repeat and a second migration
with the same spec brings a target up to date.

## Consistency

The copy is not a snapshot. Points written to the source while a collection
is being scrolled may or may not be in the target, which the verify passes
catch up on while the count comparison still shows a gap; a source that keeps
growing faster than a pass copies ends `Failed` with both counts. Updates and
deletes on the source are not picked up unless `sync` is on, and even then
only until the pass has read that ID. Stop writers before
the final run, or run once for the bulk and once more after cutover to pick
up the rest.
`pointsTotal` is Qdrant's approximate count, so `percentage` can end short
of or above the real figure while running.

## Sync pass

Qdrant has no change feed, so updates and deletes made on the source while
the copy runs would otherwise be lost. `sync: true` adds an opt-in pass after
the copy that compares both sides by point ID:

```yaml
spec:
  sync: true
  syncMaxPasses: 3
```

The source and the target are scrolled in ID order with payload and vectors,
one source page of `batchSize` points at a time together with the target
points in the same ID range. A point is upserted again when it is missing on
the target or when its hash differs: the SHA-256 of the canonical JSON (sorted
keys) of the payload plus the vectors, which covers unnamed, named, sparse and
multivectors. Identical points are not written. Target points whose ID no
longer exists on the source are deleted. Collection renames from
`collectionMapping` and custom shard keys are respected, exactly as in the
copy.

The pass runs once; with `syncMaxPasses` above one (up to 20) it runs again
only while the previous pass changed something, so a quiet source ends after
a pass that changes nothing. The pass number and the ID it has reached are
checkpointed every `checkpointBatches` windows, so a restarted operator
resumes the pass instead of starting over; it does not copy again. With
`sync` the verify passes do not run; the exact counts are compared once after
the last sync pass, and a target with fewer points than the source is
`Failed`.

`status.collections[]` reports `pointsUpdated` (points written again,
missing ones included), `pointsDeleted` and `syncPasses`, next to
`sourceCount` and `targetCount`.

Be clear about what this is. It is not live change capture: a write that
lands on the source after the pass has read that ID is only seen by a later
pass, so the target catches up fully only once writes to the source stop.
Stop writers, run the migration, and use `syncMaxPasses` to confirm a pass
finds nothing left. It also re-reads both sides, so its cost is about that
of the copy again for every pass, in time and in load on both clusters.

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
| `collections[]` | Per collection: `name`, `targetName`, `status`, `pointsTotal`, `pointsCopied`, `pointsUpdated`, `pointsDeleted`, `syncPasses`, `error` |
| `progress` | `collectionsTotal`, `collectionsCompleted`, `pointsTotal`, `pointsCopied`, `percentage` |
| `conditions[Complete]` | `MigrationCompleted` or `MigrationFailed`, message `Copied n/m` |
