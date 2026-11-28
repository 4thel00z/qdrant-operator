package controller_test

import (
	"context"
	"fmt"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	qdrantv1alpha1 "github.com/4thel00z/qdrant-operator/api/v1alpha1"
	"github.com/4thel00z/qdrant-operator/internal/controller"
	"github.com/4thel00z/qdrant-operator/internal/domain"
)

const backupBucket = "bucket"

func registerBackups(manager ctrl.Manager, deps controller.Dependencies) error {
	return (&controller.BackupReconciler{Dependencies: deps}).SetupWithManager(manager, 2)
}

func registerBackupsAndRestores(manager ctrl.Manager, deps controller.Dependencies) error {
	if err := registerBackups(manager, deps); err != nil {
		return err
	}
	return (&controller.RestoreReconciler{Dependencies: deps}).SetupWithManager(manager, 2)
}

// backupArrange routes every named cluster and gives the first one a collection called docs.
func backupArrange(clusters ...string) Arrange {
	return func(w *world) {
		for index, name := range clusters {
			w.Qdrant.Routes[serviceURL(name, w.Namespace)] = nodeURL(0, name, w.Namespace)
			if index == 0 {
				w.Qdrant.AddCollection(nodeURL(0, name, w.Namespace), "docs", 10, nil)
			}
		}
	}
}

// backupOffsetClock is a clock whose reading can be moved forward while the manager runs.
type backupOffsetClock struct {
	offset atomic.Int64
}

func (c *backupOffsetClock) now() time.Time {
	return time.Now().UTC().Add(time.Duration(c.offset.Load()))
}

func (c *backupOffsetClock) advance(delta time.Duration) {
	c.offset.Add(int64(delta))
}

func backupSecret(t *testing.T, w *world) {
	t.Helper()
	create(t, w, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "s3-creds"},
		StringData: map[string]string{"AWS_ACCESS_KEY_ID": "AK", "AWS_SECRET_ACCESS_KEY": "SK"},
	})
}

func backupObject(name, cluster string, retentionDays int32) *qdrantv1alpha1.QdrantBackup {
	spec := qdrantv1alpha1.QdrantBackupSpec{
		ClusterRef: qdrantv1alpha1.ClusterRef{Name: cluster},
		Storage: &qdrantv1alpha1.BackupStorage{S3: qdrantv1alpha1.BackupS3Storage{
			Bucket:               backupBucket,
			Prefix:               "backups",
			CredentialsSecretRef: qdrantv1alpha1.S3CredentialsSecretRef{Name: "s3-creds"},
		}},
	}
	if retentionDays > 0 {
		spec.RetentionDays = &retentionDays
	}
	return &qdrantv1alpha1.QdrantBackup{ObjectMeta: metav1.ObjectMeta{Name: name}, Spec: spec}
}

func backupPhase(t *testing.T, w *world, name string) qdrantv1alpha1.BackupPhase {
	t.Helper()
	return fetch(t, w, name, &qdrantv1alpha1.QdrantBackup{}).Status.Phase
}

func backupAwaitCompleted(t *testing.T, w *world, name string) {
	t.Helper()
	eventually(t, func() (bool, string) {
		phase := backupPhase(t, w, name)
		return phase == "Completed", fmt.Sprintf("phase %s", phase)
	})
}

func backupGone(t *testing.T, w *world, name string) {
	t.Helper()
	eventually(t, func() (bool, string) {
		err := w.Direct.Get(context.Background(), client.ObjectKey{Namespace: w.Namespace, Name: name}, &qdrantv1alpha1.QdrantBackup{})
		return apierrors.IsNotFound(err), fmt.Sprintf("get: %v", err)
	})
}

func backupReconcileOnce(t *testing.T, w *world, name string) {
	t.Helper()
	reconciler := controller.BackupReconciler{Dependencies: w.Deps}
	request := ctrl.Request{NamespacedName: client.ObjectKey{Namespace: w.Namespace, Name: name}}
	if _, err := reconciler.Reconcile(context.Background(), request); err != nil {
		t.Fatalf("reconcile %s: %v", name, err)
	}
}

func backupStorageSpec() domain.S3StorageSpec {
	return domain.S3StorageSpec{Bucket: backupBucket}
}

func TestBackupControllerRunsTheBackupAndRecordsTheStatus(t *testing.T) {
	w := startWorld(t, backupArrange("db"), registerBackups)
	backupSecret(t, w)
	create(t, w, clusterObject("db", qdrantv1alpha1.QdrantClusterSpec{Version: "1.16.3"}))
	create(t, w, backupObject("nightly", "db", 0))

	backupAwaitCompleted(t, w, "nightly")

	object := fetch(t, w, "nightly", &qdrantv1alpha1.QdrantBackup{})
	if object.Status.S3Path != "s3://bucket/backups/nightly" {
		t.Errorf("s3Path %q", object.Status.S3Path)
	}
	if !slices.Contains(object.Finalizers, controller.Finalizer) {
		t.Errorf("finalizers %v", object.Finalizers)
	}
	if keys := w.Storage.Keys(backupBucket); !slices.Contains(keys, "backups/nightly/manifest.json") {
		t.Errorf("no manifest in %v", keys)
	}
}

