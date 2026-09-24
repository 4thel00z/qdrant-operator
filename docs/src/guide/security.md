# API keys and TLS

The chart can protect Qdrant with an API key and serve HTTPS. The operator
passes both settings through and, because it talks to Qdrant itself for
collections, backups and restores, reads the same key and CA it configured.

## API keys

```yaml
spec:
  apiKey:
    autoGenerate: true
  readOnlyApiKey:
    secretRef: {name: qdrant-readonly, key: api-key}
```

Set either `secretRef` or `autoGenerate` on each key, not both; the API
server rejects the combination. With `autoGenerate` the chart creates a
Secret named `qdrant-<name>-apikey` holding the key under `api-key`. With
`secretRef` you bring your own Secret in the cluster's namespace, and the
chart mounts it through `valueFrom.secretKeyRef`.

The operator resolves the full-access key the same way, from your Secret or
the generated one, and sends it as the `api-key` header on every call it
makes. `readOnlyApiKey` is for your clients only; the operator never uses it.

Without an `apiKey` Qdrant accepts unauthenticated requests. That is fine on
a private network and wrong anywhere else.

`apiKey.jwtRbac: true` additionally lets Qdrant accept tokens signed with
the API key, which is what [access keys](./access-keys.md) hand to your
applications: scoped, expiring credentials instead of the one key that can
do everything.

## TLS

```yaml
spec:
  tls:
    enabled: true
    secretRef: {name: my-qdrant-tls}
```

`tls.enabled` requires `secretRef.name`. The Secret is a
`kubernetes.io/tls` Secret with `tls.crt` and `tls.key`, and the operator
mounts it at `/qdrant/tls` and sets `service.enable_tls` together with the
certificate paths in Qdrant's configuration. `status.endpoint` switches to
`https://`.

To verify the server certificate on its own calls, the operator reads
`ca.crt` from the same Secret. If the key is absent, it falls back to the
system trust store, which works for a certificate from a public CA and fails
for a self-signed one. cert-manager writes `ca.crt` into Secrets issued by its CA and
self-signed issuers, so a cert-manager `Certificate` for `qdrant-<name>.<namespace>.svc`
and its headless pod names covers both the Service and the per-node
snapshot traffic.

The certificate must be valid for the names the operator connects to:
`qdrant-<name>.<namespace>.svc.cluster.local` and
`qdrant-<name>-<i>.qdrant-<name>-headless.<namespace>.svc.cluster.local`
for every ordinal.

## p2p TLS

`cluster.p2p.enableTls` turns on TLS for the inter-node Raft and shard
transfer traffic and reuses the same certificate. Qdrant then requires every
node to present a certificate the others trust, so the mounted `tls.crt`
must cover the headless pod names.

## Where secrets are read from

| Secret | Namespace | Used for |
|---|---|---|
| `apiKey.secretRef` / `qdrant-<name>-apikey` | the cluster's | Header on every operator call to Qdrant |
| `tls.secretRef` | the cluster's | Served by Qdrant; `ca.crt` verifies the operator's calls |
| `storage.s3.credentialsSecretRef` | the backup's, schedule's or restore's own | Bucket access |

A `QdrantBackup` in namespace `ops` can reference a cluster in namespace
`prod` through `clusterRef.namespace`, and the operator reads the cluster's
API key from `prod`. Its bucket credentials still come from `ops`.
