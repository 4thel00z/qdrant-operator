package usecases_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/fakes"
	"github.com/4thel00z/qdrant-operator/internal/usecases"
)

func collectionPending() domain.CollectionStatus {
	return domain.CollectionStatus{Phase: domain.CollectionPending}
}

func collectionNumberIs(value any, want int) bool {
	return fmt.Sprint(value) == fmt.Sprint(want)
}

func (f *collectionFixture) reconcile() usecases.ReconcileCollection {
	return usecases.ReconcileCollection{Qdrant: f.qdrant, Kubernetes: f.kubernetes, Clock: f.clock()}
}

func collectionLiveConfig(f *collectionFixture, name string) domain.JSON {
	config, _ := f.qdrant.Collections[collectionNodeURL(0, "db")][name]["config"].(domain.JSON)
	return config
}

func collectionLiveParams(f *collectionFixture, name string) domain.JSON {
	params, _ := collectionLiveConfig(f, name)["params"].(domain.JSON)
	return params
}

func TestReconcileCollectionCreatesCollectionIndexesAndAliases(t *testing.T) {
	f := newCollectionFixture()
	spec := collectionSpec(t, "articles", domain.JSON{
		"payloadIndexes": []any{
			domain.JSON{"field": "city", "type": "keyword"},
			domain.JSON{"field": "body", "type": "text", "params": domain.JSON{"tokenizer": "word"}},
		},
		"aliases":           []any{"articles-live"},
		"replicationFactor": 2,
	})

	status, err := f.reconcile().Execute(context.Background(), spec, 1, collectionPending())
	if err != nil {
		t.Fatal(err)
	}

	if status.Phase != domain.CollectionReady || status.CollectionName != "articles" || status.Health != "green" {
		t.Errorf("status: %+v", status)
	}
	if status.ObservedGeneration == nil || *status.ObservedGeneration != 1 {
		t.Errorf("observed generation: %v", status.ObservedGeneration)
	}
	if !reflect.DeepEqual(status.PayloadIndexes, []string{"body", "city"}) {
		t.Errorf("payload indexes: %v", status.PayloadIndexes)
	}
	if !reflect.DeepEqual(status.Aliases, []string{"articles-live"}) {
		t.Errorf("aliases: %v", status.Aliases)
	}
	if !reflect.DeepEqual(f.qdrant.Aliases, map[string]string{"articles-live": "articles"}) {
		t.Errorf("live aliases: %v", f.qdrant.Aliases)
	}
	if got := collectionLiveParams(f, "articles")["replication_factor"]; got != 2 {
		t.Errorf("replication_factor: %v", got)
	}
	if len(f.qdrant.Updates) != 0 {
		t.Errorf("updates: %v", f.qdrant.Updates)
	}
	if len(status.Conditions) != 1 || status.Conditions[0].Type != "Ready" {
		t.Errorf("conditions: %+v", status.Conditions)
	}
}

func TestReconcileCollectionIsIdempotentAndPatchesOnlyMutableDrift(t *testing.T) {
	f := newCollectionFixture()
	spec := collectionSpec(t, "docs", domain.JSON{
		"optimizers": domain.JSON{"indexing_threshold": 5000},
		"aliases":    []any{"docs-live"},
	})
	use := f.reconcile()
	ctx := context.Background()

	first, err := use.Execute(ctx, spec, 1, collectionPending())
	if err != nil {
		t.Fatal(err)
	}
	second, err := use.Execute(ctx, spec, 1, first)
	if err != nil {
		t.Fatal(err)
	}
	replication := 2
	changed := spec
	changed.ReplicationFactor = &replication
	third, err := use.Execute(ctx, changed, 2, second)
	if err != nil {
		t.Fatal(err)
	}

	wantUpdates := []fakes.CollectionUpdate{{Collection: "docs", Body: domain.JSON{"params": domain.JSON{"replication_factor": 2}}}}
	if !reflect.DeepEqual(f.qdrant.Updates, wantUpdates) {
		t.Errorf("updates: %v", f.qdrant.Updates)
	}
	wantAliases := [][]domain.JSON{{{"create_alias": domain.JSON{"collection_name": "docs", "alias_name": "docs-live"}}}}
	if !reflect.DeepEqual(f.qdrant.AliasChanges, wantAliases) {
		t.Errorf("alias changes: %v", f.qdrant.AliasChanges)
	}
	if third.ObservedGeneration == nil || *third.ObservedGeneration != 2 {
		t.Errorf("observed generation: %v", third.ObservedGeneration)
	}
	if !second.Conditions[0].LastTransitionTime.Equal(first.Conditions[0].LastTransitionTime) {
		t.Errorf("transition time moved: %v -> %v", first.Conditions[0].LastTransitionTime, second.Conditions[0].LastTransitionTime)
	}
}

