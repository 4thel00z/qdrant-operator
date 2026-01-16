# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Kubernetes operator for Qdrant vector database using kopf framework. Manages QdrantCluster, QdrantBackup, QdrantBackupSchedule, and QdrantRestore custom resources.

## Commands

```bash
# Install dependencies
uv sync

# Run operator locally
uv run kopf run src/qdrant_operator/main.py --verbose

# Run tests
uv run pytest
uv run pytest tests/test_domain.py -v        # single file
uv run pytest -k "test_backup" -v            # by name pattern

# Lint and format
uv run ruff check src tests
uv run ruff format src tests
uv run pyright src

# Apply CRDs to cluster
kubectl apply -f manifests/crds/
```

## Architecture

DDD/Hexagonal architecture with flat file structure:

```
src/qdrant_operator/
├── main.py              # Entry point
├── domain.py            # Entities, value objects (no deps)
├── ports.py             # typing.Protocol interfaces
├── usecases.py          # Application logic (orchestrates ports)
├── handlers.py          # Kopf handlers (driving adapters)
├── container.py         # Dependency injection
├── helm_adapter.py      # Driven adapter: Helm CLI
├── qdrant_adapter.py    # Driven adapter: Qdrant REST API
├── s3_adapter.py        # Driven adapter: S3/MinIO
└── kubernetes_adapter.py # Driven adapter: K8s API
```

**Data flow**: Handler → UseCase → Port → Adapter → External System

## Coding Conventions

**Python Style:**
- Target Python 3.12+
- Async-first: all I/O operations must be async
- Use `typing.Protocol` for ports (no `abc.ABC`)
- No underscore-prefixed methods (no `_private`)
- No `__future__` imports
- Use dataclasses for entities and use cases
- Modern type hints: `str | None`, `list[str]` (not `Optional`, `List`)

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
| structlog | Structured logging |

## CRDs

Located in `manifests/crds/`:
- `qdrantcluster-crd.yaml` - Qdrant cluster management
- `qdrantbackup-crd.yaml` - Point-in-time backups
- `qdrantbackupschedule-crd.yaml` - Scheduled backups
- `qdrantrestore-crd.yaml` - Restore from backup

## Git Workflow

For every accepted change, create a signed commit:

```bash
git add <files>
git commit -s -m "conventional commit description of changes"
```

Always use `git commit -s` to add Signed-off-by line.