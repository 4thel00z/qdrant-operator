"""Wires adapters into use cases. Adapters hold no state, so one container per event is fine."""

from dataclasses import dataclass
from dataclasses import field

from qdrant_operator.helm_adapter import HelmAdapter
from qdrant_operator.kubernetes_adapter import KubernetesAdapter
from qdrant_operator.qdrant_adapter import QdrantAdapter
from qdrant_operator.s3_adapter import S3Adapter
from qdrant_operator.usecases import DeleteBackupData
from qdrant_operator.usecases import DeleteCluster
from qdrant_operator.usecases import DeleteCollection
from qdrant_operator.usecases import ExecuteBackup
from qdrant_operator.usecases import ExecuteRestore
from qdrant_operator.usecases import ExpireBackup
from qdrant_operator.usecases import ObserveCluster
from qdrant_operator.usecases import ProcessSchedule
from qdrant_operator.usecases import ReconcileCluster
from qdrant_operator.usecases import ReconcileCollection


@dataclass
class Container:
    helm: HelmAdapter = field(default_factory=HelmAdapter)
    kubernetes: KubernetesAdapter = field(default_factory=KubernetesAdapter)
    storage: S3Adapter = field(default_factory=S3Adapter)
    qdrant: QdrantAdapter = field(default_factory=QdrantAdapter)

    def reconcile_cluster(self) -> ReconcileCluster:
        return ReconcileCluster(helm=self.helm)

    def observe_cluster(self) -> ObserveCluster:
        return ObserveCluster(kubernetes=self.kubernetes)

    def delete_cluster(self) -> DeleteCluster:
        return DeleteCluster(helm=self.helm)

    def execute_backup(self) -> ExecuteBackup:
        return ExecuteBackup(qdrant=self.qdrant, storage=self.storage, kubernetes=self.kubernetes)

    def delete_backup_data(self) -> DeleteBackupData:
        return DeleteBackupData(storage=self.storage, kubernetes=self.kubernetes)

    def expire_backup(self) -> ExpireBackup:
        return ExpireBackup(kubernetes=self.kubernetes)

    def execute_restore(self) -> ExecuteRestore:
        return ExecuteRestore(qdrant=self.qdrant, storage=self.storage, kubernetes=self.kubernetes)

    def process_schedule(self) -> ProcessSchedule:
        return ProcessSchedule(kubernetes=self.kubernetes)

    def reconcile_collection(self) -> ReconcileCollection:
        return ReconcileCollection(qdrant=self.qdrant, kubernetes=self.kubernetes)

    def delete_collection(self) -> DeleteCollection:
        return DeleteCollection(qdrant=self.qdrant, kubernetes=self.kubernetes)
