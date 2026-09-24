# Restore

A `QdrantRestore` recovers the collections of a backup into a cluster. Each
target node receives the snapshot that the source node with the same ordinal
produced, through Qdrant's snapshot recovery API and a presigned URL, so the
data flows from the bucket to the node without passing through the operator.

## From a backup resource

```yaml
apiVersion: qdrant.io/v1alpha1
kind: QdrantRestore
metadata:
  name: restore-documents
spec:
  backupRef: {name: nightly-20260924-020000}
  targetClusterRef: {name: my-qdrant-new}
  collections: [documents]
  collectionMapping:
    documents: documents_restored
  priority: snapshot
  waitForIndexing: true
```

`backupRef` must name a `QdrantBackup` whose phase is `Completed`. A backup
still running is retried every thirty seconds. The restore reads the
backup's storage settings and root path, and the bucket credentials from the
backup's namespace.

## From a bucket path

```yaml
spec:
  source:
    s3:
      bucket: qdrant-backups
      path: prod/my-qdrant/nightly/nightly-20260924-020000
      endpoint: http://minio.minio.svc:9000
      forcePathStyle: true
      credentialsSecretRef: {name: s3-credentials}
  targetClusterRef: {name: my-qdrant}
```

`source.s3.path` is the key prefix that holds `manifest.json`. This is the
way to restore into another Kubernetes cluster, or after the `QdrantBackup`
resource is gone. Set exactly one of `backupRef` and `source`; the API
server rejects both or neither. The spec is immutable.

## Node counts must match

The manifest records how many nodes the source cluster had. The target
cluster must have the same number of replicas, or the restore fails before
touching any collection with a message naming both counts. Shards are placed
per node, and there is no re-sharding on the way in. To restore into a
cluster of a different size, create a cluster of the original size, restore,
then let Qdrant move shards.

## What happens

1. The phase moves to `Downloading` while the manifest is read.
2. `collections` selects a subset; a name missing from the manifest fails the
   restore. Without `collections`, every collection in the manifest is
   restored.
3. For each collection, phase `Restoring` with `progress` updated first, then
   for each snapshot in the manifest: a presigned `GET` URL valid for one
   hour, and `PUT /collections/<target>/snapshots/recover?wait=true` on the
   node with the same ordinal, passing the URL, `priority` and the checksum.
4. With `waitForIndexing`, the collection is polled every five seconds until
   its status is `green`, for up to one hour. Without it, the restore
   records the point count and moves on.
5. `Completed`, or `Failed` when any collection failed.
   `status.restoredCollections` lists each collection with its original name
   when remapped, size, point count and error.

Recovering a snapshot into an existing collection replaces it on that node.
`collectionMapping` renames on the way in, so a restore next to the live
collection is possible.

## Priority

| `priority` | Meaning |
|---|---|
| `snapshot` (default) | The snapshot wins; existing data for the collection on that node is replaced |
| `replica` | Existing replicas win; the snapshot fills in what is missing |
| `no_sync` | Load the snapshot and skip synchronisation with other replicas |

These are Qdrant's `SnapshotPriority` values, passed through unchanged.

## After the restore

Collections come back with the configuration they had at backup time. A
`QdrantCollection` resource for the same collection will, on its next
reconcile, compare the restored configuration with its spec and patch the
mutable parts or report `Degraded` on an immutable mismatch. Aliases are not
part of a snapshot; declare them on a `QdrantCollection` or re-create them
by hand.
