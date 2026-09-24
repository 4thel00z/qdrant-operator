# Collections

A `QdrantCollection` declares a collection inside a cluster and keeps it that
way. The operator creates the collection when it is missing, patches what
Qdrant allows to change on a live collection, and manages payload indexes and
aliases. It runs on every spec change and once a minute, so a collection or
index someone dropped by hand comes back.

## A minimal collection

```yaml
apiVersion: qdrant.io/v1alpha1
kind: QdrantCollection
metadata:
  name: documents
spec:
  clusterRef: {name: my-qdrant}
  vectors:
    - size: 1536
      distance: Cosine
```

One entry without a `name` is Qdrant's single unnamed vector. Name every
entry to declare several dense vector spaces; the API server rejects a mix.
Declare at least one of `vectors` or `sparseVectors`.

`collectionName` defaults to `metadata.name`. Set it when the Qdrant name
needs characters a Kubernetes name cannot carry, such as underscores or
upper case. Both `clusterRef` and `collectionName` are immutable.

## Vectors, sparse vectors and multivectors

```yaml
spec:
  vectors:
    - name: text
      size: 768
      distance: Cosine
      datatype: float16
      onDisk: true
      hnsw: {m: 32, ef_construct: 200}
      quantization: {scalar: {type: int8, always_ram: true}}
    - name: colbert
      size: 128
      distance: Dot
      multivector: {comparator: max_sim}
  sparseVectors:
    - name: bm25
      modifier: idf
      index: {on_disk: true}
```

`hnsw`, `quantization` and `index` take Qdrant's own field names verbatim
and are passed through untouched, so anything the Qdrant version you run
accepts is valid here.

## Collection-wide settings

```yaml
spec:
  shardNumber: 6
  replicationFactor: 2
  writeConsistencyFactor: 1
  onDiskPayload: true
  hnsw: {m: 16, ef_construct: 100}
  optimizers: {indexing_threshold: 20000}
  quantization: {scalar: {type: int8}}
  wal: {wal_capacity_mb: 64}
  strictMode: {enabled: true, max_query_limit: 1000}
```

`hnsw`, `optimizers`, `quantization`, `wal` and `strictMode` map to the
`*_config` bodies of the collection API. `wal` applies at creation only;
Qdrant has no way to change it afterwards, and the operator does not compare
it.

## What can change on a live collection

| Field | On change |
|---|---|
| `vectors[].size`, `distance`, `datatype`, `multivector` | Immutable. The resource turns `Degraded` |
| `shardNumber`, `shardingMethod` | Immutable. The resource turns `Degraded` |
| `vectors[].onDisk`, `hnsw`, `quantization` | Patched per vector |
| `sparseVectors` | Patched |
| `replicationFactor`, `writeConsistencyFactor`, `readFanOutFactor`, `onDiskPayload` | Patched |
| `hnsw`, `optimizers`, `quantization`, `strictMode` | Patched |
| `wal` | Applied at creation, never compared afterwards |
| `payloadIndexes`, `aliases` | Reconciled, see below |

When an immutable field differs from the live collection, the phase becomes
`Degraded` with reason `ImmutableFieldMismatch`, nothing is changed, and
indexes and aliases are left as they are until the mismatch is resolved. Fix
it by reverting the spec, or by deleting and re-creating the collection
through a resource with `deletionPolicy: Delete`.

The operator compares only fields the spec declares. Anything you leave out
stays at whatever Qdrant chose, and setting a boolean to `false` matches a
value Qdrant reports as unset.

## Payload indexes

```yaml
spec:
  payloadIndexes:
    - field: tenant
      type: keyword
    - field: body
      type: text
      params: {tokenizer: word, lowercase: true, min_token_len: 2}
    - field: created_at
      type: datetime
```

Each entry becomes `PUT /collections/<name>/index` with `field_schema` set to
the bare type, or to the type plus `params` when given. An existing index of
another type or with other parameters is dropped and re-created. Indexes are
removed only when this resource created them earlier and the spec no longer
lists them; indexes created by applications stay. `status.payloadIndexes`
lists the fields under management.

## Aliases

```yaml
spec:
  aliases: [documents-current]
```

Aliases are pointed at this collection through one atomic alias update. An
alias that currently points elsewhere is moved. As with indexes, the operator
deletes an alias only when it created it and the spec dropped it, and
`status.aliases` lists the ones it manages.

## Deleting

`deletionPolicy` defaults to `Retain`: deleting the resource leaves the
collection in Qdrant. With `Delete` the operator issues
`DELETE /collections/<name>` and ignores a 404. When the cluster itself is
already gone, deletion completes without contacting Qdrant.

## Status

```
NAME        CLUSTER     COLLECTION   PHASE   POINTS   HEALTH   AGE
documents   my-qdrant   documents    Ready   120044   green    3d
```

`health` is Qdrant's own collection status (`green`, `yellow`, `grey`,
`red`), and `pointsCount`, `indexedVectorsCount` and `segmentsCount` are
copied from the collection info on every reconcile. The `Ready` condition
carries `CollectionReady` or the mismatch reason.
