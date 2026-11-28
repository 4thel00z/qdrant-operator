package domain_test

import (
	"reflect"
	"testing"

	"github.com/4thel00z/qdrant-operator/internal/domain"
)

func TestCollectionBodiesKeepIntegersAsIntegers(t *testing.T) {
	shards := 6
	spec := domain.CollectionSpec{
		Vectors:     []domain.VectorSpec{{Size: 4, Distance: "Dot"}},
		ShardNumber: &shards,
	}
	body := spec.CreateBody()
	if _, ok := body["shard_number"].(int); !ok {
		t.Errorf("shard_number is %T", body["shard_number"])
	}
	vectors, ok := body["vectors"].(domain.JSON)
	if !ok {
		t.Fatalf("vectors is %T", body["vectors"])
	}
	if _, ok := vectors["size"].(int); !ok {
		t.Errorf("size is %T", vectors["size"])
	}
}

func TestCollectionSpecValidate(t *testing.T) {
	named := domain.VectorSpec{Name: "a", Size: 1, Distance: "Dot"}
	unnamed := domain.VectorSpec{Size: 1, Distance: "Dot"}
	if err := (domain.CollectionSpec{}).Validate(); err == nil {
		t.Error("a spec without vectors must fail")
	}
	if err := (domain.CollectionSpec{Vectors: []domain.VectorSpec{named, unnamed}}).Validate(); err == nil {
		t.Error("an unnamed vector next to another must fail")
	}
	if err := (domain.CollectionSpec{Vectors: []domain.VectorSpec{unnamed}}).Validate(); err != nil {
		t.Errorf("a single unnamed vector is fine: %v", err)
	}
	if err := (domain.CollectionSpec{SparseVectors: []domain.SparseVectorSpec{{Name: "s"}}}).Validate(); err != nil {
		t.Errorf("sparse only is fine: %v", err)
	}
}

func TestParseCollectionPhaseAndDeletionPolicy(t *testing.T) {
	phase, err := domain.ParseCollectionPhase("")
	if err != nil || phase != domain.CollectionPending {
		t.Errorf("empty phase: %q %v", phase, err)
	}
	if _, err := domain.ParseCollectionPhase("Exploded"); err == nil {
		t.Error("unknown phase must fail")
	}
	policy, err := domain.ParseDeletionPolicy("")
	if err != nil || policy != domain.DeletionRetain {
		t.Errorf("empty policy: %q %v", policy, err)
	}
	if _, err := domain.ParseDeletionPolicy("Bogus"); err == nil {
		t.Error("unknown policy must fail")
	}
}

func TestResetNamesFollowGroupOrder(t *testing.T) {
	resets := map[string]domain.JSON{
		"strict_mode_config": {"enabled": false},
		"hnsw_config":        {"on_disk": false, "m": 16},
		"optimizers_config":  {"deleted_threshold": 0.2},
	}
	want := []string{
		"optimizers_config.deleted_threshold",
		"hnsw_config.m",
		"hnsw_config.on_disk",
		"strict_mode_config.enabled",
	}
	if got := domain.ResetNames(resets); !reflect.DeepEqual(got, want) {
		t.Errorf("want %v, got %v", want, got)
	}
}

func TestCollectionReadyMessage(t *testing.T) {
	if got := domain.CollectionReadyMessage("docs", nil); got != "Collection docs matches the spec" {
		t.Errorf("plain: %q", got)
	}
	got := domain.CollectionReadyMessage("docs", []string{"hnsw_config.m", "params.on_disk_payload"})
	want := "Collection docs matches the spec; reset to Qdrant defaults: hnsw_config.m, params.on_disk_payload"
	if got != want {
		t.Errorf("with resets: %q", got)
	}
}
