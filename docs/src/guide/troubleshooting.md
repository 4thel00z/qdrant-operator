# Troubleshooting

Every kind carries a phase and a `Ready` or `Complete` condition whose
`reason` names the situation and whose `message` gives the detail. Start
there, then the operator log:

```sh
kubectl describe qc my-qdrant
kubectl get qb first -o jsonpath='{.status.error}{"\n"}'
kubectl logs -n qdrant-system deploy/qdrant-operator -f
```

Retries show up in the log as kopf `TemporaryError` lines with the reason and
the thirty-second delay. Permanent failures write the phase `Failed` and the
error into the status and stop.

## QdrantCluster

| Symptom | Meaning | What to do |
|---|---|---|
| `Pending` and stays there | The Helm release is applied but pods are not ready | `kubectl get pods -l app.kubernetes.io/instance=qdrant-<name>`, then `describe` the pod that is not ready |
| `Pending`, no pods at all | `helm upgrade --install` failed; the operator retries | Look for `helm upgrade failed` in the log. Common causes: a chart version that does not exist for that `version`, a `config` key the chart rejects, missing `monitoring.coreos.com` CRDs with a ServiceMonitor enabled |
| Pods `Pending` | No node fits or no volume can be bound | `kubectl describe pod`: check resources, `nodeSelector`, `affinity` and the storage class |
| Pods `CrashLoopBackOff` | Qdrant rejects its configuration | Container logs; a typo under `config` lands in `production.yaml` verbatim |
| `Upgrading` for a long time | A rolling update is stuck on one pod | The StatefulSet waits for each pod to be ready before the next; fix that pod |

Only `Pending`, `Upgrading` and `Running` are set by the operator; a Helm
error keeps the previous phase and is retried.

## QdrantCollection

| Reason | Meaning |
|---|---|
| `CollectionReady` | The collection, its indexes and aliases match the spec |
| `ImmutableFieldMismatch` | `vectors`, `shardNumber` or `shardingMethod` differ from the live collection; phase `Degraded`, nothing changed |
| retried: cluster not found | `clusterRef` names a `QdrantCluster` that does not exist yet |
| retried: cluster not ready | `/readyz` on the Service does not answer 200 |
| `Failed` with an HTTP error | Qdrant rejected a request; the message carries Qdrant's response. Usually a field name Qdrant does not know inside `hnsw`, `optimizers`, `quantization`, `strictMode` or `params` |

A `401` or `403` from Qdrant means the operator's API key does not match: it
uses `apiKey.secretRef` or the generated `qdrant-<name>-apikey` Secret, and
nothing else.

## QdrantBackup

| Phase | Meaning |
|---|---|
| `Pending` | Waiting for the cluster or the credentials Secret; retried every thirty seconds |
| `InProgress` | Snapshots are being taken and uploaded |
| `Completed` | `manifest.json` written; `s3Path` and `totalSize` set |
| `Failed` | `status.error` holds the first collection's error, `status.collections` each one's |

Frequent causes of `Failed`:

- **Connection refused or timeout to a pod URL.** The operator reaches nodes
  as `qdrant-<name>-<i>.qdrant-<name>-headless.<ns>.svc.cluster.local`. A
  NetworkPolicy between the operator namespace and the cluster namespace, or
  a `replicas` value higher than the pods that exist, produces this.
- **TLS certificate verification failed.** With `tls.enabled`, the operator
  needs `ca.crt` in the TLS Secret, or a certificate from a CA in the system
  trust store, and the certificate must cover the headless pod names.
- **No space left on device from Qdrant.** The snapshot did not fit on the
  node's volume. Enable `snapshotPersistence` or grow the data volume.
- **Access denied from the bucket.** Check the credentials Secret, the
  bucket policy, and for MinIO `endpoint` plus `forcePathStyle: true`.

Deleting a `Failed` backup still removes what was uploaded.

## QdrantBackupSchedule

| Symptom | Meaning |
|---|---|
| No backup at the expected time | The slot is computed in UTC; check `nextBackupTime`. With `Forbid`, a still running backup blocks the slot, see `activeBackup` |
| One backup right after the operator restarted | A catch-up for the most recent missed slot. Set `startingDeadlineSeconds` to skip stale slots |
| Old backups disappearing | `retentionPolicy` pruned them, together with their objects in the bucket |
| Backups gone after deleting the schedule | They were owned by the schedule and garbage-collected |

## QdrantRestore

| Message | Meaning |
|---|---|
| `QdrantBackup ... is InProgress, not Completed` | Retried until the backup finishes |
| `Backup was taken from N nodes but target cluster has M` | Node counts must match; create a target with N replicas |
| `Collections not present in backup` | `collections` names something the manifest does not have |
| `Collection ... did not turn green in time` | Indexing took more than one hour; the data is there, only the wait timed out |
| A node returns 4xx on `snapshots/recover` | The node could not fetch the presigned URL. Nodes need network access to the bucket endpoint, and the URL expires after one hour |

## Operator pod

The pod runs as uid 1000 with a read-only root filesystem and Helm's cache
directories under an `emptyDir` at `/tmp`. `helm` failures mentioning
permission denied point at a policy that replaced that volume. The pod
answers `/healthz` on port 8080; a restart loop there means kopf could not
start, and the first log lines say why, typically RBAC on the CRDs or
missing CRDs.

Turn the log up with `--set operator.logLevel=DEBUG` to see every `helm`
command line and every Qdrant request the operator makes.
