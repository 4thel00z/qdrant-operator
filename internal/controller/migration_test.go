package controller_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	qdrantv1alpha1 "github.com/4thel00z/qdrant-operator/api/v1alpha1"
	"github.com/4thel00z/qdrant-operator/internal/controller"
	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/fakes"
)

func registerMigrations(manager ctrl.Manager, deps controller.Dependencies) error {
	return (&controller.MigrationReconciler{Dependencies: deps}).SetupWithManager(manager, 2)
}

func migrationPoints(count int) []domain.Point {
	points := []domain.Point{}
	for id := 1; id <= count; id++ {
		points = append(points, domain.Point{
			ID:      domain.NumberPointID(uint64(id)),
			Vector:  json.RawMessage(fmt.Sprintf("[%d.0,0.5]", id)),
			Payload: json.RawMessage(fmt.Sprintf(`{"n":%d}`, id)),
		})
	}
	return points
}

func migrationArrange(points int) Arrange {
	return func(w *world) {
		for _, name := range []string{"src", "dst"} {
			w.Qdrant.Routes[serviceURL(name, w.Namespace)] = nodeURL(0, name, w.Namespace)
		}
		vectors := domain.JSON{"vectors": domain.JSON{"size": 2, "distance": "Dot"}}
		w.Qdrant.AddCollection(nodeURL(0, "src", w.Namespace), "docs", 0, vectors)
		w.Qdrant.AddPoints(nodeURL(0, "src", w.Namespace), "docs", migrationPoints(points))
	}
}

func migrationClusters(t *testing.T, w *world) {
	t.Helper()
	create(t, w, clusterObject("src", qdrantv1alpha1.QdrantClusterSpec{Version: "1.16.3"}))
	create(t, w, clusterObject("dst", qdrantv1alpha1.QdrantClusterSpec{Version: "1.16.3"}))
}

func migrationObject(name string, collections ...string) *qdrantv1alpha1.QdrantMigration {
	return &qdrantv1alpha1.QdrantMigration{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: qdrantv1alpha1.QdrantMigrationSpec{
			Source:            qdrantv1alpha1.MigrationSource{ClusterRef: &qdrantv1alpha1.ClusterRef{Name: "src"}},
			TargetClusterRef:  qdrantv1alpha1.ClusterRef{Name: "dst"},
			Collections:       collections,
			BatchSize:         int32Pointer(2),
			CheckpointBatches: int32Pointer(1),
		},
	}
}

func int32Pointer(value int32) *int32 {
	return &value
}

func migrationStatus(t *testing.T, w *world, name string) qdrantv1alpha1.QdrantMigrationStatus {
	t.Helper()
	return fetch(t, w, name, &qdrantv1alpha1.QdrantMigration{}).Status
}

func migrationTargetCount(w *world, collection string) int {
	return len(w.Qdrant.StoredPoints(nodeURL(0, "dst", w.Namespace), collection))
}

func migrationWaitFor(t *testing.T, w *world, name string, phase qdrantv1alpha1.MigrationPhase) {
	t.Helper()
	eventually(t, func() (bool, string) {
		status := migrationStatus(t, w, name)
		return status.Phase == phase, fmt.Sprintf("status %+v", status)
	})
}

func TestMigrationControllerCopiesPointsAndReportsCompleted(t *testing.T) {
	w := startWorld(t, migrationArrange(7), registerMigrations)
	migrationClusters(t, w)
	create(t, w, migrationObject("move"))

	migrationWaitFor(t, w, "move", "Completed")
	status := migrationStatus(t, w, "move")
	if got := migrationTargetCount(w, "docs"); got != 7 {
		t.Errorf("target points: %d", got)
	}
	if status.Progress == nil || status.Progress.PointsCopied != 7 || status.Progress.CollectionsCompleted != 1 {
		t.Errorf("progress: %+v", status.Progress)
	}
	if status.CompletionTime == "" || len(status.Conditions) != 1 {
		t.Errorf("status: %+v", status)
	}
}