func TestReconcileCollectionReportsImmutableMismatchWithoutTouchingQdrant(t *testing.T) {
	f := newCollectionFixture()
	f.qdrant.AddCollection(collectionNodeURL(0, "db"), "docs", 7, nil)
	spec := collectionSpec(t, "docs", domain.JSON{
		"vectors": []any{domain.JSON{"size": 8, "distance": "Cosine"}},
		"aliases": []any{"docs-live"},
	})

	status, err := f.reconcile().Execute(context.Background(), spec, 3, collectionPending())
	if err != nil {
		t.Fatal(err)
	}

	if status.Phase != domain.CollectionDegraded {
		t.Errorf("phase: %s", status.Phase)
	}
	if status.PointsCount == nil || *status.PointsCount != 7 {
		t.Errorf("points count: %v", status.PointsCount)
	}
	if status.Conditions[0].Reason != "ImmutableFieldMismatch" {
		t.Errorf("reason: %s", status.Conditions[0].Reason)
	}
	if !strings.Contains(status.Error, "re-creating") {
		t.Errorf("error: %q", status.Error)
	}
	if len(f.qdrant.Updates) != 0 || len(f.qdrant.AliasChanges) != 0 {
		t.Errorf("touched qdrant: %v %v", f.qdrant.Updates, f.qdrant.AliasChanges)
	}
}

func TestReconcileCollectionDropsOnlyIndexesAndAliasesItCreated(t *testing.T) {
	f := newCollectionFixture()
	ctx := context.Background()
	node := domain.QdrantNode{URL: collectionNodeURL(0, "db")}
	f.qdrant.AddCollection(node.URL, "docs", 0, nil)
	for field, schema := range map[string]string{"handmade": "integer", "city": "integer"} {
		if err := f.qdrant.CreatePayloadIndex(ctx, node, "docs", field, schema); err != nil {
			t.Fatal(err)
		}
	}
	f.qdrant.Aliases["foreign"] = "other"
	f.qdrant.IndexChanges = nil
	use := f.reconcile()

	first, err := use.Execute(ctx, collectionSpec(t, "docs", domain.JSON{
		"payloadIndexes": []any{domain.JSON{"field": "city", "type": "keyword"}},
		"aliases":        []any{"a"},
	}), 1, collectionPending())
	if err != nil {
		t.Fatal(err)
	}
	second, err := use.Execute(ctx, collectionSpec(t, "docs", domain.JSON{"aliases": []any{"b"}}), 2, first)
	if err != nil {
		t.Fatal(err)
	}

	wantChanges := []fakes.IndexChange{
		{Action: "delete", Collection: "docs", Field: "city"},
		{Action: "create", Collection: "docs", Field: "city"},
		{Action: "delete", Collection: "docs", Field: "city"},
	}
	if !reflect.DeepEqual(f.qdrant.IndexChanges, wantChanges) {
		t.Errorf("index changes: %v", f.qdrant.IndexChanges)
	}
	schema, _ := f.qdrant.Collections[node.URL]["docs"]["payload_schema"].(domain.JSON)
	if _, kept := schema["handmade"]; !kept {
		t.Errorf("handmade index dropped: %v", schema)
	}
	if !reflect.DeepEqual(f.qdrant.Aliases, map[string]string{"foreign": "other", "b": "docs"}) {
		t.Errorf("aliases: %v", f.qdrant.Aliases)
	}
	if !reflect.DeepEqual(first.PayloadIndexes, []string{"city"}) || len(second.PayloadIndexes) != 0 {
		t.Errorf("payload indexes: %v %v", first.PayloadIndexes, second.PayloadIndexes)
	}
	if !reflect.DeepEqual(first.Aliases, []string{"a"}) || !reflect.DeepEqual(second.Aliases, []string{"b"}) {
		t.Errorf("aliases: %v %v", first.Aliases, second.Aliases)
	}
}

func TestReconcileCollectionWaitsForTheCluster(t *testing.T) {
	f := newCollectionFixture()
	spec := collectionSpec(t, "docs", nil)
	spec.ClusterRef = domain.ClusterRef{Name: "ghost", Namespace: collectionFixtureNS}

	_, err := f.reconcile().Execute(context.Background(), spec, 1, collectionPending())

	if !errors.Is(err, usecases.ErrClusterNotFound) || !usecases.Retryable(err) {
		t.Fatalf("want retryable ErrClusterNotFound, got %v", err)
	}
}

