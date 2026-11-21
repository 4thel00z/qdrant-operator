from prometheus_client import CollectorRegistry
from prometheus_client.exposition import generate_latest

from qdrant_operator.domain import ClusterPhase
from qdrant_operator.metrics import OperatorMetrics


def metrics() -> OperatorMetrics:
    return OperatorMetrics(registry=CollectorRegistry())


def test_reconcile_errors_count_per_kind() -> None:
    operator = metrics()

    operator.reconcile_failed("QdrantCluster")
    operator.reconcile_failed("QdrantCluster")

    value = operator.registry.get_sample_value(
        "qdrant_operator_reconcile_errors_total", {"kind": "QdrantCluster"}
    )
    assert value == 2


def test_operations_record_outcome_and_duration() -> None:
    operator = metrics()

    operator.operation_finished("QdrantBackup", "Completed", 12.5)
    operator.operation_finished("QdrantBackup", "Failed", 1.0)

    registry = operator.registry
    completed = {"kind": "QdrantBackup", "outcome": "Completed"}
    assert registry.get_sample_value("qdrant_operator_operations_total", completed) == 1
    assert (
        registry.get_sample_value(
            "qdrant_operator_operation_duration_seconds_sum", {"kind": "QdrantBackup"}
        )
        == 13.5
    )


def test_cluster_phase_gauge_is_one_hot_and_forgotten_on_delete() -> None:
    operator = metrics()

    operator.set_cluster_phase("ns", "db", ClusterPhase.PENDING)
    operator.set_cluster_phase("ns", "db", ClusterPhase.RUNNING)

    def phase(name: str) -> float | None:
        return operator.registry.get_sample_value(
            "qdrant_operator_cluster_phase", {"namespace": "ns", "name": "db", "phase": name}
        )

    assert (phase("Running"), phase("Pending")) == (1, 0)

    operator.forget_cluster("ns", "db")

    assert phase("Running") is None
    assert b"qdrant_operator_cluster_phase{" not in generate_latest(operator.registry)
