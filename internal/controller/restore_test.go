package controller_test

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	qdrantv1alpha1 "github.com/4thel00z/qdrant-operator/api/v1alpha1"
	"github.com/4thel00z/qdrant-operator/internal/controller"
	"github.com/4thel00z/qdrant-operator/internal/domain"
)

func restoreObject(name string, spec qdrantv1alpha1.QdrantRestoreSpec) *qdrantv1alpha1.QdrantRestore {
	wait := false
	spec.WaitForIndexing = &wait
	return &qdrantv1alpha1.QdrantRestore{ObjectMeta: metav1.ObjectMeta{Name: name}, Spec: spec}
}

func restoreFromBackup(target string) qdrantv1alpha1.QdrantRestoreSpec {
	return qdrantv1alpha1.QdrantRestoreSpec{
		BackupRef:         &qdrantv1alpha1.RestoreBackupRef{Name: "nightly"},
		TargetClusterRef:  qdrantv1alpha1.ClusterRef{Name: target},
		CollectionMapping: map[string]string{"docs": "docs-restored"},
	}
}

func restorePhase(t *testing.T, w *world, name string) qdrantv1alpha1.RestorePhase {
	t.Helper()
	return fetch(t, w, name, &qdrantv1alpha1.QdrantRestore{}).Status.Phase
}

func restoreAwaitCompleted(t *testing.T, w *world, name string) {
	t.Helper()
	eventually(t, func() (bool, string) {
		status := fetch(t, w, name, &qdrantv1alpha1.QdrantRestore{}).Status
		return status.Phase == "Completed", fmt.Sprintf("status %+v", status)
	})
}

func restoreCollections(t *testing.T, w *world, cluster string) []string {
	t.Helper()
	collections, err := w.Qdrant.ListCollections(context.Background(), domain.QdrantNode{URL: nodeURL(0, cluster, w.Namespace)})
	if err != nil {
		t.Fatal(err)
	}
	return collections
}

func restoreBackedUpWorld(t *testing.T) *world {
	t.Helper()
	w := startWorld(t, backupArrange("db", "other"), registerBackupsAndRestores)
	backupSecret(t, w)
	create(t, w, clusterObject("db", qdrantv1alpha1.QdrantClusterSpec{Version: "1.16.3"}))
	create(t, w, backupObject("nightly", "db", 0))
	backupAwaitCompleted(t, w, "nightly")
	return w
}

func TestRestoreControllerRecoversTheBackupIntoTheTargetCluster(t *testing.T) {
	w := restoreBackedUpWorld(t)
	create(t, w, clusterObject("other", qdrantv1alpha1.QdrantClusterSpec{Version: "1.16.3"}))
	create(t, w, restoreObject("recover", restoreFromBackup("other")))

	restoreAwaitCompleted(t, w, "recover")

	if collections := restoreCollections(t, w, "other"); !slices.Contains(collections, "docs-restored") {
		t.Errorf("collections %v", collections)
	}
	status := fetch(t, w, "recover", &qdrantv1alpha1.QdrantRestore{}).Status
	if status.SourceBackup != "nightly" {
		t.Errorf("sourceBackup %q", status.SourceBackup)
	}
}

func TestRestoreControllerDoesNotRunAFinishedRestoreAgain(t *testing.T) {
	w := restoreBackedUpWorld(t)
	create(t, w, clusterObject("other", qdrantv1alpha1.QdrantClusterSpec{Version: "1.16.3"}))
	create(t, w, restoreObject("recover", restoreFromBackup("other")))
	restoreAwaitCompleted(t, w, "recover")
	if _, err := w.Storage.DeletePrefix(context.Background(), backupStorageSpec(), domain.S3Credentials{}, "backups/nightly"); err != nil {
		t.Fatal(err)
	}

	reconciler := controller.RestoreReconciler{Dependencies: w.Deps}
	request := ctrl.Request{NamespacedName: client.ObjectKey{Namespace: w.Namespace, Name: "recover"}}
	if _, err := reconciler.Reconcile(context.Background(), request); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}

	consistently(t, time.Second, func() (bool, string) {
		phase := restorePhase(t, w, "recover")
		return phase == "Completed", fmt.Sprintf("phase %s", phase)
	})
}

func TestRestoreControllerRetriesWhileTheTargetClusterIsMissing(t *testing.T) {
	w := restoreBackedUpWorld(t)
	create(t, w, restoreObject("recover", restoreFromBackup("other")))

	consistently(t, 3*time.Second, func() (bool, string) {
		phase := restorePhase(t, w, "recover")
		return phase != "Failed" && phase != "Completed", fmt.Sprintf("phase %s", phase)
	})
	create(t, w, clusterObject("other", qdrantv1alpha1.QdrantClusterSpec{Version: "1.16.3"}))

	restoreAwaitCompleted(t, w, "recover")
}

func TestRestoreControllerWritesFailedOnAPermanentError(t *testing.T) {
	w := restoreBackedUpWorld(t)
	create(t, w, clusterObject("other", qdrantv1alpha1.QdrantClusterSpec{Version: "1.16.3"}))
	create(t, w, restoreObject("recover", qdrantv1alpha1.QdrantRestoreSpec{
		Source: &qdrantv1alpha1.RestoreSource{S3: &qdrantv1alpha1.RestoreS3Source{
			Bucket:               backupBucket,
			Path:                 "backups/missing",
			CredentialsSecretRef: qdrantv1alpha1.S3CredentialsSecretRef{Name: "s3-creds"},
		}},
		TargetClusterRef: qdrantv1alpha1.ClusterRef{Name: "other"},
	}))

	eventually(t, func() (bool, string) {
		status := fetch(t, w, "recover", &qdrantv1alpha1.QdrantRestore{}).Status
		return status.Phase == "Failed" && status.Error != "", fmt.Sprintf("status %+v", status)
	})
}
