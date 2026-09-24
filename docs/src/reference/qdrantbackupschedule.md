# QdrantBackupSchedule

`qdrantbackupschedules.qdrant.io`, short name `qbs`, namespaced. Backups on
a cron schedule with retention.

## Spec

| Field | Default | Description |
|---|---|---|
| `schedule` | required | Five-field cron expression, UTC |
| `clusterRef`, `storage`, `collections` | as on `QdrantBackup` | Copied into each backup |
| `suspend` | `false` | Stop creating backups; retention continues |
| `concurrencyPolicy` | `Forbid` | `Allow`, `Forbid` or `Replace` |
| `startingDeadlineSeconds` | unset | Skip a missed slot older than this |
| `retentionPolicy.keepLast` | unset | Keep the N newest finished backups |
| `retentionPolicy.keepDaily` | unset | Keep the newest per day for N days |
| `retentionPolicy.keepWeekly` | unset | Keep the newest per ISO week for N weeks |
| `retentionPolicy.keepMonthly` | unset | Keep the newest per month for N months |

## Status

| Field | Meaning |
|---|---|
| `phase` | `Active` or `Suspended` |
| `lastScheduleTime` | Slot the most recent backup was created for |
| `lastBackupName`, `lastBackupTime`, `lastBackupStatus` | Newest finished backup, its completion time and phase |
| `nextBackupTime` | Next slot after now |
| `activeBackup` | Name of a backup still running |
| `recentBackups[]` | Up to ten, newest first: `name`, `creationTime`, `completionTime`, `status`, `size` |

## Behavior

On create, spec update, operator start and once a minute: list the backups
labelled `qdrant.io/schedule=<name>`, delete the finished ones the retention
policy does not keep, then, unless suspended, compute the latest slot before
now and create `<name>-<YYYYmmdd-HHMMSS>` for it when it is newer than
`lastScheduleTime` (or the schedule's creation time) and within
`startingDeadlineSeconds`. With `Forbid`, a running backup skips the slot;
with `Replace`, it is deleted first. Created backups carry the schedule as
their controller owner reference and are garbage-collected with it.

Print columns: `SCHEDULE`, `CLUSTER`, `PHASE`, `LAST BACKUP`, `AGE`.
