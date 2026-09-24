# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Kubernetes operator for Qdrant vector database using kopf framework. Manages QdrantCluster, QdrantBackup, QdrantBackupSchedule, QdrantRestore, QdrantCollection, QdrantAccessKey and QdrantMigration custom resources.

## Commands

```bash
# Install dependencies
uv sync

# Run operator locally (uses your kubeconfig)
make dev

# Run tests
uv run pytest
uv run pytest tests/test_domain.py -v        # single file
uv run pytest -k "test_backup" -v            # by name pattern

# Lint and format
uv run ruff check src tests
uv run ruff format src tests
uv run pyright src tests

# Apply CRDs to cluster
kubectl apply -f manifests/crds/
```

## Architecture

DDD/Hexagonal architecture with flat file structure:

```
src/qdrant_operator/
├── main.py              # Entry point, logging setup
├── domain.py            # Entities, value objects, from_dict/to_dict, pure rules (no deps)
├── ports.py             # typing.Protocol interfaces
├── usecases.py          # Application logic (orchestrates ports)
├── handlers.py          # Kopf handlers (driving adapters)
├── container.py         # Dependency injection
├── helm_adapter.py      # Driven adapter: Helm CLI
├── qdrant_adapter.py    # Driven adapter: Qdrant REST API
├── s3_adapter.py        # Driven adapter: S3/MinIO
├── jwt_adapter.py       # Driven adapter: HS256 token signing (PyJWT)
└── kubernetes_adapter.py # Driven adapter: K8s API
```

**Data flow**: Handler → UseCase → Port → Adapter → External System

Tests use the in-memory fakes in `tests/fakes.py`; never `mock.patch`.

## Coding Conventions

**Python Style:**
- Target Python 3.12+
- Async-first: all I/O operations must be async
- Use `typing.Protocol` for ports (no `abc.ABC`)
- No underscore-prefixed methods (no `_private`)
- No `__future__` imports
- Use dataclasses for entities and use cases
- Modern type hints: `str | None`, `list[str]` (not `Optional`, `List`)
- One import per line (`ruff` isort `force-single-line`); guard clauses instead of if/else
- Fully annotated; `pyright --strict` must pass for `src` and `tests`
- Write status with `patch.status.update(...)`; a handler's return value lands under `status.<handler-id>`

**Architecture Rules:**
- Handlers call use cases only (never adapters directly)
- Use cases depend on ports (Protocol interfaces)
- Adapters implement ports
- Domain has zero external dependencies

**Async Rules (kopf):**
- Never use blocking calls (`time.sleep`, sync HTTP)
- All adapters must use async libraries (httpx, aioboto3, kubernetes-asyncio)
- Async handlers provide full stack traces for debugging

## Dependencies

| Package | Purpose |
|---------|---------|
| kopf | Operator framework |
| kubernetes-asyncio | Async K8s client |
| httpx | Async HTTP for Qdrant API |
| aioboto3 | Async S3 client |
| croniter | Cron parsing for schedules |
| loguru | Logging (kopf's stdlib logs are intercepted into it) |
| pyjwt | Signing QdrantAccessKey tokens |

## CRDs

Source of truth is `manifests/crds/`; `make crds-sync` copies them into the chart and a test checks they match:
- `qdrantcluster-crd.yaml` - Qdrant cluster management
- `qdrantbackup-crd.yaml` - Point-in-time backups
- `qdrantbackupschedule-crd.yaml` - Scheduled backups
- `qdrantrestore-crd.yaml` - Restore from backup
- `qdrantcollection-crd.yaml` - Declarative collections, payload indexes, aliases
- `qdrantaccesskey-crd.yaml` - JWT tokens written into Secrets
- `qdrantmigration-crd.yaml` - Point-by-point copy between clusters

CEL rules live in the CRDs (`x-kubernetes-validations`); they only compile on a real API server, so
`tests/test_integration.py` dry-run applies invalid objects on kind. Level-triggered kinds
(collection, access key) share one handler for create/update/resume plus a timer and report
unapplyable specs through a `Ready=False` condition rather than an exception.

## Git Workflow

For every accepted change, create a signed commit:

```bash
git add <files>
git commit -s -m "conventional commit description of changes"
```

Always use `git commit -s` to add Signed-off-by line.