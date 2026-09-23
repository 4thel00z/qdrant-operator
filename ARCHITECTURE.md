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
   │
ports.py         HelmPort · QdrantPort · StoragePort · KubernetesPort   (typing.Protocol)
   │
adapters         helm_adapter (helm CLI, values over stdin, --repo, no repo cache state)
                 qdrant_adapter (httpx, one client per call, TLS CA from the cluster's secret)
                 s3_adapter (aioboto3, streaming multipart upload, presigned GET)
                 kubernetes_adapter (kubernetes-asyncio, config loaded once at startup)
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
