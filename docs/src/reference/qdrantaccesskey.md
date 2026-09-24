# QdrantAccessKey

`qdrantaccesskeys.qdrant.io`, short name `qak`, namespaced. A Qdrant RBAC
token, signed with the cluster's API key and kept in a Secret.

## Spec

| Field | Default | Description |
|---|---|---|
| `clusterRef.name` | required | A `QdrantCluster` with `apiKey.jwtRbac: true`. Immutable |
| `clusterRef.namespace` | own namespace | |
| `access` | | `r` or `m`, cluster-wide. Exclusive with `collections` |
| `collections[]` | | Up to 256, keyed by `name`. Exclusive with `access` |
| `collections[].name` | required | Collection inside Qdrant |
| `collections[].access` | required | `r`, `rw` or `prw` |
| `subject` | unset | The `sub` claim |
| `ttl` | never expires | Lifetime such as `720h` or `1h30m` |
| `renewBefore` | a third of `ttl` | How long before expiry a new token is written. Needs `ttl` |
| `valueExists.collection` | | Token valid only while a matching point exists here |
| `valueExists.matches[]` | | One to 32 `{key, value}` pairs the point must carry |
| `secretName` | `metadata.name` | Secret receiving the token. Immutable |

Admission rules: exactly one of `access` and `collections`; `renewBefore`
needs `ttl`; `clusterRef` and `secretName` cannot change.

## Status

| Field | Meaning |
|---|---|
| `phase` | `Pending`, `Ready` or `Failed` |
| `secretRef.name`, `secretRef.key` | Where the token is; the key is `token` |
| `issuedAt`, `expiresAt`, `renewAt` | Timeline of the current token |
| `keyFingerprint` | First sixteen hex digits of the SHA-256 of the signing key |
| `error` | Why the phase is not `Ready` |
| `observedGeneration` | Spec generation the token reflects |
| `conditions[Ready]` | `TokenIssued`, `JwtRbacDisabled` or `ApiKeyMissing` |

## Behavior

On create, spec update, operator start and once a minute: read the cluster,
stay `Pending` when `jwtRbac` is off or it has no API key, read the API key,
and re-issue when the phase is not `Ready`, the Secret is missing, the spec
generation or key fingerprint changed, or `renewAt` has passed. The Secret
is `Opaque` with keys `token` and `url`, lives in the access key's namespace,
and is owned by the resource, so it is garbage-collected on delete. The
token is an HS256 JWT with claims `sub`, `exp`, `access` and `value_exists`,
each only when set.

Print columns: `CLUSTER`, `ACCESS`, `PHASE`, `SECRET`, `EXPIRES`, `AGE`.
