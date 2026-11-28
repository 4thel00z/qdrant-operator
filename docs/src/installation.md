# Installation

The operator needs Kubernetes 1.26 or newer and a Helm 3 client for anything
you run from your machine. The operator runs Helm in-process through the Helm Go SDK, so its image has
no `helm` binary and needs no Tiller, plugin or repository cache.

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
  --set operator.logLevel=debug \
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

Each GitHub release attaches `qdrant-operator` binaries for linux and darwin
on amd64 and arm64, and the seven CRD files. Apply the CRDs, then run the
binary against your current kubeconfig:

```sh
git clone https://github.com/4thel00z/qdrant-operator
kubectl apply -f qdrant-operator/manifests/crds/
qdrant-operator
```

Or from a checkout, `make dev`, which runs `go run ./cmd/operator --logging-format text`.

Settings are flags, and each has an environment variable, see
[Operator configuration](./reference/operator-configuration.md#command-line-and-environment).
`qdrant-operator --manager-namespaces team-a` watches one namespace instead of
all. No `helm` binary is needed; the chart is pulled by the operator.

## What gets installed

The chart creates, in the release namespace, a Deployment with one replica (see [High availability](#high-availability)), a
ServiceAccount, and a ClusterRole plus binding. The rules for the `qdrant.io`
kinds and their status subresources, the objects the qdrant chart renders
(StatefulSets, Services, ConfigMaps, Secrets, ServiceAccounts,
PodDisruptionBudgets, ServiceMonitors) and Events sit in the ClusterRole, or in
a Role per namespace with `watchNamespaces`; each has the verbs Helm and the
operator call and no wildcards. With `ha.enabled` a Role and binding for the
leader election Lease are added.
By default the Deployment uses the `Recreate` strategy, because two operator
replicas would race on the same Helm releases.

## High availability

Several replicas are safe only with leader election, which the chart turns on
with `ha.enabled`:

```sh
helm install qdrant-operator oci://ghcr.io/4thel00z/charts/qdrant-operator \
  --namespace qdrant-system --create-namespace \
  --set ha.enabled=true --set replicaCount=2
```

The chart then starts the operator with leader election on, creates a Role for
the Lease in the release namespace, and switches the Deployment to
`RollingUpdate` with `maxSurge: 1` and `maxUnavailable: 0`, so a standby exists
during rollouts. `replicaCount` above one without `ha.enabled` fails to render.

One replica holds a Lease named `ha.leaseName` (the release full name by
default) and reconciles; the others wait as standbys. A pod that shuts down
normally releases the Lease and a standby takes over at once. A pod that is
killed or loses its node is waited out for up to `ha.leaseDuration`, 15 seconds
by default. A shorter duration means faster failover and more Lease renewals.
Check which pod is active with
`kubectl get lease -n qdrant-system <name> -o jsonpath='{.spec.holderIdentity}'`.

The Lease name is fixed per release; to run two releases that watch different
namespaces, give each its own `ha.leaseName`.

## Watching only some namespaces

By default the operator watches every namespace. To limit it, list the
namespaces in `watchNamespaces`:

```sh
helm install qdrant-operator oci://ghcr.io/4thel00z/charts/qdrant-operator \
  --namespace qdrant-system --create-namespace \
  --set 'watchNamespaces={team-a,team-b}'
```

The chart sets `QDRANT_OPERATOR_MANAGER_NAMESPACES` to the comma-separated list, and the operator's caches cover only those namespaces. The rules above
move into a Role and RoleBinding in each listed namespace. The ClusterRole shrinks to
`get` on PersistentVolumes and `list` on Nodes, which are cluster-scoped. The listed namespaces must exist before the install; the
operator no longer creates namespaces.

## Verifying

```sh
kubectl get pods -n qdrant-system -l app.kubernetes.io/name=qdrant-operator
kubectl get crd | grep qdrant.io
```

Seven CRDs are installed: `qdrantclusters`, `qdrantcollections`,
`qdrantaccesskeys`, `qdrantbackups`, `qdrantbackupschedules`,
`qdrantrestores` and `qdrantmigrations`. The pod
answers `/healthz` and `/readyz` on port 8080.

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
