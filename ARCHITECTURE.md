# Qdrant Operator Architecture

A Go Kubernetes operator built on controller-runtime that manages Qdrant clusters through the official
[qdrant-helm](https://github.com/qdrant/qdrant-helm) chart and adds multi-node backup and
restore to S3-compatible storage.

## Custom resources

| Kind | What the operator does |
|------|------------------------|
| `QdrantCluster` | Helm install or upgrade of the qdrant chart (Helm Go SDK, in-process) with values derived 1:1 from the spec; a 30s requeue lifts StatefulSet readiness into `status.phase`, `status.readyReplicas` and the `Ready`/`Progressing` conditions. |
| `QdrantBackup` | Snapshots **every node** of the cluster per collection, streams each snapshot straight into the bucket, writes `manifest.json`, deletes the node-local snapshots. Deleting the resource deletes its objects. `retentionDays` sets `status.expiresAt`; the reconciler deletes the resource once it passes. |
| `QdrantBackupSchedule` | CronJob semantics: fires the most recent missed slot once (`startingDeadlineSeconds` bounds how late), honours `concurrencyPolicy` (Forbid/Allow/Replace), owns the spawned backups (ownerReferences → GC), applies `retentionPolicy` (keepLast/keepDaily/keepWeekly/keepMonthly) by deleting old `QdrantBackup`s, keeps `status.recentBackups`. |
| `QdrantRestore` | Reads the backup manifest (via `backupRef` or a direct `source.s3.path`), hands each target node a presigned URL for the snapshot of the matching source node (`PUT /collections/{c}/snapshots/recover`), optionally waits for the collection to turn green. Node counts must match. |
| `QdrantCollection` | Level-triggered: on create, spec change, resume and every 60s it creates the collection if absent, PATCHes only when the declared mutable fields differ from the live config (`is_subset`), replaces payload indexes whose type or params changed, points aliases here, and removes only indexes/aliases it created earlier (tracked in `status`); custom shard keys are created but never dropped. Immutable mismatches (vector size/distance, `shardNumber`, `shardingMethod`) become `phase: Degraded` + `Ready=False`. `deletionPolicy` decides whether the collection is dropped with the resource. |
| `QdrantAccessKey` | Signs an HS256 JWT with the cluster's API key (claims in the layout Qdrant's auth parser expects: `access` as `r`/`m` or a per-collection list, `sub`, `exp`, `value_exists`) and writes `token` + `url` into an owned Secret. Re-issues on spec change, key rotation (fingerprint in status), missing Secret, or when `renewBefore` is reached. Blocked with `Ready=False` while the cluster has `apiKey.jwtRbac: false`. |
| `QdrantMigration` | One-shot copy from a managed cluster or any endpoint (URL + API key/CA secrets) into a managed cluster: scroll pages → upserts grouped by the target shard key (source key preserved, or `target.shardKey`/`shardKeyField`); missing target collections are created from the source config with shard/replication counts left to the target unless overridden, existing ones must match the vector layout; payload indexes and shard keys are created as needed. Progress plus the scroll offset are patched every 20 batches, and a re-run resumes from them. |

### Bucket layout

```
<prefix>/<backup-name>/
├── manifest.json                       # BackupManifest: nodes, collections, keys, checksums
└── <collection>/node-<i>/<snapshot>    # one snapshot per StatefulSet ordinal
```

## Layers

```
cmd/operator            cobra/fang command; flags and QDRANT_OPERATOR_* variables read by viper
   │
internal/app            wiring: adapters → use cases → reconcilers, manager, probes, metrics
   │
internal/controller     one reconciler per kind · Timing intervals · finalizers · status patch · events
   │
internal/usecases       cluster, backup, restore, schedule, collection, access key, migration logic
   │
internal/ports          Helm · Qdrant · Storage · Kubernetes · Token interfaces
   │
internal/adapters       helm (Helm v4 Go SDK, in-process, values passed as a map, charts cached under the Helm cache directory)
                        qdrant (net/http, one client per call, TLS CA from the cluster's secret)
                        s3 (AWS SDK v2, streaming multipart upload, presigned GET)
                        kubernetes (client-go) · jwt (HS256) · cron (schedule parsing)
internal/translate      API types ↔ domain types
internal/domain         pure rules: helm value derivation, cron/retention, conditions — stdlib only
api/v1alpha1            CRD Go types with kubebuilder markers; CRDs are generated from them
internal/fakes          in-memory implementations of the ports, used by the tests
internal/settings       flags and environment variables
```

Rules the code follows:

- Reconcilers call use cases only; use cases see ports only; adapters implement ports.
- Status is written as one whole-status JSON patch, never field by field.
- Phase changes are recorded as `events.k8s.io` Events: `Warning` for `Failed`, `Normal`
  otherwise, with the status error or message as the note.
- Transient problems (`QdrantCluster` missing, secret missing, backup not yet complete) requeue
  after `Timing.Retry`; anything else writes `phase: Failed`.
- Clusters, backups and collections carry the finalizer `qdrant.io/finalizer`, which runs the
  Helm uninstall, deletes the bucket objects or drops the collection as the spec says.
- No temp files: snapshot bytes stream node → S3 and S3 → node; chart values are passed as a map.
- Adapters keep no connection state; each call builds and closes its own client.
- Level-triggered resources (`QdrantCollection`, `QdrantAccessKey`) reconcile on create, spec
  change and every requeue interval (60s); a condition in status, not an error, reports a spec
  that cannot be applied, so the requeue keeps observing without retry noise. controller-runtime
  never runs two reconciles of one object at once, so the requeue cannot overlap a spec change.
- CRDs carry CEL `x-kubernetes-validations` for what the schema alone cannot say (exactly-one
  choices, immutable fields, one-shot specs). `test/admission` runs them on envtest and expects the
  rejection messages.

## Runtime

- Entry point `qdrant-operator` (`cmd/operator`) starts the controller-runtime manager.
  `/healthz` and `/readyz` are served on `:8080`, Prometheus metrics on `:9090`.
- With `--manager-leader-election` several replicas run and one holds a Lease; the others wait as
  standbys. A clean shutdown releases the Lease at once.
- Logs go through slog (`--logging-level debug|info|warn|error`, `--logging-format json|text`).
- Metrics keep their names (`internal/controller/metrics.go`) on the controller-runtime metrics
  endpoint.
- The finalizer is `qdrant.io/finalizer`; progress annotations use the `qdrant.io` prefix.
- In-cluster config is used when present, otherwise the local kubeconfig (`make dev`).

## Testing

- `go test ./...` runs the unit tests and the envtest tests in `internal/controller` and
  `test/admission` (the CEL rules). `make test` downloads the pinned envtest control plane.
- `internal/fakes`: in-memory Helm, Qdrant, storage, Kubernetes and token fakes implementing the
  ports.
- `test/chart`: renders the chart and checks the objects and RBAC it produces.
- `make test-integration`: the Helm adapter against a real cluster (`-tags integration`).
- `test/e2e` (build tag `e2e`): the operator image deployed by its own chart on kind. Applies a
  cluster, collection, access key, backup to a RustFS bucket and restore, deletes them, and checks
  the operator pod did not restart. Runs under the chart's
  ServiceAccount, so missing RBAC verbs and in-pod Helm or S3 problems fail here.