func TestMigrationControllerDoesNotRunACompletedMigrationAgain(t *testing.T) {
	w := startWorld(t, migrationArrange(4), registerMigrations)
	migrationClusters(t, w)
	create(t, w, migrationObject("move"))
	migrationWaitFor(t, w, "move", "Completed")

	w.Qdrant.AddPoints(nodeURL(0, "src", w.Namespace), "docs", migrationPoints(9))
	object := fetch(t, w, "move", &qdrantv1alpha1.QdrantMigration{})
	base := object.DeepCopy()
	object.Annotations = map[string]string{controller.RetryAnnotation: "now", "touched": "yes"}
	if err := w.Direct.Patch(context.Background(), object, client.MergeFrom(base)); err != nil {
		t.Fatal(err)
	}

	consistently(t, 3*time.Second, func() (bool, string) {
		count := migrationTargetCount(w, "docs")
		return count == 4, fmt.Sprintf("target points %d", count)
	})
	if got := fetch(t, w, "move", &qdrantv1alpha1.QdrantMigration{}).Annotations[controller.RetryAnnotation]; got != "now" {
		t.Errorf("retry annotation on a completed migration: %q", got)
	}
}

func TestMigrationControllerRetriesAfterAnUnreachableQdrantAndCompletes(t *testing.T) {
	var qdrant *fakes.Qdrant
	var delays atomic.Int32
	arrange := func(w *world) {
		migrationArrange(10)(w)
		qdrant = w.Qdrant
		limit := 3
		w.Qdrant.UnreachableAfterUpserts = &limit
	}
	register := func(manager ctrl.Manager, deps controller.Dependencies) error {
		reconciler := &controller.MigrationReconciler{Dependencies: deps, UnreachableDelay: func(attempt int) time.Duration {
			if attempt == 0 {
				delays.Add(1)
				qdrant.UnreachableAfterUpserts = nil
			}
			return 500 * time.Millisecond
		}}
		return reconciler.SetupWithManager(manager, 2)
	}
	w := startWorld(t, arrange, register)
	migrationClusters(t, w)
	create(t, w, migrationObject("move"))

	migrationWaitFor(t, w, "move", "Completed")
	if got := migrationTargetCount(w, "docs"); got != 10 {
		t.Errorf("target points: %d", got)
	}
	if delays.Load() != 1 {
		t.Errorf("unreachable delays: %d", delays.Load())
	}
}

func TestMigrationControllerReportsFailedForAMissingCollectionAndReRunsOnTheRetryAnnotation(t *testing.T) {
	w := startWorld(t, migrationArrange(5), registerMigrations)
	migrationClusters(t, w)
	create(t, w, migrationObject("move", "docs", "late"))

	migrationWaitFor(t, w, "move", "Failed")
	if status := migrationStatus(t, w, "move"); status.Error == "" {
		t.Errorf("failed without an error: %+v", status)
	}
	consistently(t, 2*time.Second, func() (bool, string) {
		phase := migrationStatus(t, w, "move").Phase
		return phase == "Failed", fmt.Sprintf("phase %s", phase)
	})

	source := nodeURL(0, "src", w.Namespace)
	w.Qdrant.AddCollection(source, "late", 0, domain.JSON{"vectors": domain.JSON{"size": 2, "distance": "Dot"}})
	w.Qdrant.AddPoints(source, "late", migrationPoints(3))
	object := fetch(t, w, "move", &qdrantv1alpha1.QdrantMigration{})
	base := object.DeepCopy()
	object.Annotations = map[string]string{controller.RetryAnnotation: "1"}
	if err := w.Direct.Patch(context.Background(), object, client.MergeFrom(base)); err != nil {
		t.Fatal(err)
	}

	migrationWaitFor(t, w, "move", "Completed")
	if migrationTargetCount(w, "docs") != 5 || migrationTargetCount(w, "late") != 3 {
		t.Errorf("target points: docs %d, late %d", migrationTargetCount(w, "docs"), migrationTargetCount(w, "late"))
	}
	eventually(t, func() (bool, string) {
		annotations := fetch(t, w, "move", &qdrantv1alpha1.QdrantMigration{}).Annotations
		_, present := annotations[controller.RetryAnnotation]
		return !present, fmt.Sprintf("annotations %v", annotations)
	})
}

func TestMigrationControllerWaitsForAMissingTargetCluster(t *testing.T) {
	w := startWorld(t, migrationArrange(3), registerMigrations)
	create(t, w, clusterObject("src", qdrantv1alpha1.QdrantClusterSpec{Version: "1.16.3"}))
	create(t, w, migrationObject("move"))

	consistently(t, 3*time.Second, func() (bool, string) {
		phase := migrationStatus(t, w, "move").Phase
		return phase != "Failed" && phase != "Completed", fmt.Sprintf("phase %s", phase)
	})
	create(t, w, clusterObject("dst", qdrantv1alpha1.QdrantClusterSpec{Version: "1.16.3"}))
	migrationWaitFor(t, w, "move", "Completed")
}
