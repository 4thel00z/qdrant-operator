package fakes

import (
	"context"
	"errors"
	"testing"

	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/ports"
)

func TestHelmRecordsAppliedReleases(t *testing.T) {
	helm := NewHelm()
	ctx := context.Background()
	request := ports.ReleaseRequest{
		Name:         "db",
		Namespace:    "qdrant",
		ChartVersion: "1.16.3",
		Values:       domain.JSON{"replicaCount": 3},
		StatefulSet:  "db",
		Partition:    2,
	}

	if err := helm.Apply(ctx, request); err != nil {
		t.Fatal(err)
	}

	got, found := helm.Release("db", "qdrant")
	if !found || got.ChartVersion != "1.16.3" || got.Partition != 2 || got.Values["replicaCount"] != 3 {
		t.Fatalf("release = %+v, found %v", got, found)
	}
	exists, err := helm.ReleaseExists(ctx, "db", "qdrant")
	if err != nil || !exists {
		t.Fatalf("exists = %v, err %v", exists, err)
	}
}

func TestHelmUninstallForgetsTheRelease(t *testing.T) {
	helm := NewHelm()
	ctx := context.Background()
	if err := helm.Apply(ctx, ports.ReleaseRequest{Name: "db", Namespace: "qdrant"}); err != nil {
		t.Fatal(err)
	}

	if err := helm.Uninstall(ctx, "db", "qdrant"); err != nil {
		t.Fatal(err)
	}

	if exists, _ := helm.ReleaseExists(ctx, "db", "qdrant"); exists {
		t.Fatal("release still exists")
	}
	if len(helm.Releases()) != 0 {
		t.Fatalf("releases = %+v", helm.Releases())
	}
}

func TestHelmInjectedApplyErrorIsReturned(t *testing.T) {
	helm := NewHelm()
	helm.ApplyErr = errors.New("boom")

	if err := helm.Apply(context.Background(), ports.ReleaseRequest{Name: "db", Namespace: "qdrant"}); !errors.Is(err, helm.ApplyErr) {
		t.Fatalf("err = %v", err)
	}
	if len(helm.Releases()) != 0 {
		t.Fatal("failed apply was recorded")
	}
}
