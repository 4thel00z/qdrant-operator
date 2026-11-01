# Development

The operator is Python 3.12 on [kopf](https://kopf.readthedocs.io), managed
with `uv`. The package is `qdrant_operator`.

## Layout

| Path | Contents |
|---|---|
| `src/qdrant_operator/domain.py` | Frozen dataclasses for every spec and status, `from_dict`/`to_dict`, Helm value derivation, cron and retention rules. No I/O, no framework imports |
| `src/qdrant_operator/ports.py` | `typing.Protocol` interfaces: `HelmPort`, `QdrantPort`, `StoragePort`, `KubernetesPort`, `TokenPort` |
| `src/qdrant_operator/usecases.py` | One dataclass per operation: `ReconcileCluster`, `ObserveCluster`, `DeleteCluster`, `ExecuteBackup`, `DeleteBackupData`, `ExpireBackup`, `ExecuteRestore`, `ProcessSchedule`, `ReconcileCollection`, `DeleteCollection`, `IssueAccessKey`, `ExecuteMigration` |
| `src/qdrant_operator/handlers.py` | kopf handlers: event to dataclasses to use case to `patch.status` |
| `src/qdrant_operator/*_adapter.py` | Helm CLI, Qdrant REST over httpx, S3 over aioboto3, Kubernetes over kubernetes-asyncio, JWT signing over PyJWT |
| `src/qdrant_operator/container.py` | Wires adapters into use cases |
| `src/qdrant_operator/main.py` | Entry point, loguru setup, `kopf run` |
| `manifests/crds/` | The CRDs; `charts/qdrant-operator/crds/` must be an identical copy |
| `charts/qdrant-operator/` | The operator's own chart |
| `tests/` | Unit tests with in-memory fakes, CRD sync check, integration suite |
| `docs/` | This book |

Rules the code follows: handlers call use cases only, use cases see ports
only, adapters implement ports structurally. Status goes through
`patch.status.update`. Transient problems raise `kopf.TemporaryError`,
everything else writes `Failed` and raises `kopf.PermanentError`. Adapters
keep no connection state and build a client per call. No temp files:
snapshots stream node to bucket and bucket to node, Helm values go over
stdin.

## Make targets

```sh
make dev                # run against the current kubeconfig, text logs
make lint               # ruff check, ruff format --check, pyright --strict, helm lint
make format
make test               # unit tests
make test-integration   # QDRANT_OPERATOR_INTEGRATION=1, needs a cluster and helm
make e2e-image          # build qdrant-operator:e2e
make e2e-deploy         # kind load the image and install the chart (KIND_CLUSTER=kind)
make test-e2e           # QDRANT_OPERATOR_E2E=1, needs the deployed operator and helm
make crds-sync          # copy manifests/crds into the chart
make build              # docker image
```

## Tests

`tests/fakes.py` holds `FakeHelm`, `FakeQdrant`, `FakeStorage` and
`FakeKubernetes`, in-memory implementations of the ports that record calls;
there is no `mock.patch`. `test_domain.py` and `test_usecases.py` run
against those in well under a second and cover value derivation, cron and
retention arithmetic, the backup and restore flows, schedule concurrency,
collection reconciliation, token claims and renewal, and the migration copy
loop. `test_crds.py` fails when the chart's CRDs differ
from `manifests/crds`.

`test_integration.py` runs against a real cluster with `helm` on the path and
the CRDs applied: it installs a one-node cluster through `ReconcileCluster`,
waits for `Running`, port-forwards and queries Qdrant, uninstalls, and checks
that the API server enforces the admission rules. It runs the use cases in
the test process with the admin kubeconfig.

`test_e2e.py` runs against the operator itself: the image built from the
checkout, installed by its chart and running under the chart's
ServiceAccount. It applies a `QdrantCluster`, a `QdrantCollection`, a
`QdrantAccessKey`, a `QdrantBackup` into a RustFS bucket and a
`QdrantRestore`, waits for each phase, checks the result in Qdrant and the
bucket, deletes them again and fails if the operator pod restarted or logged
a handler that failed permanently. Missing RBAC verbs, Helm or S3 problems
inside the pod and status patches the API server rejects all fail here and
not in the integration suite.

```sh
kind create cluster
make e2e-image e2e-deploy test-e2e
```

CI runs both suites on separate kind clusters on every push and pull
request.

## Releasing

Releases are cut by release-please from the conventional commit history. A
release PR keeps `pyproject.toml`, `Chart.yaml`, `uv.lock` and
`CHANGELOG.md` current; merging it tags `vX.Y.Z`, publishes the wheel and
sdist to PyPI, pushes the multi-arch image and the Helm chart to
`ghcr.io/4thel00z`, and attaches the CRDs to the GitHub release.

## Documentation

```sh
mdbook serve docs --open
```

The book publishes to GitHub Pages from the `book` workflow on every push to
`main`.
