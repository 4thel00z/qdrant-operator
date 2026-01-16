"""Port interfaces using typing.Protocol.

Ports define the boundaries between the application and external systems.
All methods are async-first as required by kopf operator patterns.
"""

from typing import Protocol

from qdrant_operator.domain import (
    BackupStatus,
    ClusterSpec,
    ClusterStatus,
    S3StorageSpec,
    SecretRef,
    Snapshot,
)


class HelmPort(Protocol):
    """Port for Helm chart operations."""

    async def install(
        self,
        release_name: str,
        namespace: str,
        chart: str,
        values: dict,
        version: str | None = None,
    ) -> str:
        """Install a Helm release. Returns release name."""
        ...

    async def upgrade(
        self,
        release_name: str,
        namespace: str,
        chart: str,
        values: dict,
        version: str | None = None,
    ) -> str:
        """Upgrade an existing Helm release. Returns release name."""
        ...

    async def uninstall(self, release_name: str, namespace: str) -> None:
        """Uninstall a Helm release."""
        ...

    async def get_release_status(
        self, release_name: str, namespace: str
    ) -> dict | None:
        """Get status of a Helm release. Returns None if not found."""
        ...


class QdrantPort(Protocol):
    """Port for Qdrant REST API operations."""

    async def list_collections(self, endpoint: str, api_key: str | None = None) -> list[str]:
        """List all collection names."""
        ...

    async def create_snapshot(
        self, endpoint: str, collection: str, api_key: str | None = None
    ) -> Snapshot:
        """Create a snapshot of a collection."""
        ...

    async def list_snapshots(
        self, endpoint: str, collection: str, api_key: str | None = None
    ) -> list[Snapshot]:
        """List snapshots for a collection."""
        ...

    async def delete_snapshot(
        self, endpoint: str, collection: str, snapshot_name: str, api_key: str | None = None
    ) -> None:
        """Delete a snapshot."""
        ...

    async def download_snapshot(
        self,
        endpoint: str,
        collection: str,
        snapshot_name: str,
        destination: str,
        api_key: str | None = None,
    ) -> str:
        """Download a snapshot to local path. Returns file path."""
        ...

    async def recover_from_snapshot(
        self,
        endpoint: str,
        collection: str,
        snapshot_path: str,
        api_key: str | None = None,
    ) -> None:
        """Recover a collection from a snapshot file."""
        ...

    async def get_collection_info(
        self, endpoint: str, collection: str, api_key: str | None = None
    ) -> dict:
        """Get collection info including point count and status."""
        ...

    async def health_check(self, endpoint: str) -> bool:
        """Check if Qdrant is healthy and ready."""
        ...


class StoragePort(Protocol):
    """Port for S3-compatible object storage operations."""

    async def upload_file(
        self,
        storage: S3StorageSpec,
        credentials: tuple[str, str],
        local_path: str,
        remote_key: str,
    ) -> str:
        """Upload a file to storage. Returns the full S3 path."""
        ...

    async def download_file(
        self,
        storage: S3StorageSpec,
        credentials: tuple[str, str],
        remote_key: str,
        local_path: str,
    ) -> str:
        """Download a file from storage. Returns local path."""
        ...

    async def delete_file(
        self,
        storage: S3StorageSpec,
        credentials: tuple[str, str],
        remote_key: str,
    ) -> None:
        """Delete a file from storage."""
        ...

    async def list_files(
        self,
        storage: S3StorageSpec,
        credentials: tuple[str, str],
        prefix: str,
    ) -> list[str]:
        """List files under a prefix."""
        ...

    async def file_exists(
        self,
        storage: S3StorageSpec,
        credentials: tuple[str, str],
        remote_key: str,
    ) -> bool:
        """Check if a file exists in storage."""
        ...


class KubernetesPort(Protocol):
    """Port for Kubernetes API operations."""

    async def get_secret_value(self, secret_ref: SecretRef) -> str:
        """Get a value from a Kubernetes Secret."""
        ...

    async def update_status(
        self,
        group: str,
        version: str,
        plural: str,
        name: str,
        namespace: str,
        status: dict,
    ) -> None:
        """Update the status subresource of a custom resource."""
        ...

    async def create_resource(
        self,
        group: str,
        version: str,
        plural: str,
        namespace: str,
        body: dict,
    ) -> dict:
        """Create a custom resource."""
        ...

    async def get_resource(
        self,
        group: str,
        version: str,
        plural: str,
        name: str,
        namespace: str,
    ) -> dict | None:
        """Get a custom resource. Returns None if not found."""
        ...

    async def list_resources(
        self,
        group: str,
        version: str,
        plural: str,
        namespace: str,
        label_selector: str | None = None,
    ) -> list[dict]:
        """List custom resources in a namespace."""
        ...

    async def delete_resource(
        self,
        group: str,
        version: str,
        plural: str,
        name: str,
        namespace: str,
    ) -> None:
        """Delete a custom resource."""
        ...

    async def get_service_endpoint(
        self,
        name: str,
        namespace: str,
        port: int = 6333,
    ) -> str:
        """Get the internal endpoint URL for a service."""
        ...


class ClusterUseCase(Protocol):
    """Use case interface for cluster operations."""

    async def reconcile(self, spec: ClusterSpec) -> ClusterStatus:
        """Reconcile a QdrantCluster to desired state."""
        ...

    async def delete(self, spec: ClusterSpec) -> None:
        """Delete a QdrantCluster and its resources."""
        ...


class BackupUseCase(Protocol):
    """Use case interface for backup operations."""

    async def execute(
        self,
        name: str,
        namespace: str,
        cluster_endpoint: str,
        storage: S3StorageSpec,
        collections: tuple[str, ...],
        api_key: str | None = None,
    ) -> BackupStatus:
        """Execute a backup operation."""
        ...