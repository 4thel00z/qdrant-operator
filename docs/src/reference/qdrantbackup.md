# QdrantBackup

`qdrantbackups.qdrant.io`, short name `qb`, namespaced. One backup of a
cluster: a snapshot per collection per node, in a bucket, with a manifest.
The spec is immutable.

## Spec

| Field | Default | Description |
|---|---|---|
| `clusterRef.name` | required | The `QdrantCluster` |
| `clusterRef.namespace` | own namespace | |
| `storage.s3.bucket` | required | |
| `storage.s3.prefix` | `""` | Key prefix; the backup lives under `<prefix>/<name>/` |
| `storage.s3.region` | `us-east-1` | |
| `storage.s3.endpoint` | AWS | Custom endpoint for MinIO and other S3-compatible stores |
| `storage.s3.forcePathStyle` | `false` | Path-style URLs, required for MinIO |
| `storage.s3.credentialsSecretRef.name` | required | Secret in the backup's namespace |
| `storage.s3.credentialsSecretRef.accessKeyIdKey` | `AWS_ACCESS_KEY_ID` | |
| `storage.s3.credentialsSecretRef.secretAccessKeyKey` | `AWS_SECRET_ACCESS_KEY` | |
| `collections[]` | all | Collections to back up |
| `retentionDays` | unset | Days after completion until the resource deletes itself |

## Status

| Field | Meaning |
|---|---|
| `phase` | `Pending`, `InProgress`, `Completed` or `Failed` |
| `startTime`, `completionTime` | |
| `expiresAt` | `completionTime` plus `retentionDays` |
| `s3Path` | `s3://<bucket>/<prefix>/<name>` |
| `totalSize` | Sum of the completed snapshots, human-readable |
| `collections[]` | Per collection: `name`, `status`, `size`, `snapshots[]` with `node`, `snapshotName` and `key`, and `error` |
| `error` | The first failed collection's error |
| `conditions[Complete]` | `BackupCompleted` or `BackupFailed`, message `Backed up n/m collections` |

## Behavior

On create only. Resolves the cluster and the credentials, retrying every
thirty seconds while either is missing, sets `InProgress`, then for each
collection and each node in ordinal order creates a snapshot, streams it to
`<prefix>/<name>/<collection>/node-<i>/<snapshot>` and deletes it on the
node. Writes `manifest.json` under `<prefix>/<name>/` and sets `Completed`,
or `Failed` when any collection failed. With `retentionDays`, a timer every
five minutes deletes the resource once `expiresAt` has passed.

On delete: every object under `<prefix>/<name>/` is removed. If the
credentials Secret is missing, a warning is logged and the objects stay.

Print columns: `CLUSTER`, `PHASE`, `SIZE`, `AGE`.
