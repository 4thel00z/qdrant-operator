# QdrantRestore

`qdrantrestores.qdrant.io`, short name `qr`, namespaced. Recovery of a
backup into a cluster. The spec is immutable.

## Spec

| Field | Default | Description |
|---|---|---|
| `backupRef.name` | | A `Completed` `QdrantBackup`. Exclusive with `source` |
| `backupRef.namespace` | own namespace | |
| `source.s3.bucket` | | Direct bucket source. Exclusive with `backupRef` |
| `source.s3.path` | | Key prefix that holds `manifest.json` |
| `source.s3.region`, `endpoint`, `forcePathStyle`, `credentialsSecretRef` | as on `QdrantBackup` | Credentials from the restore's namespace |
| `targetClusterRef.name` | required | Cluster to restore into; node count must equal the manifest's |
| `targetClusterRef.namespace` | own namespace | |
| `collections[]` | all in the manifest | Subset to restore |
| `collectionMapping` | `{}` | Source name to target name |
| `priority` | `snapshot` | `snapshot`, `replica` or `no_sync` |
| `waitForIndexing` | `true` | Poll until the collection is `green`, up to one hour |

Admission rules: exactly one of `backupRef` and `source`; the spec cannot
change.

## Status

| Field | Meaning |
|---|---|
| `phase` | `Pending`, `Downloading`, `Restoring`, `Completed` or `Failed` |
| `startTime`, `completionTime` | |
| `sourceBackup` | The backup name, or the `s3://` URI of the source |
| `restoredCollections[]` | Per collection: `name`, `originalName` when remapped, `status`, `size`, `pointsCount`, `error` |
| `progress` | `collectionsTotal`, `collectionsCompleted`, `currentCollection`, `percentage` |
| `error` | The first failed collection's error |
| `conditions[Complete]` | `RestoreCompleted` or `RestoreFailed`, message `Restored n/m` |

## Behavior

On create only. Resolves the source, retrying every thirty seconds while
the backup is not `Completed` or a Secret is missing, reads the manifest,
fails when the node counts differ, then for each selected collection hands
every target node a presigned URL, valid one hour, for the snapshot of the
source node with the same ordinal via
`PUT /collections/<target>/snapshots/recover?wait=true`, and waits for
`green` when asked to. `Completed`, or `Failed` when any collection failed.

Print columns: `SOURCE`, `TARGET`, `PHASE`, `AGE`.
