package controller_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	qdrantv1alpha1 "github.com/4thel00z/qdrant-operator/api/v1alpha1"
	"github.com/4thel00z/qdrant-operator/internal/controller"
	"github.com/4thel00z/qdrant-operator/internal/domain"
)

func registerSchedules(manager ctrl.Manager, deps controller.Dependencies) error {
	return (&controller.ScheduleReconciler{Dependencies: deps}).SetupWithManager(manager, 2)
}

// scheduleLater moves the manager's clock two hours ahead, so an every-minute slot is already due.
func scheduleLater(w *world) {
	w.Deps.Clock = func() time.Time { return time.Now().UTC().Add(2 * time.Hour) }
}

func scheduleObject(name string, suspend bool) *qdrantv1alpha1.QdrantBackupSchedule {
	return &qdrantv1alpha1.QdrantBackupSchedule{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: qdrantv1alpha1.QdrantBackupScheduleSpec{
			Schedule:   "* * * * *",
			ClusterRef: qdrantv1alpha1.ClusterRef{Name: "db"},
			Storage: &qdrantv1alpha1.ScheduleStorage{S3: qdrantv1alpha1.ScheduleS3Storage{
				Bucket:               backupBucket,
				Prefix:               "backups",
				CredentialsSecretRef: qdrantv1alpha1.S3CredentialsSecretRef{Name: "s3-creds"},
			}},
			DeleteBackupsWithSchedule: true,
			Suspend:                   suspend,
		},
	}
}

func scheduleBackups(t *testing.T, w *world) []qdrantv1alpha1.QdrantBackup {
	t.Helper()
	list := &qdrantv1alpha1.QdrantBackupList{}
	if err := w.Direct.List(context.Background(), list, client.InNamespace(w.Namespace)); err != nil {
		t.Fatal(err)
	}
	return list.Items
}

func TestScheduleControllerCreatesABackupOwnedByTheSchedule(t *testing.T) {
	w := startWorld(t, scheduleLater, registerSchedules)
	create(t, w, scheduleObject("nightly", false))

	eventually(t, func() (bool, string) {
		backups := scheduleBackups(t, w)
		return len(backups) == 1, fmt.Sprintf("%d backups", len(backups))
	})

	schedule := fetch(t, w, "nightly", &qdrantv1alpha1.QdrantBackupSchedule{})
	backup := scheduleBackups(t, w)[0]
	if len(backup.OwnerReferences) != 1 {
		t.Fatalf("owner references: %+v", backup.OwnerReferences)
	}
	owner := backup.OwnerReferences[0]
	if owner.Kind != domain.Schedules.Kind || owner.APIVersion != domain.APIVersion || owner.Name != "nightly" || owner.UID != schedule.UID {
		t.Errorf("owner %+v, schedule uid %s", owner, schedule.UID)
	}
	eventually(t, func() (bool, string) {
		status := fetch(t, w, "nightly", &qdrantv1alpha1.QdrantBackupSchedule{}).Status
		return status.ActiveBackup == backup.Name, fmt.Sprintf("status %+v", status)
	})
}

func TestScheduleControllerLaunchesOneBackupPerSlot(t *testing.T) {
	w := startWorld(t, scheduleLater, registerSchedules)
	create(t, w, scheduleObject("nightly", false))
	eventually(t, func() (bool, string) {
		status := fetch(t, w, "nightly", &qdrantv1alpha1.QdrantBackupSchedule{}).Status
		return status.LastScheduleTime != "", fmt.Sprintf("status %+v", status)
	})

	consistently(t, 3*time.Second, func() (bool, string) {
		backups := scheduleBackups(t, w)
		return len(backups) == 1, fmt.Sprintf("%d backups", len(backups))
	})
}

func TestScheduleControllerCreatesNothingWhileSuspended(t *testing.T) {
	w := startWorld(t, scheduleLater, registerSchedules)
	create(t, w, scheduleObject("nightly", true))

	eventually(t, func() (bool, string) {
		phase := fetch(t, w, "nightly", &qdrantv1alpha1.QdrantBackupSchedule{}).Status.Phase
		return phase == "Suspended", fmt.Sprintf("phase %q", phase)
	})
	consistently(t, 2*time.Second, func() (bool, string) {
		backups := scheduleBackups(t, w)
		return len(backups) == 0, fmt.Sprintf("%d backups", len(backups))
	})
}
