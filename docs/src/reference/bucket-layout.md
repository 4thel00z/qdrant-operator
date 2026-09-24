# Bucket layout

A `QdrantBackup` named `nightly-20260924-020000` with `prefix:
prod/my-qdrant` on a three-node cluster with two collections produces:

```
prod/my-qdrant/nightly-20260924-020000/
├── manifest.json
├── documents/
│   ├── node-0/documents-2026-09-24-02-00-03.snapshot
│   ├── node-1/documents-2026-09-24-02-00-09.snapshot
│   └── node-2/documents-2026-09-24-02-00-14.snapshot
└── images/
    ├── node-0/images-2026-09-24-02-00-20.snapshot
    ├── node-1/images-2026-09-24-02-00-31.snapshot
    └── node-2/images-2026-09-24-02-00-40.snapshot
```

Snapshot file names are whatever Qdrant chose; the manifest is what a
restore reads.

## manifest.json

```json
{
  "backupName": "nightly-20260924-020000",
  "cluster": {"name": "my-qdrant", "namespace": "prod"},
  "nodeCount": 3,
  "createdAt": "2026-09-24T02:00:01Z",
  "collections": [
    {
      "name": "documents",
      "snapshots": [
        {
          "nodeIndex": 0,
          "key": "prod/my-qdrant/nightly-20260924-020000/documents/node-0/documents-2026-09-24-02-00-03.snapshot",
          "snapshotName": "documents-2026-09-24-02-00-03.snapshot",
          "sizeBytes": 734003200,
          "checksum": "3f2a…"
        }
      ]
    }
  ]
}
```

| Field | Meaning |
|---|---|
| `backupName` | The `QdrantBackup` name |
| `cluster` | The source `QdrantCluster` |
| `nodeCount` | Replicas of the source at backup time; a restore target must match |
| `createdAt` | When the backup started |
| `collections[]` | Completed collections only; a failed collection is absent |
| `snapshots[].nodeIndex` | StatefulSet ordinal the snapshot came from, and the ordinal it is restored to |
| `snapshots[].key` | Full object key inside the bucket |
| `snapshots[].sizeBytes` | Bytes uploaded |
| `snapshots[].checksum` | Qdrant's snapshot checksum, passed to recovery when present |

## Lifecycle

Deleting the `QdrantBackup` deletes every object under the backup's root,
in batches of a thousand keys. Nothing else in the bucket is touched, so
several clusters and schedules can share a bucket under different prefixes.
A `QdrantRestore` with `source.s3.path` set to the root key works on any
manifest, including one whose `QdrantBackup` no longer exists or was taken in
another Kubernetes cluster.
