"""Wires adapters into use cases; built once in main and handed to every handler as kopf memo."""

from dataclasses import dataclass
from typing import Any
from typing import Self

from qdrant_operator.helm_adapter import HelmAdapter
from qdrant_operator.helm_adapter import post_renderer_path
from qdrant_operator.jwt_adapter import JwtAdapter
from qdrant_operator.kubernetes_adapter import KubernetesAdapter
from qdrant_operator.metrics import OperatorMetrics
from qdrant_operator.qdrant_adapter import QdrantAdapter
from qdrant_operator.s3_adapter import S3Adapter
from qdrant_operator.settings import Settings
from qdrant_operator.usecases import CleanupBackupSnapshots
from qdrant_operator.usecases import DeleteBackupData
from qdrant_operator.usecases import DeleteCluster
from qdrant_operator.usecases import DeleteCollection
from qdrant_operator.usecases import ExecuteBackup
from qdrant_operator.usecases import ExecuteMigration
from qdrant_operator.usecases import ExecuteRestore
from qdrant_operator.usecases import ExpireBackup
from qdrant_operator.usecases import IssueAccessKey
from qdrant_operator.usecases import ObserveCluster
from qdrant_operator.usecases import ProcessSchedule
from qdrant_operator.usecases import RebalanceShards
from qdrant_operator.usecases import ReconcileCluster
from qdrant_operator.usecases import ReconcileCollection
from qdrant_operator.usecases import RecoverDeadPeers
from qdrant_operator.usecases import StepRollout


@dataclass(frozen=True)
class Container:
    helm: HelmAdapter
    kubernetes: KubernetesAdapter
    storage: S3Adapter
    qdrant: QdrantAdapter
    token: JwtAdapter
    metrics: OperatorMetrics

    @classmethod
    def from_settings(cls, settings: Settings, configuration: Any) -> Self:
        return cls(
            helm=HelmAdapter(
                settings.helm,
                settings.kubernetes.kubeconfig,
                post_renderer_path(settings.helm.post_renderer),
            ),
            kubernetes=KubernetesAdapter(configuration),
            storage=S3Adapter(),
            qdrant=QdrantAdapter(settings.qdrant),
            token=JwtAdapter(),
            metrics=OperatorMetrics(),
        )

    def reconcile_cluster(self) -> ReconcileCluster:
        return ReconcileCluster(helm=self.helm, kubernetes=self.kubernetes, qdrant=self.qdrant)

    def step_rollout(self) -> StepRollout:
        return StepRollout(qdrant=self.qdrant, kubernetes=self.kubernetes)

    def observe_cluster(self) -> ObserveCluster:
        return ObserveCluster(kubernetes=self.kubernetes)

    def recover_dead_peers(self) -> RecoverDeadPeers:
        return RecoverDeadPeers(qdrant=self.qdrant, kubernetes=self.kubernetes)

    def rebalance_shards(self) -> RebalanceShards:
        return RebalanceShards(qdrant=self.qdrant, kubernetes=self.kubernetes)

    def delete_cluster(self) -> DeleteCluster:
        return DeleteCluster(helm=self.helm, kubernetes=self.kubernetes)

    def execute_backup(self) -> ExecuteBackup:
        return ExecuteBackup(qdrant=self.qdrant, storage=self.storage, kubernetes=self.kubernetes)

    def cleanup_backup_snapshots(self) -> CleanupBackupSnapshots:
        return CleanupBackupSnapshots(qdrant=self.qdrant, kubernetes=self.kubernetes)

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

    def issue_access_key(self) -> IssueAccessKey:
        return IssueAccessKey(kubernetes=self.kubernetes, token=self.token)

    def execute_migration(self) -> ExecuteMigration:
        return ExecuteMigration(qdrant=self.qdrant, kubernetes=self.kubernetes)
