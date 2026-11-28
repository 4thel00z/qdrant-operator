package usecases

import (
	"context"
	"fmt"

	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/ports"
)

// ObserveCluster lifts StatefulSet readiness into the QdrantCluster status.
type ObserveCluster struct {
	Kubernetes ports.Kubernetes
	Clock      Clock
}

func (o ObserveCluster) Execute(ctx context.Context, spec domain.ClusterSpec, current domain.ClusterStatus) (domain.ClusterStatus, error) {
	statefulSet, err := o.Kubernetes.StatefulSetStatus(ctx, spec.ReleaseName(), spec.Namespace)
	if err != nil {
		return current, fmt.Errorf("read statefulset: %w", err)
	}
	observed := domain.StatefulSetStatus{}
	if statefulSet != nil {
		observed = *statefulSet
	}
	ready := observed.ReadyReplicas >= spec.Replicas && !observed.Rolling()
	phase := pendingPhase(current)
	readyStatus := domain.ConditionFalse
	reason := "ReplicasNotReady"
	if ready {
		phase = domain.PhaseRunning
		readyStatus = domain.ConditionTrue
		reason = "AllReplicasReady"
	}
	conditions := domain.SetCondition(current.Conditions, domain.Condition{
		Type:               "Ready",
		Status:             readyStatus,
		LastTransitionTime: now(o.Clock),
		Reason:             reason,
		Message:            readyMessage(spec, observed),
	})
	if ready && !IsApplyFailed(current.Conditions) {
		conditions = domain.SetCondition(conditions, ProgressCondition(o.Clock, "Progressing", false, "RolloutComplete", "StatefulSet rollout complete"))
	}
	current.Phase = phase
	current.Replicas = spec.Replicas
	current.ReadyReplicas = observed.ReadyReplicas
	current.UpdatedReplicas = observed.UpdatedReplicas
	current.CurrentRevision = observed.CurrentRevision
	current.UpdateRevision = observed.UpdateRevision
	current.Endpoint = spec.ServiceURL()
	current.Conditions = conditions
	return current, nil
}

func readyMessage(spec domain.ClusterSpec, statefulSet domain.StatefulSetStatus) string {
	message := fmt.Sprintf("%d/%d replicas ready", statefulSet.ReadyReplicas, spec.Replicas)
	if !statefulSet.Rolling() {
		return message
	}
	return fmt.Sprintf("%s; rolling update %d/%d updated", message, statefulSet.UpdatedReplicas, spec.Replicas)
}

func pendingPhase(current domain.ClusterStatus) domain.ClusterPhase {
	if current.Phase == domain.PhaseUpgrading || current.Phase == domain.PhaseFailed {
		return current.Phase
	}
	return domain.PhasePending
}
