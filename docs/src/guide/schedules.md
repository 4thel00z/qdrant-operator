# Scheduled backups

A `QdrantBackupSchedule` creates `QdrantBackup` resources on a cron schedule
and prunes old ones. Its semantics follow Kubernetes CronJobs: one missed
slot is caught up, a `concurrencyPolicy` decides what happens when the
previous backup is still running, and `startingDeadlineSeconds` bounds how
late a run may start.

```yaml
apiVersion: qdrant.io/v1alpha1
kind: QdrantBackupSchedule
metadata:
  name: nightly
spec:
  schedule: "0 2 * * *"
  clusterRef: {name: my-qdrant}
  storage:
    s3:
      bucket: qdrant-backups
      prefix: prod/my-qdrant/nightly
      credentialsSecretRef: {name: s3-credentials}
  concurrencyPolicy: Forbid
  startingDeadlineSeconds: 3600
  retentionPolicy:
    keepLast: 3
    keepDaily: 7
    keepWeekly: 4
    keepMonthly: 6
```

```
NAME      SCHEDULE    CLUSTER     PHASE    LAST BACKUP   AGE
nightly   0 2 * * *   my-qdrant   Active   6h            12d
```

## When a backup is created

`schedule` is a five-field cron expression evaluated in UTC. The operator
looks at the schedule once a minute and on every spec change. It computes the
most recent slot before now and creates a backup for it when that slot is
later than `status.lastScheduleTime`, or, before the first run, later than
the schedule's creation time. Only that one slot is created; if the operator
was down for a day, one catch-up backup is taken, not twenty-four.

`startingDeadlineSeconds` skips the slot when more than that many seconds
have passed since it was due. Without it a missed slot is always caught up.

The backup is named `<schedule>-<YYYYmmdd-HHMMSS>` after the slot time,
carries the label `qdrant.io/schedule=<schedule>`, and has the schedule as
its controller owner. Deleting the schedule therefore garbage-collects its
backups, and with them their objects in the bucket. `collections` and
`storage` are copied into each backup.

## Concurrency

| Policy | When the previous backup is still running |
|---|---|
| `Forbid` (default) | Skip this slot; it is retried at the next tick until the running backup finishes or the deadline passes |
| `Allow` | Create the new backup alongside |
| `Replace` | Delete the running backup, and its partial objects, then create the new one |

`status.activeBackup` names the backup in flight.

## Retention

`retentionPolicy` keeps the union of four selections over the schedule's
finished backups, newest first:

- `keepLast`: the N most recent.
- `keepDaily`: the newest backup of each of the N most recent days that have one.
- `keepWeekly`: the same per ISO week.
- `keepMonthly`: the same per month.

Every finished backup outside that union is deleted at the next tick, which
deletes its objects in the bucket. Backups still running are never pruned.
Without a `retentionPolicy` nothing is deleted. Retention runs even while the
schedule is suspended.

## Suspending

`suspend: true` stops creating backups and sets the phase to `Suspended`.
Retention and status updates continue. Resuming does not create a backup for
slots missed while suspended, because `lastScheduleTime` is compared against
the latest slot only; the next due slot runs normally.

## Status

| Field | Meaning |
|---|---|
| `lastScheduleTime` | The cron slot the most recent backup was created for |
| `lastBackupName`, `lastBackupTime`, `lastBackupStatus` | The most recent finished backup, its completion time and phase |
| `nextBackupTime` | The next slot after now |
| `activeBackup` | A backup that is `Pending` or `InProgress` |
| `recentBackups` | The ten newest backups with creation time, completion time, phase and size |
