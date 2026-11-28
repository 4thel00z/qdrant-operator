package usecases

import "github.com/4thel00z/qdrant-operator/internal/domain"

// ScaleDownPendingError means peers are still being emptied; the caller writes Status and retries.
type ScaleDownPendingError struct {
	Message string
	Status  domain.ClusterStatus
	Cause   error
}

func (e *ScaleDownPendingError) Error() string { return e.Message }
func (e *ScaleDownPendingError) Unwrap() error { return e.Cause }

// RolloutInProgressError means a pod restart is running; the caller retries once it is done.
// Status is nil when there is nothing to report.
type RolloutInProgressError struct {
	Message string
	Status  *domain.ClusterStatus
}

func (e *RolloutInProgressError) Error() string { return e.Message }

// ReleaseApplyError means helm install or upgrade failed; Status carries the failure condition.
type ReleaseApplyError struct {
	Message string
	Status  domain.ClusterStatus
	Cause   error
}

func (e *ReleaseApplyError) Error() string { return e.Message }
func (e *ReleaseApplyError) Unwrap() error { return e.Cause }

// ShardsUnsafeError means moving a replica would lose data: no healthy copy exists outside the removed peers.
type ShardsUnsafeError struct {
	Message string
}

func (e *ShardsUnsafeError) Error() string { return e.Message }