func TestDeleteCollectionHonoursDeletionPolicy(t *testing.T) {
	f := newCollectionFixture()
	f.qdrant.AddCollection(collectionNodeURL(0, "db"), "docs", 0, nil)
	f.qdrant.Aliases["docs-live"] = "docs"
	use := usecases.DeleteCollection{Qdrant: f.qdrant, Kubernetes: f.kubernetes}
	ctx := context.Background()
	ghost := collectionSpec(t, "docs", domain.JSON{"deletionPolicy": "Delete"})
	ghost.ClusterRef = domain.ClusterRef{Name: "ghost", Namespace: collectionFixtureNS}

	retained, err := use.Execute(ctx, collectionSpec(t, "docs", nil))
	if err != nil {
		t.Fatal(err)
	}
	dropped, err := use.Execute(ctx, collectionSpec(t, "docs", domain.JSON{"deletionPolicy": "Delete"}))
	if err != nil {
		t.Fatal(err)
	}
	gone, err := use.Execute(ctx, ghost)
	if err != nil {
		t.Fatal(err)
	}

	if retained || !dropped || gone {
		t.Errorf("retained %v, dropped %v, gone %v", retained, dropped, gone)
	}
	if len(f.qdrant.Collections[collectionNodeURL(0, "db")]) != 0 || len(f.qdrant.Aliases) != 0 {
		t.Errorf("left behind: %v %v", f.qdrant.Collections, f.qdrant.Aliases)
	}
}

func TestReconcileCollectionCreatesMissingShardKeysOnly(t *testing.T) {
	f := newCollectionFixture()
	ctx := context.Background()
	use := f.reconcile()
	custom := domain.JSON{
		"shardingMethod": "custom",
		"shardKeys":      []any{domain.JSON{"key": "eu"}, domain.JSON{"key": "us", "shardsNumber": 2}},
	}

	first, err := use.Execute(ctx, collectionSpec(t, "docs", custom), 1, collectionPending())
	if err != nil {
		t.Fatal(err)
	}
	trimmed, err := use.Execute(ctx, collectionSpec(t, "docs", domain.JSON{
		"shardingMethod": "custom",
		"shardKeys":      []any{domain.JSON{"key": "eu"}},
	}), 2, first)
	if err != nil {
		t.Fatal(err)
	}

	want := []any{"eu", "us"}
	if !reflect.DeepEqual(first.ShardKeys, want) || !reflect.DeepEqual(trimmed.ShardKeys, want) {
		t.Errorf("shard keys: %v %v", first.ShardKeys, trimmed.ShardKeys)
	}
	wantBodies := []fakes.ShardKeyBody{
		{Collection: "docs", Body: domain.JSON{"shard_key": "eu"}},
		{Collection: "docs", Body: domain.JSON{"shard_key": "us", "shards_number": 2}},
	}
	if !reflect.DeepEqual(f.qdrant.ShardKeyBodies, wantBodies) {
		t.Errorf("shard key bodies: %v", f.qdrant.ShardKeyBodies)
	}
}

func collectionReconcileTwice(t *testing.T, f *collectionFixture, first domain.CollectionSpec, second domain.CollectionSpec) (domain.CollectionStatus, domain.CollectionStatus) {
	t.Helper()
	use := f.reconcile()
	firstStatus, err := use.Execute(context.Background(), first, 1, collectionPending())
	if err != nil {
		t.Fatal(err)
	}
	secondStatus, err := use.Execute(context.Background(), second, 2, firstStatus)
	if err != nil {
		t.Fatal(err)
	}
	return firstStatus, secondStatus
}

func collectionLastUpdate(t *testing.T, f *collectionFixture) fakes.CollectionUpdate {
	t.Helper()
	if len(f.qdrant.Updates) == 0 {
		t.Fatal("no update was sent")
	}
	return f.qdrant.Updates[len(f.qdrant.Updates)-1]
}

