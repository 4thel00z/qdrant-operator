package usecases

import (
	"context"
	"fmt"
	"time"

	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/ports"
	"github.com/4thel00z/qdrant-operator/internal/translate"
)

func backupNow(clock Clock) time.Time {
	if clock == nil {
		return SystemClock()
	}
	return clock()
}

func backupStartTime(previous *domain.BackupStatus, clock Clock) time.Time {
	if previous != nil && !previous.StartTime.IsZero() {
		return previous.StartTime
	}
	return backupNow(clock)
}

func patchBackupStatus(ctx context.Context, kubernetes ports.Kubernetes, ref domain.ResourceRef, status domain.BackupStatus) error {
	body, err := EncodeObject(translate.BackupStatusToAPI(status))
	if err != nil {
		return fmt.Errorf("encode backup status: %w", err)
	}
	if err := kubernetes.PatchStatus(ctx, ref, body); err != nil {
		return fmt.Errorf("patch status of %s %s/%s: %w", ref.Kind.Kind, ref.Namespace, ref.Name, err)
	}
	return nil
}

// patchInProgressSnapshot writes the marker by hand: the API type omits a zero nodeIndex, which a merge patch
// would then leave at the value of the previous marker.
func patchInProgressSnapshot(ctx context.Context, kubernetes ports.Kubernetes, ref domain.ResourceRef, marker *domain.InProgressSnapshot) error {
	var value any
	if marker != nil {
		value = domain.JSON{"nodeIndex": marker.NodeIndex, "collection": marker.Collection, "snapshot": marker.Snapshot}
	}
	if err := kubernetes.PatchStatus(ctx, ref, domain.JSON{"inProgressSnapshot": value}); err != nil {
		return fmt.Errorf("patch inProgressSnapshot of %s %s/%s: %w", ref.Kind.Kind, ref.Namespace, ref.Name, err)
	}
	return nil
}

func backupCondition(succeeded bool, at time.Time, reasonOK string, reasonFailed string, message string) domain.Condition {
	if succeeded {
		return domain.Condition{Type: "Complete", Status: domain.ConditionTrue, LastTransitionTime: at, Reason: reasonOK, Message: message}
	}
	return domain.Condition{Type: "Complete", Status: domain.ConditionFalse, LastTransitionTime: at, Reason: reasonFailed, Message: message}
}
