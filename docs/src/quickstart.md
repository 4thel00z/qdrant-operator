# Quickstart

This walk-through creates a three-node cluster, declares a collection in it,
and takes a backup. It assumes the operator is [installed](./installation.md)
and that you have an S3 bucket or a MinIO instance at hand.

## Create a cluster

```yaml
apiVersion: qdrant.io/v1alpha1
kind: QdrantCluster
metadata:
  name: my-qdrant
spec:
  replicas: 3
  version: v1.16.3
  resources:
    requests: {cpu: 500m, memory: 1Gi}
    limits: {cpu: "2", memory: 4Gi}
  persistence:
    size: 10Gi
  apiKey:
    autoGenerate: true
```

```sh
kubectl apply -f my-qdrant.yaml
kubectl get qc my-qdrant -w
```

```
NAME        PHASE     REPLICAS   READY   VERSION   AGE
my-qdrant   Pending   3                  v1.16.3   4s
my-qdrant   Running   3          3       v1.16.3   71s
```

Behind the scenes the operator ran `helm upgrade --install qdrant-my-qdrant`
with the qdrant chart at version 1.16.3. `Running` means the StatefulSet
reports as many ready replicas as the spec asks for; the operator checks every
thirty seconds.

## Connect

The chart generated a Secret named `qdrant-my-qdrant-apikey` with the key
`api-key`. The Service address is in the status:

```sh
kubectl get qc my-qdrant -o jsonpath='{.status.endpoint}{"\n"}'
API_KEY=$(kubectl get secret qdrant-my-qdrant-apikey -o jsonpath='{.data.api-key}' | base64 -d)
kubectl port-forward svc/qdrant-my-qdrant 6333:6333 &
curl -H "api-key: $API_KEY" http://127.0.0.1:6333/collections
```

Inside the cluster, connect to `qdrant-my-qdrant.<namespace>.svc.cluster.local`
on port 6333 for HTTP and 6334 for gRPC.

## Declare a collection

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
  replicationFactor: 2
  payloadIndexes:
    - field: tenant
      type: keyword
```

```sh
kubectl apply -f documents.yaml
kubectl get qcol
```

```
NAME        CLUSTER     COLLECTION   PHASE   POINTS   HEALTH   AGE
documents   my-qdrant   documents    Ready   0        green    6s
```

The operator waited for the cluster to answer `/readyz`, created the
collection with `PUT /collections/documents`, and added the payload index.
It re-checks every minute, so a collection dropped by hand comes back.

## Take a backup

Put the bucket credentials in a Secret, then create a `QdrantBackup`:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: s3-credentials
stringData:
  AWS_ACCESS_KEY_ID: minioadmin
  AWS_SECRET_ACCESS_KEY: minioadmin
---
apiVersion: qdrant.io/v1alpha1
kind: QdrantBackup
metadata:
  name: first
spec:
  clusterRef: {name: my-qdrant}
  storage:
    s3:
      bucket: qdrant-backups
      prefix: my-qdrant
      endpoint: http://minio.minio.svc:9000
      forcePathStyle: true
      credentialsSecretRef: {name: s3-credentials}
```

```sh
kubectl apply -f backup.yaml
kubectl get qb first -w
```

```
NAME    CLUSTER     PHASE        SIZE     AGE
first   my-qdrant   InProgress            2s
first   my-qdrant   Completed    12.4MB   19s
```

The bucket now holds `my-qdrant/first/manifest.json` and one snapshot per
collection per node under `my-qdrant/first/<collection>/node-<i>/`.

## Next

- [Running a cluster](./guide/cluster.md) covers the rest of the
  `QdrantCluster` spec.
- [Collections](./guide/collections.md) explains what the operator can and
  cannot change on a live collection.
- [Scheduled backups](./guide/schedules.md) and [Restore](./guide/restore.md)
  complete the backup story.
