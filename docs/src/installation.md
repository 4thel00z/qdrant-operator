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

The chart creates, in the release namespace, a Deployment with one replica (see [High availability](#high-availability)), a
ServiceAccount, and a ClusterRole plus binding. The ClusterRole covers the
`qdrant.io` kinds and their status subresources, the objects the qdrant chart
renders (StatefulSets, Services, ConfigMaps, Secrets, ServiceAccounts,
PodDisruptionBudgets, Ingresses, ServiceMonitors), Events, and kopf's own
peering resource. By default the Deployment uses the `Recreate` strategy, because
two operator replicas would race on the same Helm releases.

## High availability

Several replicas are safe only with kopf peering, which the chart turns on with
`ha.enabled`:

```sh
helm install qdrant-operator oci://ghcr.io/4thel00z/charts/qdrant-operator \
  --namespace qdrant-system --create-namespace \
  --set ha.enabled=true --set replicaCount=2
```

The chart then ships the kopf peering CRD and a peering object, starts the
operator with `--peering <name> --priority <n>`, and switches the Deployment to
`RollingUpdate` with `maxSurge: 1` and `maxUnavailable: 0`, so a standby exists
during rollouts. `replicaCount` above one without `ha.enabled` fails to render.

Kopf pauses every operator but the one with the highest priority, and it pauses
all of them when the top priorities are equal. Each pod therefore draws a random
priority between 1 and 2147483647 at startup (or takes `PEERING_PRIORITY`). A
restarted pod draws a new one, so the active pod can change after a rollout.
Pods missing the peering object or CRD refuse to start instead of falling back
to standalone.

Failover time is the peering lifetime, `ha.lifetime`, 60 seconds by default. A
pod that shuts down cleanly removes its record and a standby resumes at once; a
pod that is killed or loses its node is waited out for up to the lifetime. A
shorter lifetime means faster failover and more keep-alive patches. Kopf has no
per-object locks, so a handler already running in the paused pod finishes while
the new active pod starts, so a brief overlap is possible.

With `ha.namespaces` set, the operator watches only those namespaces
(`--namespace` for each), the chart ships the namespaced `KopfPeering` CRD and
creates the peering object in each listed namespace. Left empty, a
`ClusterKopfPeering` is used. Check which pod is active with
`kubectl get clusterkopfpeering` (or `kubectl get kopfpeering -n <ns>`) and
read `status`: the live pod with the highest priority is the active one.

The peering CRDs are cluster-scoped, so install only one release per cluster
with `ha.enabled`.

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