func TestRemovedOptimizerFieldsResetToQdrantDefaults(t *testing.T) {
	f := newCollectionFixture()
	bulkLoad := collectionSpec(t, "docs", domain.JSON{
		"optimizers": domain.JSON{"max_optimization_threads": 0, "indexing_threshold": 0, "flush_interval_sec": 9},
	})
	steady := collectionSpec(t, "docs", domain.JSON{"optimizers": domain.JSON{"flush_interval_sec": 9}})

	first, second := collectionReconcileTwice(t, f, bulkLoad, steady)

	wantApplied := map[string][]string{"optimizers": {"flush_interval_sec", "indexing_threshold", "max_optimization_threads"}}
	if !reflect.DeepEqual(first.AppliedFields, wantApplied) {
		t.Errorf("applied fields: %v", first.AppliedFields)
	}
	wantUpdate := fakes.CollectionUpdate{Collection: "docs", Body: domain.JSON{
		"optimizers_config": domain.JSON{"indexing_threshold": 10000, "max_optimization_threads": "auto"},
	}}
	if got := collectionLastUpdate(t, f); !reflect.DeepEqual(got, wantUpdate) {
		t.Errorf("update: %v", got)
	}
	live, _ := collectionLiveConfig(f, "docs")["optimizer_config"].(domain.JSON)
	if live["indexing_threshold"] != 10000 || live["max_optimization_threads"] != nil || !collectionNumberIs(live["flush_interval_sec"], 9) {
		t.Errorf("live optimizers: %v", live)
	}
	if !reflect.DeepEqual(second.AppliedFields, map[string][]string{"optimizers": {"flush_interval_sec"}}) {
		t.Errorf("applied fields: %v", second.AppliedFields)
	}
	wantMessage := "Collection docs matches the spec; reset to Qdrant defaults: " +
		"optimizers_config.indexing_threshold, optimizers_config.max_optimization_threads"
	if second.Conditions[0].Message != wantMessage {
		t.Errorf("message: %q", second.Conditions[0].Message)
	}
}

func TestRemovedFieldsResetAcrossHNSWParamsQuantizationAndStrictMode(t *testing.T) {
	f := newCollectionFixture()
	tuned := collectionSpec(t, "docs", domain.JSON{
		"hnsw":              domain.JSON{"m": 32, "on_disk": true},
		"replicationFactor": 2,
		"onDiskPayload":     false,
		"quantization":      domain.JSON{"scalar": domain.JSON{"type": "int8"}},
		"strictMode":        domain.JSON{"enabled": true},
	})

	_, second := collectionReconcileTwice(t, f, tuned, collectionSpec(t, "docs", nil))

	wantUpdate := fakes.CollectionUpdate{Collection: "docs", Body: domain.JSON{
		"hnsw_config":         domain.JSON{"m": 16, "on_disk": false},
		"params":              domain.JSON{"replication_factor": 1, "on_disk_payload": true},
		"quantization_config": "Disabled",
		"strict_mode_config":  domain.JSON{"enabled": false},
	}}
	if got := collectionLastUpdate(t, f); !reflect.DeepEqual(got, wantUpdate) {
		t.Errorf("update: %v", got)
	}
	config := collectionLiveConfig(f, "docs")
	hnsw, _ := config["hnsw_config"].(domain.JSON)
	if config["quantization_config"] != nil || hnsw["m"] != 16 {
		t.Errorf("live config: %v", config)
	}
	if len(second.AppliedFields) != 0 {
		t.Errorf("applied fields: %v", second.AppliedFields)
	}
}

func TestReconcileResetsNothingForFieldsItNeverApplied(t *testing.T) {
	f := newCollectionFixture()
	f.qdrant.AddCollection(collectionNodeURL(0, "db"), "docs", 0, domain.JSON{
		"vectors":           domain.JSON{"size": 4, "distance": "Cosine"},
		"optimizers_config": domain.JSON{"indexing_threshold": 0},
		"hnsw_config":       domain.JSON{"m": 64},
	})

	status, err := f.reconcile().Execute(context.Background(), collectionSpec(t, "docs", nil), 1, collectionPending())
	if err != nil {
		t.Fatal(err)
	}

	if len(f.qdrant.Updates) != 0 {
		t.Errorf("updates: %v", f.qdrant.Updates)
	}
	config := collectionLiveConfig(f, "docs")
	optimizers, _ := config["optimizer_config"].(domain.JSON)
	hnsw, _ := config["hnsw_config"].(domain.JSON)
	if optimizers["indexing_threshold"] != 0 || hnsw["m"] != 64 {
		t.Errorf("live config changed: %v", config)
	}
	if status.Conditions[0].Message != "Collection docs matches the spec" {
		t.Errorf("message: %q", status.Conditions[0].Message)
	}
}

func TestRemovedFieldAlreadyAtDefaultSendsNoPatch(t *testing.T) {
	f := newCollectionFixture()

	_, second := collectionReconcileTwice(t, f,
		collectionSpec(t, "docs", domain.JSON{"optimizers": domain.JSON{"indexing_threshold": 10000}}),
		collectionSpec(t, "docs", nil))

	if len(f.qdrant.Updates) != 0 {
		t.Errorf("updates: %v", f.qdrant.Updates)
	}
	if len(second.AppliedFields) != 0 {
		t.Errorf("applied fields: %v", second.AppliedFields)
	}
}
