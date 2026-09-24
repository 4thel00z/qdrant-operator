# Access keys

A `QdrantAccessKey` issues a Qdrant role-based access token and keeps it in a
Secret for your applications. Qdrant validates such tokens as JSON Web Tokens
signed with the cluster's API key, so no call to Qdrant is needed to create
one: the operator signs the claims itself and rewrites the Secret before the
token expires.

## Turn on JWT RBAC

```yaml
apiVersion: qdrant.io/v1alpha1
kind: QdrantCluster
metadata:
  name: my-qdrant
spec:
  version: v1.16.3
  apiKey:
    autoGenerate: true
    jwtRbac: true
```

`apiKey.jwtRbac` sets `service.jwt_rbac` in Qdrant's configuration and needs
an API key to sign with, either `secretRef` or `autoGenerate`; the API server
rejects `jwtRbac` without one. The full-access key keeps working as before,
and tokens signed with it are accepted next to it. A `QdrantAccessKey`
against a cluster without `jwtRbac` stays `Pending` with reason
`JwtRbacDisabled`.

Qdrant's parser accepts HS256 only, and HS256 wants at least 32 bytes of key.
The chart's generated key is long enough; with `secretRef`, use 32 random
bytes or more, or the operator logs a warning on every token.

## A read-only token for one collection

```yaml
apiVersion: qdrant.io/v1alpha1
kind: QdrantAccessKey
metadata:
  name: search-frontend
spec:
  clusterRef: {name: my-qdrant}
  collections:
    - name: documents
      access: r
  subject: search-frontend
  ttl: 720h
```

```
NAME              CLUSTER     ACCESS   PHASE   SECRET            EXPIRES                AGE
search-frontend   my-qdrant            Ready   search-frontend   2026-10-24T07:30:00Z   4s
```

The Secret `search-frontend` in the same namespace now holds two keys:
`token`, the signed JWT, and `url`, the cluster's in-cluster HTTP URL. Mount
or inject both:

```yaml
env:
  - name: QDRANT_URL
    valueFrom: {secretKeyRef: {name: search-frontend, key: url}}
  - name: QDRANT_API_KEY
    valueFrom: {secretKeyRef: {name: search-frontend, key: token}}
```

Qdrant clients send the token where they would send the API key, in the
`api-key` header or as a bearer token.

## Access levels

Set exactly one of `access` and `collections`.

| Field | Values | Meaning |
|---|---|---|
| `access` | `r` | Read everything on the cluster |
| | `m` | Manage everything, the same as the API key |
| `collections[].access` | `r` | Read this collection |
| | `rw` | Read and write points, manage the collection |
| | `prw` | Read and write points only; no snapshots, no index or schema changes |

`subject` becomes the `sub` claim, which Qdrant records in its logs and
which your own auditing can use. `valueExists` adds Qdrant's condition that
the token is valid only while a point matching every listed key and value
exists in the named collection, so deleting that point revokes the token
without touching the API key:

```yaml
spec:
  access: r
  valueExists:
    collection: api_tokens
    matches:
      - {key: owner, value: search-frontend}
      - {key: active, value: true}
```

## Lifetime and renewal

`ttl` is a duration in hours, minutes and seconds such as `720h` or `1h30m`
and sets the `exp` claim. Without `ttl` the token never expires. With it,
the operator writes a fresh token at `renewAt`, which is `renewBefore` ahead
of expiry, or a third of `ttl` when `renewBefore` is unset. The check runs
once a minute. Applications must re-read the Secret to pick the new token
up; a Secret mounted as a file is updated by the kubelet, an environment
variable is not.

A token is also re-issued when the spec changes, when the Secret is deleted,
and when the cluster's API key changes. `status.keyFingerprint` records the
key the token was signed with.

## Revocation

A JWT cannot be revoked. Deleting the `QdrantAccessKey` deletes its Secret
through the owner reference, but a copy of the token stays valid until `exp`.
To cut a token off early, use `valueExists`, keep `ttl` short, or rotate the
cluster's API key, which invalidates every token at once and makes the
operator re-issue every `QdrantAccessKey` on its next tick.

## Status

| Field | Meaning |
|---|---|
| `phase` | `Pending` while blocked, `Ready` with a token in the Secret, `Failed` on an error |
| `secretRef` | Name and key of the token |
| `issuedAt`, `expiresAt`, `renewAt` | The current token's timeline |
| `keyFingerprint` | Prefix of the SHA-256 of the signing key |
| `conditions[Ready]` | `TokenIssued`, `JwtRbacDisabled` or `ApiKeyMissing` |
