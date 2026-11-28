package controller_test

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	qdrantv1alpha1 "github.com/4thel00z/qdrant-operator/api/v1alpha1"
	"github.com/4thel00z/qdrant-operator/internal/controller"
	"github.com/4thel00z/qdrant-operator/internal/domain"
)

func registerCollections(manager ctrl.Manager, deps controller.Dependencies) error {
	return (&controller.CollectionReconciler{Dependencies: deps}).SetupWithManager(manager, 2)
}

func routeCluster(name string) Arrange {
	return func(w *world) {
		w.Qdrant.Routes[serviceURL(name, w.Namespace)] = nodeURL(0, name, w.Namespace)
	}
}

func collectionObject(name, cluster string, policy qdrantv1alpha1.CollectionDeletionPolicy) *qdrantv1alpha1.QdrantCollection {
	return &qdrantv1alpha1.QdrantCollection{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: qdrantv1alpha1.QdrantCollectionSpec{
			ClusterRef:     qdrantv1alpha1.ClusterRef{Name: cluster},
			Vectors:        []qdrantv1alpha1.DenseVector{{Size: 4, Distance: "Cosine"}},
			DeletionPolicy: policy,
		},
	}
}

func collectionPhase(t *testing.T, w *world, name string) qdrantv1alpha1.CollectionPhase {
	t.Helper()
	return fetch(t, w, name, &qdrantv1alpha1.QdrantCollection{}).Status.Phase
}

func TestCollectionControllerCreatesTheCollectionAndReportsReady(t *testing.T) {
	w := startWorld(t, routeCluster("db"), registerCollections)
	create(t, w, clusterObject("db", qdrantv1alpha1.QdrantClusterSpec{Version: "1.16.3"}))
	create(t, w, collectionObject("articles", "db", ""))

	eventually(t, func() (bool, string) {
		object := fetch(t, w, "articles", &qdrantv1alpha1.QdrantCollection{})
		ready := object.Status.Phase == "Ready" && slices.Contains(object.Finalizers, controller.Finalizer)
		return ready, fmt.Sprintf("status %+v, finalizers %v", object.Status, object.Finalizers)
	})
	object := fetch(t, w, "articles", &qdrantv1alpha1.QdrantCollection{})
	if object.Status.ObservedGeneration != object.Generation {
		t.Errorf("observedGeneration %d, generation %d", object.Status.ObservedGeneration, object.Generation)
	}
	if len(object.Status.Conditions) != 1 || object.Status.Conditions[0].LastTransitionTime == "" {
		t.Errorf("conditions: %+v", object.Status.Conditions)
	}
}

func TestCollectionControllerRetriesWhileTheClusterIsMissing(t *testing.T) {
	w := startWorld(t, routeCluster("db"), registerCollections)
	create(t, w, collectionObject("articles", "db", ""))

	consistently(t, 3*time.Second, func() (bool, string) {
		phase := collectionPhase(t, w, "articles")
		return phase != "Failed" && phase != "Ready", fmt.Sprintf("phase %s", phase)
	})
	create(t, w, clusterObject("db", qdrantv1alpha1.QdrantClusterSpec{Version: "1.16.3"}))
	eventually(t, func() (bool, string) {
		phase := collectionPhase(t, w, "articles")
		return phase == "Ready", fmt.Sprintf("phase %s", phase)
	})
}

func TestCollectionControllerDropsTheCollectionOnDeleteAndReleasesTheFinalizer(t *testing.T) {
	w := startWorld(t, routeCluster("db"), registerCollections)
	create(t, w, clusterObject("db", qdrantv1alpha1.QdrantClusterSpec{Version: "1.16.3"}))
	create(t, w, collectionObject("articles", "db", "Delete"))
	eventually(t, func() (bool, string) {
		phase := collectionPhase(t, w, "articles")
		return phase == "Ready", fmt.Sprintf("phase %s", phase)
	})

	object := fetch(t, w, "articles", &qdrantv1alpha1.QdrantCollection{})
	if err := w.Direct.Delete(context.Background(), object); err != nil {
		t.Fatal(err)
	}

	eventually(t, func() (bool, string) {
		err := w.Direct.Get(context.Background(), client.ObjectKeyFromObject(object), &qdrantv1alpha1.QdrantCollection{})
		return apierrors.IsNotFound(err), fmt.Sprintf("get: %v", err)
	})
	collections, err := w.Qdrant.ListCollections(context.Background(), domain.QdrantNode{URL: nodeURL(0, "db", w.Namespace)})
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(collections, "articles") {
		t.Errorf("collection still present: %v", collections)
	}
}