func TestBackupControllerDoesNotRunAFinishedBackupAgain(t *testing.T) {
	w := startWorld(t, backupArrange("db"), registerBackups)
	backupSecret(t, w)
	create(t, w, clusterObject("db", qdrantv1alpha1.QdrantClusterSpec{Version: "1.16.3"}))
	create(t, w, backupObject("nightly", "db", 0))
	backupAwaitCompleted(t, w, "nightly")
	if _, err := w.Storage.DeletePrefix(context.Background(), backupStorageSpec(), domain.S3Credentials{}, "backups/nightly"); err != nil {
		t.Fatal(err)
	}

	backupReconcileOnce(t, w, "nightly")

	consistently(t, 2*time.Second, func() (bool, string) {
		keys := w.Storage.Keys(backupBucket)
		return len(keys) == 0 && backupPhase(t, w, "nightly") == "Completed", fmt.Sprintf("keys %v", keys)
	})
}

func TestBackupControllerRetriesWhileTheClusterIsMissing(t *testing.T) {
	w := startWorld(t, backupArrange("db"), registerBackups)
	backupSecret(t, w)
	create(t, w, backupObject("nightly", "db", 0))

	consistently(t, 3*time.Second, func() (bool, string) {
		phase := backupPhase(t, w, "nightly")
		return phase != "Failed" && phase != "Completed", fmt.Sprintf("phase %s", phase)
	})
	create(t, w, clusterObject("db", qdrantv1alpha1.QdrantClusterSpec{Version: "1.16.3"}))

	backupAwaitCompleted(t, w, "nightly")
}

func TestBackupControllerWritesFailedOnAPermanentError(t *testing.T) {
	w := startWorld(t, backupArrange("db"), registerBackups)
	create(t, w, clusterObject("db", qdrantv1alpha1.QdrantClusterSpec{Version: "1.16.3"}))
	object := backupObject("volumes", "db", 0)
	object.Spec.Method = "volumeSnapshot"
	object.Spec.VolumeSnapshotClassName = "csi"
	object.Spec.Storage = nil
	create(t, w, object)

	eventually(t, func() (bool, string) {
		status := fetch(t, w, "volumes", &qdrantv1alpha1.QdrantBackup{}).Status
		return status.Phase == "Failed" && status.Error != "", fmt.Sprintf("status %+v", status)
	})
}

func TestBackupControllerDeletesTheStoredDataAndReleasesTheFinalizer(t *testing.T) {
	w := startWorld(t, backupArrange("db"), registerBackups)
	backupSecret(t, w)
	create(t, w, clusterObject("db", qdrantv1alpha1.QdrantClusterSpec{Version: "1.16.3"}))
	create(t, w, backupObject("nightly", "db", 0))
	backupAwaitCompleted(t, w, "nightly")
	if len(w.Storage.Keys(backupBucket)) == 0 {
		t.Fatal("backup stored nothing")
	}

	if err := w.Direct.Delete(context.Background(), fetch(t, w, "nightly", &qdrantv1alpha1.QdrantBackup{})); err != nil {
		t.Fatal(err)
	}

	backupGone(t, w, "nightly")
	if keys := w.Storage.Keys(backupBucket); len(keys) != 0 {
		t.Errorf("data left behind: %v", keys)
	}
}

func TestBackupControllerReleasesTheFinalizerWhenTheDataCannotBeDeleted(t *testing.T) {
	w := startWorld(t, backupArrange("db"), registerBackups)
	create(t, w, clusterObject("db", qdrantv1alpha1.QdrantClusterSpec{Version: "1.16.3"}))
	create(t, w, backupObject("nightly", "db", 0))
	eventually(t, func() (bool, string) {
		finalizers := fetch(t, w, "nightly", &qdrantv1alpha1.QdrantBackup{}).Finalizers
		return slices.Contains(finalizers, controller.Finalizer), fmt.Sprintf("finalizers %v", finalizers)
	})

	if err := w.Direct.Delete(context.Background(), fetch(t, w, "nightly", &qdrantv1alpha1.QdrantBackup{})); err != nil {
		t.Fatal(err)
	}

	backupGone(t, w, "nightly")
}

func TestBackupControllerDeletesABackupOnceItsRetentionHasPassed(t *testing.T) {
	clock := &backupOffsetClock{}
	arrange := func(w *world) {
		backupArrange("db")(w)
		w.Deps.Clock = clock.now
	}
	w := startWorld(t, arrange, registerBackups)
	backupSecret(t, w)
	create(t, w, clusterObject("db", qdrantv1alpha1.QdrantClusterSpec{Version: "1.16.3"}))
	create(t, w, backupObject("nightly", "db", 1))
	backupAwaitCompleted(t, w, "nightly")
	if expires := fetch(t, w, "nightly", &qdrantv1alpha1.QdrantBackup{}).Status.ExpiresAt; expires == "" {
		t.Fatal("no expiresAt")
	}

	consistently(t, 2*time.Second, func() (bool, string) {
		err := w.Direct.Get(context.Background(), client.ObjectKey{Namespace: w.Namespace, Name: "nightly"}, &qdrantv1alpha1.QdrantBackup{})
		return err == nil, fmt.Sprintf("get: %v", err)
	})
	clock.advance(48 * time.Hour)

	backupGone(t, w, "nightly")
	eventually(t, func() (bool, string) {
		keys := w.Storage.Keys(backupBucket)
		return len(keys) == 0, fmt.Sprintf("keys %v", keys)
	})
}
