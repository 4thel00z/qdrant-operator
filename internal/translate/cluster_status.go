package translate

import (
	"fmt"

	qdrantv1alpha1 "github.com/4thel00z/qdrant-operator/api/v1alpha1"
	"github.com/4thel00z/qdrant-operator/internal/domain"
)

// ClusterStatusFromAPI reads a stored status; an empty phase means Pending.
func ClusterStatusFromAPI(status qdrantv1alpha1.QdrantClusterStatus) (domain.ClusterStatus, error) {
	conditions, err := ConditionsFromAPI(status.Conditions)
	if err != nil {
		return domain.ClusterStatus{}, fmt.Errorf("cluster status conditions: %w", err)
	}
	phase := domain.ClusterPhase(status.Phase)
	if phase == "" {
		phase = domain.PhasePending
	}
	return domain.ClusterStatus{
		Phase:              phase,
		Replicas:           int(status.Replicas),
		ReadyReplicas:      int(status.ReadyReplicas),
		UpdatedReplicas:    int(status.UpdatedReplicas),
		CurrentRevision:    status.CurrentRevision,
		UpdateRevision:     status.UpdateRevision,
		AppliedHash:        status.AppliedHash,
		HelmRelease:        status.HelmRelease,
		Endpoint:           status.Endpoint,
		Version:            status.Version,
		Conditions:         conditions,
		ObservedGeneration: status.ObservedGeneration,
	}, nil
}

func ClusterStatusToAPI(status domain.ClusterStatus) qdrantv1alpha1.QdrantClusterStatus {
	return qdrantv1alpha1.QdrantClusterStatus{
		Phase:              string(status.Phase),
		Replicas:           int32(status.Replicas),
		ReadyReplicas:      int32(status.ReadyReplicas),
		UpdatedReplicas:    int32(status.UpdatedReplicas),
		CurrentRevision:    status.CurrentRevision,
		UpdateRevision:     status.UpdateRevision,
		AppliedHash:        status.AppliedHash,
		Version:            status.Version,
		HelmRelease:        status.HelmRelease,
		Endpoint:           status.Endpoint,
		Conditions:         ConditionsToAPI(status.Conditions),
		ObservedGeneration: status.ObservedGeneration,
	}
}

func ConditionsFromAPI(conditions []qdrantv1alpha1.Condition) ([]domain.Condition, error) {
	result := make([]domain.Condition, 0, len(conditions))
	for _, condition := range conditions {
		transitioned, err := domain.ParseTime(condition.LastTransitionTime)
		if err != nil {
			return nil, fmt.Errorf("condition %s lastTransitionTime: %w", condition.Type, err)
		}
		result = append(result, domain.Condition{
			Type:               condition.Type,
			Status:             domain.ConditionStatus(condition.Status),
			LastTransitionTime: transitioned,
			Reason:             condition.Reason,
			Message:            condition.Message,
		})
	}
	return result, nil
}

func ConditionsToAPI(conditions []domain.Condition) []qdrantv1alpha1.Condition {
	if len(conditions) == 0 {
		return nil
	}
	result := make([]qdrantv1alpha1.Condition, 0, len(conditions))
	for _, condition := range conditions {
		transitioned := ""
		if !condition.LastTransitionTime.IsZero() {
			transitioned = domain.FormatTime(condition.LastTransitionTime)
		}
		result = append(result, qdrantv1alpha1.Condition{
			Type:               condition.Type,
			Status:             qdrantv1alpha1.ConditionStatus(condition.Status),
			LastTransitionTime: transitioned,
			Reason:             condition.Reason,
			Message:            condition.Message,
		})
	}
	return result
}
