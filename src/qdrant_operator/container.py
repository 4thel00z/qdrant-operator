"""Dependency injection container.

Wires adapters to use cases for the hexagonal architecture.
"""

from dataclasses import dataclass

from qdrant_operator.helm_adapter import HelmAdapter
from qdrant_operator.kubernetes_adapter import KubernetesAdapter
from qdrant_operator.qdrant_adapter import QdrantAdapter
from qdrant_operator.s3_adapter import S3Adapter
from qdrant_operator.usecases import (
    DeleteCluster,
    ExecuteBackup,
    ExecuteRestore,
    ProcessSchedule,
    ReconcileCluster,
)


@dataclass
class Container:
    """Dependency injection container for the operator."""

    def helm_adapter(self) -> HelmAdapter:
        """Create Helm adapter."""
        return HelmAdapter()

    def kubernetes_adapter(self) -> KubernetesAdapter:
        """Create Kubernetes adapter."""
        return KubernetesAdapter()

    def s3_adapter(self) -> S3Adapter:
        """Create S3 adapter."""
        return S3Adapter()

    def qdrant_adapter(self, endpoint: str, api_key: str | None = None) -> QdrantAdapter:
        """Create Qdrant adapter with endpoint and optional API key."""
        return QdrantAdapter(endpoint=endpoint, api_key=api_key)

    def reconcile_cluster(self) -> ReconcileCluster:
        """Create ReconcileCluster use case."""
        return ReconcileCluster(
            helm=self.helm_adapter(),
            kubernetes=self.kubernetes_adapter(),
        )

    def delete_cluster(self) -> DeleteCluster:
        """Create DeleteCluster use case."""
        return DeleteCluster(helm=self.helm_adapter())

    def execute_backup(self, endpoint: str, api_key: str | None = None) -> ExecuteBackup:
        """Create ExecuteBackup use case."""
        return ExecuteBackup(
            qdrant=self.qdrant_adapter(endpoint, api_key),
            storage=self.s3_adapter(),
            kubernetes=self.kubernetes_adapter(),
        )

    def execute_restore(self, endpoint: str, api_key: str | None = None) -> ExecuteRestore:
        """Create ExecuteRestore use case."""
        return ExecuteRestore(
            qdrant=self.qdrant_adapter(endpoint, api_key),
            storage=self.s3_adapter(),
            kubernetes=self.kubernetes_adapter(),
        )

    def process_schedule(self) -> ProcessSchedule:
        """Create ProcessSchedule use case."""
        return ProcessSchedule(kubernetes=self.kubernetes_adapter())