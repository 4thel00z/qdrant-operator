"""Prometheus metrics of the operator itself, served on their own port."""

from dataclasses import dataclass
from dataclasses import field

from prometheus_client import CollectorRegistry
from prometheus_client import Counter
from prometheus_client import Gauge
from prometheus_client import Histogram
from prometheus_client import start_http_server

from qdrant_operator.domain import ClusterPhase

DURATION_BUCKETS = (1, 5, 15, 60, 300, 900, 3600, 14400)


@dataclass
class OperatorMetrics:
    """Counters, durations and the cluster phase gauge, registered on an injectable registry."""

    registry: CollectorRegistry = field(default_factory=CollectorRegistry)

    def __post_init__(self) -> None:
        self.reconcile_errors = Counter(
            "qdrant_operator_reconcile_errors_total",
            "Failed handler invocations",
            ["kind"],
            registry=self.registry,
        )
        self.operations = Counter(
            "qdrant_operator_operations_total",
            "Finished backups, restores and migrations by outcome",
            ["kind", "outcome"],
            registry=self.registry,
        )
        self.operation_duration = Histogram(
            "qdrant_operator_operation_duration_seconds",
            "Wall time of backups, restores and migrations",
            ["kind"],
            buckets=DURATION_BUCKETS,
            registry=self.registry,
        )
        self.cluster_phase = Gauge(
            "qdrant_operator_cluster_phase",
            "1 for the phase a QdrantCluster is in, 0 for the others",
            ["namespace", "name", "phase"],
            registry=self.registry,
        )

    def reconcile_failed(self, kind: str) -> None:
        self.reconcile_errors.labels(kind).inc()

    def operation_finished(self, kind: str, outcome: str, seconds: float) -> None:
        self.operations.labels(kind, outcome).inc()
        self.operation_duration.labels(kind).observe(seconds)

    def set_cluster_phase(self, namespace: str, name: str, phase: ClusterPhase) -> None:
        for candidate in ClusterPhase:
            self.cluster_phase.labels(namespace, name, candidate.value).set(
                1 if candidate == phase else 0
            )

    def forget_cluster(self, namespace: str, name: str) -> None:
        for candidate in ClusterPhase:
            self.cluster_phase.remove(namespace, name, candidate.value)

    def serve(self, port: int) -> None:
        start_http_server(port, registry=self.registry)
