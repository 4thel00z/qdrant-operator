package controller

import (
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/4thel00z/qdrant-operator/internal/domain"
)

var (
	durationBuckets = []float64{1, 5, 15, 60, 300, 900, 3600, 14400}
	clusterPhases   = []domain.ClusterPhase{
		domain.PhasePending,
		domain.PhaseRunning,
		domain.PhaseFailed,
		domain.PhaseUpgrading,
		domain.PhaseTerminating,
	}
)

// Metrics are the operator's own Prometheus series, with the names the Python operator exported.
type Metrics struct {
	ReconcileErrors   *prometheus.CounterVec
	Operations        *prometheus.CounterVec
	OperationDuration *prometheus.HistogramVec
	ClusterPhase      *prometheus.GaugeVec
}

func NewMetrics(registerer prometheus.Registerer) (*Metrics, error) {
	metrics := &Metrics{
		ReconcileErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "qdrant_operator_reconcile_errors_total",
			Help: "Failed handler invocations",
		}, []string{"kind"}),
		Operations: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "qdrant_operator_operations_total",
			Help: "Finished backups, restores and migrations by outcome",
		}, []string{"kind", "outcome"}),
		OperationDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "qdrant_operator_operation_duration_seconds",
			Help:    "Wall time of backups, restores and migrations",
			Buckets: durationBuckets,
		}, []string{"kind"}),
		ClusterPhase: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "qdrant_operator_cluster_phase",
			Help: "1 for the phase a QdrantCluster is in, 0 for the others",
		}, []string{"namespace", "name", "phase"}),
	}
	collectors := []prometheus.Collector{metrics.ReconcileErrors, metrics.Operations, metrics.OperationDuration, metrics.ClusterPhase}
	for _, collector := range collectors {
		if err := registerer.Register(collector); err != nil {
			return nil, fmt.Errorf("register operator metrics: %w", err)
		}
	}
	return metrics, nil
}

func (m *Metrics) ReconcileFailed(kind domain.ResourceKind) {
	m.ReconcileErrors.WithLabelValues(kind.Kind).Inc()
}

func (m *Metrics) OperationFinished(kind domain.ResourceKind, outcome string, elapsed time.Duration) {
	m.Operations.WithLabelValues(kind.Kind, outcome).Inc()
	m.OperationDuration.WithLabelValues(kind.Kind).Observe(elapsed.Seconds())
}

func (m *Metrics) SetClusterPhase(namespace, name string, phase domain.ClusterPhase) {
	for _, candidate := range clusterPhases {
		value := 0.0
		if candidate == phase {
			value = 1
		}
		m.ClusterPhase.WithLabelValues(namespace, name, string(candidate)).Set(value)
	}
}

func (m *Metrics) ForgetCluster(namespace, name string) {
	for _, candidate := range clusterPhases {
		m.ClusterPhase.DeleteLabelValues(namespace, name, string(candidate))
	}
}
