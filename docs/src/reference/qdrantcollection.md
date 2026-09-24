# QdrantCollection

`qdrantcollections.qdrant.io`, short name `qcol`, namespaced. One collection
inside a cluster, with its payload indexes and aliases.

## Spec

| Field | Default | Description |
|---|---|---|
| `clusterRef.name` | required | The `QdrantCluster`. Immutable |
| `clusterRef.namespace` | own namespace | |
| `collectionName` | `metadata.name` | Name inside Qdrant. Immutable |
| `vectors[]` | | Dense vector spaces, up to 64. One unnamed entry is the single unnamed vector |
| `vectors[].name` | | Required when there is more than one entry |
| `vectors[].size` | required | Dimension. Immutable |
| `vectors[].distance` | required | `Cosine`, `Euclid`, `Dot` or `Manhattan`. Immutable |
| `vectors[].datatype` | Qdrant default | `float32`, `float16` or `uint8`. Immutable |
| `vectors[].multivector.comparator` | | `max_sim`. Immutable |
| `vectors[].onDisk` | Qdrant default | Mutable |
| `vectors[].hnsw` | `{}` | Per-vector `HnswConfigDiff`, passed through. Mutable |
| `vectors[].quantization` | `{}` | Per-vector `QuantizationConfig`, passed through. Mutable |
| `sparseVectors[]` | | Up to 64, each with a required `name` |
| `sparseVectors[].modifier` | | `none` or `idf` |
| `sparseVectors[].index` | `{}` | `SparseIndexParams`, passed through |
| `shardNumber` | Qdrant default | Immutable |
| `shardingMethod` | `auto` | `auto` or `custom`. Immutable |
| `replicationFactor`, `writeConsistencyFactor`, `readFanOutFactor` | Qdrant default | Mutable |
| `onDiskPayload` | Qdrant default | Mutable |
| `hnsw` | `{}` | Collection-wide `HnswConfigDiff` |
| `optimizers` | `{}` | `OptimizersConfigDiff` |
| `quantization` | `{}` | Collection-wide `QuantizationConfig` |
| `wal` | `{}` | `WalConfigDiff`, creation only |
| `strictMode` | `{}` | `StrictModeConfig` |
| `payloadIndexes[]` | | Up to 256, keyed by `field` |
| `payloadIndexes[].field` | required | Payload key to index |
| `payloadIndexes[].type` | required | `keyword`, `integer`, `float`, `geo`, `text`, `bool`, `datetime` or `uuid` |
| `payloadIndexes[].params` | `{}` | Type-specific index parameters |
| `aliases[]` | | Up to 64 aliases that must point at this collection |
| `deletionPolicy` | `Retain` | `Retain` or `Delete` |

Admission rules: at least one of `vectors` and `sparseVectors`; an unnamed
vector must be the only vector; `clusterRef` and `collectionName` cannot
change.

## Status

| Field | Meaning |
|---|---|
| `phase` | `Pending`, `Ready`, `Degraded` or `Failed` |
| `collectionName` | The name inside Qdrant |
| `health` | Qdrant's collection status: `green`, `yellow`, `grey` or `red` |
| `pointsCount`, `indexedVectorsCount`, `segmentsCount` | From the collection info |
| `payloadIndexes` | Fields whose indexes this resource manages |
| `aliases` | Aliases this resource manages |
| `error` | The mismatch or failure message |
| `observedGeneration` | Spec generation the status reflects |
| `conditions[Ready]` | `CollectionReady` or `ImmutableFieldMismatch` |

## Behavior

On create, spec update, operator start and once a minute: resolve the
cluster, require `/readyz`, `PUT /collections/<name>` when the collection
does not exist, then compare the live `config` with the spec. An immutable
mismatch sets `Degraded` and stops. A mutable mismatch issues
`PATCH /collections/<name>`. Payload indexes are created, replaced when their
type or parameters differ, and deleted only when this resource created them
and the spec dropped them. Aliases are handled the same way through
`POST /collections/aliases`.

A cluster that is missing or not ready is retried every thirty seconds.
Any other error sets `Failed` with the message.

On delete with `deletionPolicy: Delete`, `DELETE /collections/<name>`; a
missing collection or a missing cluster completes the deletion silently.

Print columns: `CLUSTER`, `COLLECTION`, `PHASE`, `POINTS`, `HEALTH`, `AGE`.
