# Introduction

The Qdrant Operator runs [Qdrant](https://qdrant.tech), the vector search
engine, on Kubernetes. You declare a `QdrantCluster` and the operator installs
the official [qdrant-helm](https://github.com/qdrant/qdrant-helm) chart with
values derived from the spec, then reports StatefulSet readiness in the
resource status. Four more kinds manage what lives inside or around that
cluster: collections with their payload indexes and aliases, backups of every
node to S3-compatible storage, backups on a cron schedule, and restores from a
backup into a cluster.

## Helm underneath

The operator does not render Qdrant's StatefulSet itself. Each `QdrantCluster`
is one Helm release named `qdrant-<name>`, installed with
`helm upgrade --install` from the upstream chart repository, and
`spec.version` is both the Qdrant version and the chart version. A cluster you
already run from the chart translates to a `QdrantCluster` almost value for
value; [Helm values the operator renders](./reference/helm-values.md) lists
the mapping. What the chart cannot do, and the operator adds, is everything
that talks to Qdrant's HTTP API: collection reconciliation, snapshots and
recovery.

## The kinds

| Kind | Short name | What it manages |
|---|---|---|
| `QdrantCluster` | `qc` | One Helm release of the qdrant chart: replicas, version, storage, service, API keys, TLS, metrics, scheduling |
| `QdrantCollection` | `qcol` | A collection inside a cluster: vectors, sharding, payload indexes, aliases |
| `QdrantBackup` | `qb` | One backup: a snapshot of every node per collection, streamed into a bucket with a manifest |
| `QdrantBackupSchedule` | `qbs` | Backups on a cron schedule with CronJob-style concurrency and grandfather-father-son retention |
| `QdrantRestore` | `qr` | Recovery of a backup into a cluster with the same node count, node by node |

All kinds live in the API group `qdrant.io`, version `v1alpha1`, and are
namespaced.

## Every node, not one

Qdrant in distributed mode spreads a collection's shards across nodes, and a
snapshot taken on one node holds only that node's shards. The operator
therefore snapshots each StatefulSet pod through the chart's headless Service
and records which node every snapshot came from. A restore hands each target
node the snapshot of the matching source node, which is why the node counts of
source and target must be equal.

## Where to go next

[Installation](./installation.md) deploys the operator and its CRDs.
[Quickstart](./quickstart.md) creates a cluster, a collection and a backup.
The guide then takes one topic per chapter, from
[running a cluster](./guide/cluster.md) through [backups](./guide/backups.md)
to [troubleshooting](./guide/troubleshooting.md), and the reference section
has a field table for every kind.
