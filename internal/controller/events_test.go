package controller_test

import (
	"context"
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	qdrantv1alpha1 "github.com/4thel00z/qdrant-operator/api/v1alpha1"
	"github.com/4thel00z/qdrant-operator/internal/controller"
	"github.com/4thel00z/qdrant-operator/internal/domain"
)

func TestPhaseChangesAreRecordedAsEvents(t *testing.T) {
	w := startWorld(t, routeCluster("db"), registerCollections)
	create(t, w, clusterObject("db", qdrantv1alpha1.QdrantClusterSpec{Version: "1.16.3"}))
	create(t, w, collectionObject("articles", "db", ""))

	eventually(t, func() (bool, string) {
		recorded := &eventsv1.EventList{}
		if err := w.Direct.List(context.Background(), recorded, client.InNamespace(w.Namespace)); err != nil {
			return false, err.Error()
		}
		for _, event := range recorded.Items {
			if event.Regarding.Name == "articles" && event.Reason == "Ready" && event.Type == corev1.EventTypeNormal {
				return true, ""
			}
		}
		return false, fmt.Sprintf("events %+v", recorded.Items)
	})
}

func TestFailedPhaseIsAWarning(t *testing.T) {
	cases := map[string]string{
		"Failed":    corev1.EventTypeWarning,
		"Ready":     corev1.EventTypeNormal,
		"Running":   corev1.EventTypeNormal,
		"Completed": corev1.EventTypeNormal,
	}
	for phase, want := range cases {
		if got := controller.EventType(phase); got != want {
			t.Errorf("%s: %s, want %s", phase, got, want)
		}
	}
}

func TestEventNotePrefersTheStatusError(t *testing.T) {
	cases := []struct {
		before string
		after  domain.JSON
		want   string
	}{
		{"Running", domain.JSON{"phase": "Failed", "error": "snapshot failed"}, "snapshot failed"},
		{"", domain.JSON{"phase": "Running", "message": "copied 10 points"}, "copied 10 points"},
		{"Pending", domain.JSON{"phase": "Ready"}, "phase changed from Pending to Ready"},
		{"", domain.JSON{"phase": "Pending"}, "phase is Pending"},
	}
	for _, c := range cases {
		if got := controller.EventNote(c.before, c.after); got != c.want {
			t.Errorf("%s -> %v: %q, want %q", c.before, c.after, got, c.want)
		}
	}
}
