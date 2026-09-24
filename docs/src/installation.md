# Installation

The operator needs Kubernetes 1.26 or newer and a Helm 3 client for anything
you run from your machine. Inside the cluster the operator image ships its own
`helm` binary and needs no Tiller, plugin or repository cache.

## With Helm

The chart is published to the GitHub container registry on every release:

```sh
helm install qdrant-operator oci://ghcr.io/4thel00z/charts/qdrant-operator \
  --namespace qdrant-system --create-namespace
```

The chart carries the seven CRDs in its `crds/` directory, so a fresh install
creates them. Helm does not upgrade CRDs on `helm upgrade`; apply them
yourself when moving to a new version, see
[Upgrading the operator](./guide/upgrades.md#upgrading-the-operator).

Values worth setting are listed under
[Operator configuration](./reference/operator-configuration.md). For example:

```sh
helm install qdrant-operator oci://ghcr.io/4thel00z/charts/qdrant-operator \
  --namespace qdrant-system --create-namespace \
  --set operator.logLevel=DEBUG \
  --set operator.logFormat=text
```

## From the repository

```sh
git clone https://github.com/4thel00z/qdrant-operator
cd qdrant-operator
make crds-install
make install IMAGE_TAG=<version>
```

`make install` runs `helm upgrade --install` on `charts/qdrant-operator` into
the `qdrant-system` namespace with the image from `ghcr.io/4thel00z/qdrant-operator`.

## Outside the cluster

The operator is also a Python package. The `qdrant-operator` command runs it
against your current kubeconfig, with the CRDs applied to the cluster first:

```sh
git clone https://github.com/4thel00z/qdrant-operator
kubectl apply -f qdrant-operator/manifests/crds/
uv tool install qdrant-operator   # or: pipx install qdrant-operator
qdrant-operator
```

Each GitHub release also attaches the seven CRD files as assets, so a pinned
version can be applied without a clone.

Extra arguments go to `kopf run` and override the defaults, so
`qdrant-operator --namespace team-a` watches one namespace instead of all.
This mode needs `helm` on your `PATH`.

## What gets installed

The chart creates, in the release namespace, a Deployment with one replica, a
ServiceAccount, and a ClusterRole plus binding. The ClusterRole covers the
`qdrant.io` kinds and their status subresources, the objects the qdrant chart
renders (StatefulSets, Services, ConfigMaps, Secrets, ServiceAccounts,
PodDisruptionBudgets, Ingresses, ServiceMonitors), Events, and kopf's own
peering resource. The Deployment uses the `Recreate` strategy, because two
operator replicas would race on the same Helm releases.

## Verifying

```sh
kubectl get pods -n qdrant-system -l app.kubernetes.io/name=qdrant-operator
kubectl get crd | grep qdrant.io
```

Seven CRDs are installed: `qdrantclusters`, `qdrantcollections`,
`qdrantaccesskeys`, `qdrantbackups`, `qdrantbackupschedules`,
`qdrantrestores` and `qdrantmigrations`. The pod
answers liveness probes on `/healthz` port 8080.

## Uninstalling

```sh
helm uninstall qdrant-operator --namespace qdrant-system
```

Helm leaves the CRDs in place. Deleting them deletes every custom resource,
and with the operator gone nobody runs the finalizers, so uninstall the
`QdrantCluster` resources first if you want their Helm releases removed, and
keep in mind that deleting a `QdrantBackup` while the operator runs deletes
its objects in the bucket.

```sh
kubectl delete -f manifests/crds/
```
