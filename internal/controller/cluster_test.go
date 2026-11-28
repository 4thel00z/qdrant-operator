package controller_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	qdrantv1alpha1 "github.com/4thel00z/qdrant-operator/api/v1alpha1"
	"github.com/4thel00z/qdrant-operator/internal/controller"
	"github.com/4thel00z/qdrant-operator/internal/fakes"
	"github.com/4thel00z/qdrant-operator/internal/ports"
)

// clusterHelm counts apply attempts and fails them while failing is set.
type clusterHelm struct {
	*fakes.Helm
	attempts atomic.Int64
	failing  atomic.Bool
}

func (h *clusterHelm) Apply(ctx context.Context, release ports.ReleaseRequest) error {
	h.attempts.Add(1)
	if h.failing.Load() {
		return errors.New("chart rejected")
	}
	return h.Helm.Apply(ctx, release)
}

func registerClusters(helm *clusterHelm) Register {
	return func(manager ctrl.Manager, deps controller.Dependencies) error {
		deps.Helm = helm
		return (&controller.ClusterReconciler{Dependencies: deps}).SetupWithManager(manager, 2)
	}
}

func newClusterHelm(failing bool) *clusterHelm {
	helm := &clusterHelm{Helm: fakes.NewHelm()}
	helm.failing.Store(failing)
	return helm
}

func clusterSpec(replicas int32) qdrantv1alpha1.QdrantClusterSpec {
	return qdrantv1alpha1.QdrantClusterSpec{Version: "1.16.3", Replicas: &replicas}
}

func clusterFetch(t *testing.T, w *world, name string) *qdrantv1alpha1.QdrantCluster {
	t.Helper()
	return fetch(t, w, name, &qdrantv1alpha1.QdrantCluster{})
}

func clusterApplyFailed(object *qdrantv1alpha1.QdrantCluster) bool {
	return slices.ContainsFunc(object.Status.Conditions, func(condition qdrantv1alpha1.Condition) bool {
		return condition.Type == "Progressing" && condition.Status == "False" && condition.Reason == "HelmApplyFailed"
	})
}

func TestClusterControllerAppliesTheReleaseAndWritesStatus(t *testing.T) {
	helm := newClusterHelm(false)
	w := startWorld(t, nil, registerClusters(helm))
	create(t, w, clusterObject("db", clusterSpec(2)))

	eventually(t, func() (bool, string) {
		object := clusterFetch(t, w, "db")
		_, applied := helm.Release("qdrant-db", w.Namespace)
		done := applied && object.Status.ObservedGeneration == object.Generation && slices.Contains(object.Finalizers, controller.Finalizer)
		return done, fmt.Sprintf("applied %v, status %+v, finalizers %v", applied, object.Status, object.Finalizers)
	})
	release, _ := helm.Release("qdrant-db", w.Namespace)
	if release.Namespace != w.Namespace || release.Values["replicaCount"] != 2 {
		t.Errorf("release %+v", release)
	}
	object := clusterFetch(t, w, "db")
	if object.Status.HelmRelease != "qdrant-db" || object.Status.AppliedHash == "" {
		t.Errorf("status %+v", object.Status)
	}
}

func TestClusterControllerAppliesAgainOnSpecChange(t *testing.T) {
	helm := newClusterHelm(false)
	w := startWorld(t, nil, registerClusters(helm))
	create(t, w, clusterObject("db", clusterSpec(1)))
	eventually(t, func() (bool, string) {
		_, applied := helm.Release("qdrant-db", w.Namespace)
		return applied, "release missing"
	})

	object := clusterFetch(t, w, "db")
	replicas := int32(3)
	object.Spec.Replicas = &replicas
	if err := w.Direct.Update(context.Background(), object); err != nil {
		t.Fatal(err)
	}

	eventually(t, func() (bool, string) {
		release, _ := helm.Release("qdrant-db", w.Namespace)
		return release.Values["replicaCount"] == 3, fmt.Sprintf("values %v", release.Values)
	})
}

func TestClusterControllerRetriesAFailedApplyAfterTheRetryDelay(t *testing.T) {
	helm := newClusterHelm(true)
	w := startWorld(t, nil, registerClusters(helm))
	create(t, w, clusterObject("db", clusterSpec(1)))

	eventually(t, func() (bool, string) {
		object := clusterFetch(t, w, "db")
		return clusterApplyFailed(object), fmt.Sprintf("status %+v", object.Status)
	})
	eventually(t, func() (bool, string) {
		attempts := helm.attempts.Load()
		return attempts >= 2, fmt.Sprintf("attempts %d", attempts)
	})

	helm.failing.Store(false)
	eventually(t, func() (bool, string) {
		object := clusterFetch(t, w, "db")
		_, applied := helm.Release("qdrant-db", w.Namespace)
		return applied && !clusterApplyFailed(object), fmt.Sprintf("applied %v, status %+v", applied, object.Status)
	})
}

func TestClusterControllerUninstallsTheReleaseOnDelete(t *testing.T) {
	helm := newClusterHelm(false)
	w := startWorld(t, nil, registerClusters(helm))
	create(t, w, clusterObject("db", clusterSpec(1)))
	eventually(t, func() (bool, string) {
		object := clusterFetch(t, w, "db")
		_, applied := helm.Release("qdrant-db", w.Namespace)
		return applied && object.Status.ObservedGeneration == object.Generation, fmt.Sprintf("applied %v, status %+v", applied, object.Status)
	})

	object := clusterFetch(t, w, "db")
	if err := w.Direct.Delete(context.Background(), object); err != nil {
		t.Fatal(err)
	}

	eventually(t, func() (bool, string) {
		err := w.Direct.Get(context.Background(), client.ObjectKeyFromObject(object), &qdrantv1alpha1.QdrantCluster{})
		return apierrors.IsNotFound(err), fmt.Sprintf("get: %v", err)
	})
	if releases := helm.Releases(); len(releases) != 0 {
		t.Errorf("releases remain: %+v", releases)
	}
}

func TestClusterControllerKeepsObservingBetweenApplies(t *testing.T) {
	helm := newClusterHelm(false)
	w := startWorld(t, nil, registerClusters(helm))
	create(t, w, clusterObject("db", clusterSpec(1)))
	eventually(t, func() (bool, string) {
		object := clusterFetch(t, w, "db")
		return object.Status.Phase != "" && len(object.Status.Conditions) > 1, fmt.Sprintf("status %+v", object.Status)
	})

	consistently(t, 3*time.Second, func() (bool, string) {
		attempts := helm.attempts.Load()
		return attempts == 1, fmt.Sprintf("apply attempts %d", attempts)
	})
}
