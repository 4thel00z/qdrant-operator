package translate

import (
	"fmt"
	"time"

	qdrantv1alpha1 "github.com/4thel00z/qdrant-operator/api/v1alpha1"
	"github.com/4thel00z/qdrant-operator/internal/domain"
)

func accessMigrationConditionsFromAPI(conditions []qdrantv1alpha1.Condition) ([]domain.Condition, error) {
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

func accessMigrationConditionsToAPI(conditions []domain.Condition) []qdrantv1alpha1.Condition {
	result := make([]qdrantv1alpha1.Condition, 0, len(conditions))
	for _, condition := range conditions {
		result = append(result, qdrantv1alpha1.Condition{
			Type:               condition.Type,
			Status:             qdrantv1alpha1.ConditionStatus(condition.Status),
			LastTransitionTime: accessMigrationTime(condition.LastTransitionTime),
			Reason:             condition.Reason,
			Message:            condition.Message,
		})
	}
	return result
}

func accessMigrationTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return domain.FormatTime(value)
}

func accessMigrationClusterRef(ref qdrantv1alpha1.ClusterRef, namespace string) domain.ClusterRef {
	if ref.Namespace == "" {
		return domain.ClusterRef{Name: ref.Name, Namespace: namespace}
	}
	return domain.ClusterRef{Name: ref.Name, Namespace: ref.Namespace}
}
