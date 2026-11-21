# QdrantMigration

`qdrantmigrations.qdrant.io`, short name `qmig`, namespaced. A point-by-point
copy of collections from any Qdrant into a managed cluster. The spec is
immutable.

## Spec

| Field | Default | Description |
|---|---|---|
| `source.clusterRef.name` | | A `QdrantCluster`. Exclusive with `endpoint` |
| `source.clusterRef.namespace` | own namespace | |
| `source.endpoint.url` | | `http://` or `https://` URL of any Qdrant. Exclusive with `clusterRef` |
| `source.endpoint.apiKeySecretRef.name`, `.key` | none | API key for the source, Secret in the migration's namespace |
| `source.endpoint.caSecretRef.name`, `.key` | system trust | PEM bundle for a private CA |
| `targetClusterRef.name` | required | The managed cluster to copy into |
| `targetClusterRef.namespace` | own namespace | |
| `collections[]` | all on the source | Collections to copy |
| `collectionMapping` | `{}` | Source name to target name |
| `batchSize` | `256` | Points per scroll and upsert, 1 to 10000 |
| `checkpointBatches` | `20` | Batches between progress checkpoints in status; a crash re-copies at most this many batches |
| `verifyPasses` | `1` | Extra scroll passes over the source when, after the copy, the target has fewer points than the source (exact counts); `0` disables them but the counts are still compared |
| `createMissing` | `true` | Create absent target collections from the source configuration |
| `target.shardNumber`, `replicationFactor`, `writeConsistencyFactor` | target defaults | Applied to collections the migration creates |

Admission rules: exactly one of `source.clusterRef` and `source.endpoint`;
the spec cannot change.

## Status

| Field | Meaning |
|---|---|
| `phase` | `Pending`, `Running`, `Completed` or `Failed` |
| `startTime`, `completionTime` | |
| `source` | `<namespace>/<name>` or the endpoint URL |
| `collections[]` | `name`, `targetName`, `status`, `pointsTotal`, `pointsCopied`, `sourceCount`, `targetCount`, `verifyPass`, `message`, `error` |
| `progress` | `collectionsTotal`, `collectionsCompleted`, `pointsTotal`, `pointsCopied`, `percentage` |
| `error` | The first failed collection's error |
| `conditions[Complete]` | `MigrationCompleted` or `MigrationFailed` |

## Behavior

On create only. Resolves both sides, retrying every thirty seconds while
the target cluster is missing or not ready or a Secret is absent, counts the
points of each selected collection, then per collection creates the target
collection and its payload indexes when missing and allowed, scrolls pages
with payload and vectors, and upserts them grouped by shard key with
`wait=true`. Progress is patched every twenty batches. `Completed`, or
`Failed` when any collection failed.

Print columns: `SOURCE`, `TARGET`, `PHASE`, `PROGRESS`, `AGE`.
