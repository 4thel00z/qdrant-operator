# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Kubernetes operator for the Qdrant vector database, written in Go with controller-runtime and the Helm v4 Go SDK (in-process, no helm CLI). Manages QdrantCluster, QdrantBackup, QdrantBackupSchedule, QdrantRestore, QdrantCollection, QdrantAccessKey and QdrantMigration custom resources.

## Commands

```bash
make dev                      # run the operator against the current kubeconfig, text logs
make build                    # bin/qdrant-operator
make test                     # go test ./... with the pinned envtest control plane
make lint                     # gofmt check, go vet (incl. integration and e2e tags), helm lint
make format                   # gofmt -w

# single package or test (envtest packages need KUBEBUILDER_ASSETS)
export KUBEBUILDER_ASSETS="$(make -s envtest)"
CGO_ENABLED=0 go test ./internal/controller/ -run TestCollection -count=1 -v

make test-integration         # Helm adapter against the current kubeconfig
make e2e-image e2e-deploy test-e2e   # build the image, install the chart on kind, run test/e2e; KIND_CLUSTER=<name> when not "kind"

make manifests                # regenerate deepcopy and CRDs from api/v1alpha1, then copy them into the chart
```

Always build and test with `CGO_ENABLED=0` (the Makefile exports it).

## Architecture

Hexagonal layout; dependencies point inwards:

```
api/v1alpha1/        CRD types with kubebuilder markers and field docs
internal/domain/     entities, value objects, pure rules (stdlib only)
internal/translate/  api/v1alpha1 <-> domain
internal/ports/      interfaces the use cases depend on
internal/adapters/   helm, qdrant (REST), s3, jwt, kubernetes, cron (gronx)
internal/fakes/      in-memory implementations of the ports for tests
internal/usecases/   application logic over ports
internal/controller/ one reconciler per kind; status writing, finalizers, events, metrics
internal/settings/   cobra flags + viper (env prefix QDRANT_OPERATOR_)
internal/app/        builds adapters and the manager, registers reconcilers
cmd/operator/        entry point (fang)
```

**Data flow**: Reconciler → use case → port → adapter → external system.

## Conventions

- Reconcilers call use cases; they never call adapters directly. The domain package imports nothing outside the standard library.
- No panics in production code: no `panic`, unchecked single-value type assertions or `Must*` helpers; return errors.
- Guard clauses instead of `else`; no `For`-suffixed names; effectively no comments beyond exported-API docs.
- Tests use `internal/fakes` and envtest, never mocks. Controller tests run a manager per test namespace through `startWorld` in `internal/controller/envtest_test.go` with `fastTiming`.
- Wrap errors at adapter and use-case layers only.
- Status is written with `Dependencies.WriteStatus`, which replaces the whole status with a JSON patch and records a phase change as an events.k8s.io Event (Warning for `Failed`).
- Error mapping in reconcilers: retryable → `RetryAfter`; context cancelled → `Interrupted`; permanent one-shot failure → Failed status and `Stop`; permanent failure of a level-triggered kind → Failed status and keep the interval.
- Finalizer `qdrant.io/finalizer` only on clusters, backups and collections.

## CRDs

Generated from `api/v1alpha1` into `manifests/crds/` by `make manifests`, which also copies them into `charts/qdrant-operator/crds/`; `test/chart` checks both directories match. CEL rules (`x-kubernetes-validations`) are exercised against a real API server in `test/admission` (envtest), with one rejecting case per rule.

## Chart

`charts/qdrant-operator`. `ha.enabled` turns on leader election through a Lease (`ha.leaseName`, `ha.leaseDuration`); without it more than one replica is refused. Settings reach the operator as `QDRANT_OPERATOR_*` env vars. `test/chart` renders the chart with the Helm v4 SDK and checks RBAC and settings.

## Git Workflow

For every accepted change, create a signed commit:

```bash
git add <files>
git commit -s -m "conventional commit description of changes"
```

Always use `git commit -s` to add the Signed-off-by line.
