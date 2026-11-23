# Backups

A `QdrantBackup` snapshots every node of a cluster, collection by collection,
and streams each snapshot straight into an S3-compatible bucket. Nothing is
staged on the operator's disk. A manifest written next to the snapshots
records which node each one came from, so a restore needs no guessing.

## Choosing a method

`spec.method` is `s3` (the default) or `volumeSnapshot`.

| | `s3` | `volumeSnapshot` |
|---|---|---|
| What is stored | Qdrant snapshots per collection in a bucket | One CSI `VolumeSnapshot` per data volume |
| Speed | Bound by the stream through the operator pod | Seconds, done by the storage backend |
| Extra disk | One node snapshot on each node | None |
| Consistency | Per node, per collection | Crash-consistent per volume |
| Restore | Any cluster with the same node count, per collection | A new cluster of the same name and size, in the same namespace |
| Survives losing the cluster's storage | Yes | Only if the CSI snapshots live outside it |

Use `s3` for portability, partial restores and off-cluster copies. Use
`volumeSnapshot` for fast, frequent backups on a cluster whose CSI driver
supports snapshots.

## Volume snapshot backups

Prerequisites: the snapshot CRDs and snapshot controller installed, a CSI
driver that supports snapshots, and a `VolumeSnapshotClass` for it. Managed
Kubernetes usually ships all three.

```yaml
apiVersion: qdrant.io/v1alpha1
kind: QdrantBackup
metadata:
  name: before-upgrade
spec:
  clusterRef: {name: my-qdrant}
  method: volumeSnapshot
  volumeSnapshotClassName: csi-cinder-snapclass
```

`storage` is not needed here: it is required
only for `s3`, and `volumeSnapshotClassName` only for `volumeSnapshot`.
`QdrantBackupSchedule` takes the same two fields and its retention deletes
the snapshots of pruned backups.

The operator creates `snapshot.storage.k8s.io/v1` `VolumeSnapshot`s named
`<backup>-node-<i>` for the claims `qdrant-storage-qdrant-<cluster>-<i>`, in
the cluster's namespace, labelled `qdrant.io/backup=<backup>`. It waits until
every one is `readyToUse`, then records `status.volumeSnapshots[]` (node
index, claim, snapshot, size, storage class and access modes) and completes.
A failed or timed-out snapshot (one hour) deletes the others and fails the
backup. Deleting the `QdrantBackup`, by hand, by `retentionDays` or by a
schedule's retention, deletes its `VolumeSnapshot`s. The snapshot
volume of `snapshotPersistence` is not snapshotted.

The snapshots are crash-consistent: Qdrant has no API to flush to disk, so
each volume is captured as it is and Qdrant replays its write-ahead log on
start. Snapshots of different nodes are taken a moment apart and are not
coordinated; pause writers for a clean cut.

Restoring is described in [Restore](restore.md#from-a-volume-snapshot-backup).
It works only into a new cluster of the same name and replica count in the
same namespace.

## Requirements (s3)

- A bucket on S3 or anything speaking its API. MinIO needs `endpoint` and
  `forcePathStyle: true`.
- A Secret in the backup's namespace with the access key pair. The default
  keys are `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY`; other key names go
  in `credentialsSecretRef.accessKeyIdKey` and `secretAccessKeyKey`.
- Network access from the operator pod to every Qdrant pod through the
  headless Service, and from every Qdrant pod to the bucket for restores.
- Disk on each node for the snapshot Qdrant writes before it is streamed.
  `snapshotPersistence` on the cluster gives that its own volume.

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: s3-credentials
stringData:
  AWS_ACCESS_KEY_ID: AKIA...
  AWS_SECRET_ACCESS_KEY: ...
```

## A one-off backup

```yaml
apiVersion: qdrant.io/v1alpha1
kind: QdrantBackup
metadata:
  name: before-upgrade
spec:
  clusterRef: {name: my-qdrant}
  storage:
    s3:
      bucket: qdrant-backups
      prefix: prod/my-qdrant
      region: eu-central-1
      credentialsSecretRef: {name: s3-credentials}
  collections: [documents, images]
  retentionDays: 30
```

```
NAME             CLUSTER     PHASE       SIZE     AGE
before-upgrade   my-qdrant   Completed   1.3GB    2m
```

`collections` limits the backup; leave it out to back up every collection
the cluster lists. The spec is immutable, so a change of mind means a new
resource.

## What happens

1. The operator resolves the cluster: its Service URL, one URL per
   StatefulSet pod, the API key and the CA certificate. A missing cluster or
   Secret is retried every thirty seconds rather than failed.
2. The phase moves to `InProgress` with `startTime`. After a restart the
   original `startTime` is kept. Before snapshotting, the operator deletes on
   every node the snapshots of the backup's collections created at or after
   `startTime`, which can only be leftovers of the interrupted attempt.
3. For each collection, for each node in ordinal order: `POST
   /collections/<c>/snapshots?wait=true` on that node, then `GET` the snapshot
   as a stream into a multipart upload at
   `<prefix>/<backup>/<collection>/node-<i>/<snapshot>`, then `DELETE` the
   snapshot on the node. The node-local snapshot is deleted even when the
   upload fails. `status.inProgressSnapshot` names it while it is streamed.
4. `manifest.json` is written at `<prefix>/<backup>/` listing the completed
   collections, their snapshots, sizes and checksums, and the node count.
5. The phase becomes `Completed`, or `Failed` when any collection failed.
   `status.collections` carries each collection's outcome and
   `status.error` the first failure.

Collections and nodes are processed one after another, not in parallel. The
whole backup happens inside one handler invocation, so a restart of the
operator mid-way leaves the resource without a final phase; kopf then re-runs
the handler and the backup is taken again from the start. Snapshot names
carry Qdrant's timestamp, so the second run writes new objects and only
`manifest.json` is overwritten; the first run's partial objects stay under
the same root until the resource is deleted, when everything under it goes.
The first run's node snapshots are deleted when the second starts. Do not
create other snapshots of the same collections by hand while a backup runs:
a snapshot made after `startTime` is treated as a leftover.

## Consistency

Each snapshot is consistent for the shards on its node at the moment Qdrant
took it. Snapshots on different nodes are taken a few seconds apart and are
not coordinated, so writes that arrive during the backup may be in one
node's snapshot and not another's. Pause writers or accept that window.

## Retention and deletion

`retentionDays` sets `status.expiresAt` to the completion time plus that
many days. A timer checks every five minutes and deletes the resource once
the time has passed.

Deleting a `QdrantBackup`, by hand, by expiry or by a schedule's retention,
deletes every object under `<prefix>/<backup>/` in the bucket and aborts
unfinished multipart uploads there, which an upload cut off by an operator
restart leaves behind and the bucket still bills for. A re-run backup aborts
them as well before it uploads again. When the credentials Secret is gone at
that point, the operator logs a warning and leaves the objects in place.
Deleting a backup that did not complete also deletes the node snapshots it
left behind, by the same `startTime` rule. To keep the data but drop the
resource, copy the objects first, or point a later `QdrantRestore` at their
path through `source.s3`.

## Cross-namespace clusters

`clusterRef.namespace` defaults to the backup's namespace. When it names
another namespace, the operator reads the cluster and its API key Secret
there, while the bucket credentials still come from the backup's own
namespace.
