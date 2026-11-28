package fakes_test

import (
	"context"
	"errors"
	"testing"

	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/fakes"
	"github.com/4thel00z/qdrant-operator/internal/ports"
)

func TestPatchStatusMergesAndClearsNil(t *testing.T) {
	ctx := context.Background()
	kubernetes := fakes.NewKubernetes()
	kubernetes.PutResource(domain.JSON{
		"kind":     "QdrantBackup",
		"metadata": domain.JSON{"name": "nightly", "namespace": "search"},
		"status":   domain.JSON{"phase": "Running", "inProgressSnapshot": domain.JSON{"collection": "docs"}},
	})
	ref := domain.ResourceRef{Kind: domain.Backups, Name: "nightly", Namespace: "search"}
	if err := kubernetes.PatchStatus(ctx, ref, domain.JSON{"inProgressSnapshot": nil, "phase": "Completed"}); err != nil {
		t.Fatal(err)
	}
	body, err := kubernetes.CustomResource(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	status := body["status"].(domain.JSON)
	if status["phase"] != "Completed" || status["inProgressSnapshot"] != nil {
		t.Errorf("status: %v", status)
	}
	if metadata := body["metadata"].(domain.JSON); metadata["uid"] != "uid-nightly" {
		t.Errorf("metadata: %v", metadata)
	}
}

func TestApplySecretRefusesAForeignSecret(t *testing.T) {
	ctx := context.Background()
	kubernetes := fakes.NewKubernetes()
	kubernetes.Secrets["search/keys"] = map[string]string{"api-key": "theirs"}
	err := kubernetes.ApplySecret(ctx, "keys", "search", map[string]string{"api-key": "ours"}, domain.OwnerReference{UID: "uid-demo"})
	if !errors.Is(err, ports.ErrSecretNotOwned) {
		t.Fatalf("want ErrSecretNotOwned, got %v", err)
	}
	if err := kubernetes.ApplySecret(ctx, "fresh", "search", map[string]string{"api-key": "ours"}, domain.OwnerReference{UID: "uid-demo"}); err != nil {
		t.Fatal(err)
	}
	value, err := kubernetes.SecretValue(ctx, domain.SecretRef{Name: "fresh", Key: "api-key", Namespace: "search"})
	if err != nil || value != "ours" {
		t.Errorf("value %q, err %v", value, err)
	}
	if _, err := kubernetes.SecretValue(ctx, domain.SecretRef{Name: "fresh", Key: "missing", Namespace: "search"}); !errors.Is(err, ports.ErrSecretKeyNotFound) {
		t.Errorf("want ErrSecretKeyNotFound, got %v", err)
	}
}

func TestListCustomResourcesFiltersByLabel(t *testing.T) {
	ctx := context.Background()
	kubernetes := fakes.NewKubernetes()
	for _, name := range []string{"a", "b"} {
		kubernetes.PutResource(domain.JSON{
			"kind":     "QdrantBackup",
			"metadata": domain.JSON{"name": name, "namespace": "search", "labels": domain.JSON{domain.ScheduleLabel: name}},
		})
	}
	bodies, err := kubernetes.ListCustomResources(ctx, domain.Backups, "search", domain.ScheduleLabel+"=b")
	if err != nil || len(bodies) != 1 {
		t.Fatalf("bodies %v, err %v", bodies, err)
	}
}
