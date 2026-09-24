# Qdrant Operator Architecture

A kopf-based Kubernetes operator that manages Qdrant clusters through the official
[qdrant-helm](https://github.com/qdrant/qdrant-helm) chart and adds multi-node backup and
restore to S3-compatible storage.

## Custom resources

| Kind | What the operator does |
|------|------------------------|
| `QdrantCluster` | `helm upgrade --install` of the qdrant chart with values derived 1:1 from the spec; a 30s timer lifts StatefulSet readiness into `status.phase`, `status.readyReplicas` and the `Ready`/`Progressing` conditions. |
| `QdrantBackup` | Snapshots **every node** of the cluster per collection, streams each snapshot straight into the bucket, writes `manifest.json`, deletes the node-local snapshots. Deleting the resource deletes its objects. `retentionDays` sets `status.expiresAt`; a timer deletes the resource once it passes. |
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
handlers.py      kopf event → spec/status dataclasses → use case → patch.status
   │
usecases.py      ReconcileCluster · ObserveCluster · DeleteCluster · ResolveCluster
   │             ExecuteBackup · DeleteBackupData · ExpireBackup · ExecuteRestore · ProcessSchedule
   │             ReconcileCollection · DeleteCollection · IssueAccessKey · ExecuteMigration
   │
ports.py         HelmPort · QdrantPort · StoragePort · KubernetesPort · TokenPort  (typing.Protocol)
   │
adapters         helm_adapter (helm CLI, values over stdin, --repo, no repo cache state)
                 qdrant_adapter (httpx, one client per call, TLS CA from the cluster's secret)
                 s3_adapter (aioboto3, streaming multipart upload, presigned GET)
                 kubernetes_adapter (kubernetes-asyncio, config loaded once at startup)
                 jwt_adapter (PyJWT, HS256)
domain.py        frozen dataclasses with from_dict/to_dict, helm value derivation,
                 cron/retention rules, condition handling — no I/O, no framework imports
```

Rules the code follows:

- Handlers call use cases only; use cases see ports only; adapters implement ports structurally.
- Status is written with `patch.status.update(...)`, never via handler return values
  (kopf stores those under `status.<handler-id>`).
- Transient problems (`QdrantCluster` missing, secret missing, backup not yet complete) raise
  `kopf.TemporaryError`; anything else writes `phase: Failed` and raises `kopf.PermanentError`.
- No temp files: snapshot bytes stream node → S3 and S3 → node; helm values go over stdin.
- Adapters keep no connection state; each call builds and closes its own client.
- Level-triggered resources (`QdrantCollection`, `QdrantAccessKey`) run one handler for
  create/update/resume and a 60s timer; a condition in status, not an exception, reports a spec
  that cannot be applied, so the timer keeps observing without retry noise.
- CRDs carry CEL `x-kubernetes-validations` for what the schema alone cannot say (exactly-one
  choices, immutable fields, one-shot specs). The kind e2e job applies invalid objects with
  `--dry-run=server` and expects the rejection messages.
- Names never carry an underscore prefix; control flow uses guard clauses.

## Runtime

- Entry point `qdrant-operator` runs `kopf run -m qdrant_operator.handlers --all-namespaces
  --liveness=http://0.0.0.0:8080/healthz`.
- kopf logs go through stdlib logging into loguru (`LOG_LEVEL`, `LOG_FORMAT=json|text`).
- Progress and diff-base annotations use the `qdrant.io` prefix; the finalizer is
  `qdrant.io/finalizer`.
- In-cluster config is used when present, otherwise the local kubeconfig (`make dev`).

## Testing

- `tests/fakes.py`: in-memory `FakeHelm`, `FakeQdrant`, `FakeStorage`, `FakeKubernetes`
  implementing the ports. No `mock.patch`.
- `tests/test_domain.py`, `tests/test_usecases.py`: pure unit tests, run in well under a second.
- `tests/test_crds.py`: the chart's CRDs must be identical to `manifests/crds` (`make crds-sync`).
- `tests/test_integration.py`: real cluster + helm, enabled with `QDRANT_OPERATOR_INTEGRATION=1`.
