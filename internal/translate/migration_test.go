package translate_test

import (
	"encoding/json"
	"slices"
	"testing"

	qdrantv1alpha1 "github.com/4thel00z/qdrant-operator/api/v1alpha1"
	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/translate"
)

type migrationCases struct {
	Specs           []migrationSpecCase   `json:"specs"`
	SelectionInputs [][]string            `json:"selection_inputs"`
	TargetNames     []string              `json:"target_name_inputs"`
	CreateBodies    []migrationConfigCase `json:"create_bodies"`
	PayloadSchemas  []migrationSchemaCase `json:"payload_schemas"`
	VectorLayouts   []migrationConfigCase `json:"vector_layouts"`
	Routes          []migrationRouteCase  `json:"routes"`
	PointOrders     [][]domain.PointID    `json:"point_orders"`
	Digests         []migrationDigestCase `json:"digests"`
	SyncWindows     []migrationWindowCase `json:"sync_windows"`
	Statuses        []migrationStatusCase `json:"statuses"`
}

type migrationSpecCase struct {
	Name string          `json:"name"`
	Spec json.RawMessage `json:"spec"`
}

type migrationConfigCase struct {
	Name   string          `json:"name"`
	Target json.RawMessage `json:"target"`
	Config domain.JSON     `json:"config"`
}

type migrationSchemaCase struct {
	Name   string      `json:"name"`
	Schema domain.JSON `json:"schema"`
}

type migrationRouteCase struct {
	Name   string          `json:"name"`
	Target json.RawMessage `json:"target"`
	Points []domain.Point  `json:"points"`
}

type migrationDigestCase struct {
	Name  string       `json:"name"`
	Point domain.Point `json:"point"`
}

type migrationWindowCase struct {
	Name   string         `json:"name"`
	Source []domain.Point `json:"source"`
	Target []domain.Point `json:"target"`
}

type migrationStatusCase struct {
	Name         string          `json:"name"`
	Status       json.RawMessage `json:"status"`
	ResumeInputs []string        `json:"resume_inputs"`
}

type migrationGolden struct {
	Specs          []map[string]any   `json:"specs"`
	CreateBodies   []map[string]any   `json:"create_bodies"`
	PayloadSchemas []map[string]any   `json:"payload_schemas"`
	VectorLayouts  []map[string]any   `json:"vector_layouts"`
	Routes         []map[string]any   `json:"routes"`
	PointOrders    [][]domain.PointID `json:"point_orders"`
	Digests        []string           `json:"digests"`
	SyncWindows    []map[string]any   `json:"sync_windows"`
	Statuses       []map[string]any   `json:"statuses"`
}

func loadMigration(t *testing.T) (migrationCases, migrationGolden) {
	t.Helper()
	cases := migrationCases{}
	golden := migrationGolden{}
	readJSON(t, "testdata/migration_cases.json", &cases)
	readJSON(t, "testdata/migration_golden.json", &golden)
	sizes := [][2]int{
		{len(cases.Specs), len(golden.Specs)},
		{len(cases.CreateBodies), len(golden.CreateBodies)},
		{len(cases.PayloadSchemas), len(golden.PayloadSchemas)},
		{len(cases.VectorLayouts), len(golden.VectorLayouts)},
		{len(cases.Routes), len(golden.Routes)},
		{len(cases.PointOrders), len(golden.PointOrders)},
		{len(cases.Digests), len(golden.Digests)},
		{len(cases.SyncWindows), len(golden.SyncWindows)},
		{len(cases.Statuses), len(golden.Statuses)},
	}
	for _, size := range sizes {
		if size[0] != size[1] {
			t.Fatal("migration golden data is stale; regenerate it with hack/golden/migration_values.py")
		}
	}
	return cases, golden
}

func decodeTargetOverrides(t *testing.T, raw json.RawMessage) domain.TargetOverrides {
	t.Helper()
	target := qdrantv1alpha1.MigrationTarget{}
	decodeStrict(t, raw, &target)
	spec := qdrantv1alpha1.QdrantMigrationSpec{
		Source:           qdrantv1alpha1.MigrationSource{ClusterRef: &qdrantv1alpha1.ClusterRef{Name: "old"}},
		TargetClusterRef: qdrantv1alpha1.ClusterRef{Name: "new"},
		Target:           &target,
	}
	migration, err := translate.MigrationSpecFromAPI("move", "tenant-a", spec)
	if err != nil {
		t.Fatalf("translate target: %v", err)
	}
	return migration.Target
}

func secretRefOutput(ref *domain.SecretRef) any {
	if ref == nil {
		return nil
	}
	return map[string]any{"name": ref.Name, "key": ref.Key, "namespace": ref.Namespace}
}

func clusterRefOutput(ref *domain.ClusterRef) any {
	if ref == nil {
		return nil
	}
	return map[string]any{"name": ref.Name, "namespace": ref.Namespace}
}

func shardKeyOutput(key domain.PointShardKey) any {
	if key.Kind == domain.ShardKeyText {
		return key.Text
	}
	if key.Kind == domain.ShardKeyNumber {
		return key.Number
	}
	return nil
}

func TestMigrationSpecOutputsMatchPythonGolden(t *testing.T) {
	cases, golden := loadMigration(t)
	for index, testCase := range cases.Specs {
		t.Run(testCase.Name, func(t *testing.T) {
			apiSpec := qdrantv1alpha1.QdrantMigrationSpec{}
			decodeStrict(t, testCase.Spec, &apiSpec)
			spec, err := translate.MigrationSpecFromAPI("move", "tenant-a", apiSpec)
			if err != nil {
				compareJSON(t, golden.Specs[index], map[string]any{"error": true})
				return
			}
			var endpoint any
			if spec.Source.Endpoint != nil {
				endpoint = map[string]any{
					"url":         spec.Source.Endpoint.URL,
					"api_key_ref": secretRefOutput(spec.Source.Endpoint.APIKeyRef),
					"ca_ref":      secretRefOutput(spec.Source.Endpoint.CARef),
				}
			}
			names := map[string]any{}
			for _, name := range cases.TargetNames {
				names[name] = spec.TargetName(name)
			}
			selections := []any{}
			for _, available := range cases.SelectionInputs {
				selected, err := spec.SelectCollections(available)
				if err != nil {
					selections = append(selections, map[string]any{"error": true})
					continue
				}
				selections = append(selections, map[string]any{"error": false, "selected": selected})
			}
			compareJSON(t, golden.Specs[index], map[string]any{
				"error":                  false,
				"source_description":     spec.Source.Description(),
				"source_cluster_ref":     clusterRefOutput(spec.Source.ClusterRef),
				"endpoint":               endpoint,
				"target_cluster_ref":     clusterRefOutput(&spec.TargetClusterRef),
				"collections":            nonNil(spec.Collections),
				"collection_mapping":     spec.CollectionMapping,
				"batch_size":             spec.BatchSize,
				"batch_delay_ms":         spec.BatchDelay.Milliseconds(),
				"checkpoint_batches":     spec.CheckpointBatches,
				"verify_passes":          spec.VerifyPasses,
				"sync":                   spec.Sync,
				"sync_max_passes":        spec.SyncMaxPasses,
				"create_missing":         spec.CreateMissing,
				"ensure_payload_indexes": spec.EnsurePayloadIndexes,
				"target_params":          spec.Target.ToParams(),
				"shard_key_params":       spec.Target.ShardKeyParams(),
				"shard_key":              shardKeyOutput(spec.Target.ShardKey),
				"shard_key_field":        stringOrNil(spec.Target.ShardKeyField),
				"target_names":           names,
				"selections":             selections,
			})
		})
	}
}

func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func stringOrNil(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func TestMigrationCreateBodiesMatchPythonGolden(t *testing.T) {
	cases, golden := loadMigration(t)
	for index, testCase := range cases.CreateBodies {
		t.Run(testCase.Name, func(t *testing.T) {
			overrides := decodeTargetOverrides(t, testCase.Target)
			compareJSON(t, golden.CreateBodies[index], map[string]any{
				"body": domain.CreateBodyFromConfig(testCase.Config, overrides),
			})
		})
	}
}

func TestMigrationPayloadIndexesMatchPythonGolden(t *testing.T) {
	cases, golden := loadMigration(t)
	for index, testCase := range cases.PayloadSchemas {
		t.Run(testCase.Name, func(t *testing.T) {
			indexes, err := domain.PayloadIndexesFromSchema(testCase.Schema)
			if err != nil {
				compareJSON(t, golden.PayloadSchemas[index], map[string]any{"error": true})
				return
			}
			listed := []any{}
			for _, entry := range indexes {
				listed = append(listed, map[string]any{"field": entry.Field, "schema": entry.Schema})
			}
			compareJSON(t, golden.PayloadSchemas[index], map[string]any{"error": false, "indexes": listed})
		})
	}
}

func TestVectorLayoutMatchesPythonGolden(t *testing.T) {
	cases, golden := loadMigration(t)
	for index, testCase := range cases.VectorLayouts {
		t.Run(testCase.Name, func(t *testing.T) {
			layout, err := domain.VectorLayout(testCase.Config)
			if err != nil {
				compareJSON(t, golden.VectorLayouts[index], map[string]any{"error": true})
				return
			}
			compareJSON(t, golden.VectorLayouts[index], map[string]any{"error": false, "layout": layout})
		})
	}
}

func TestGroupByShardKeyMatchesPythonGolden(t *testing.T) {
	cases, golden := loadMigration(t)
	for index, testCase := range cases.Routes {
		t.Run(testCase.Name, func(t *testing.T) {
			groups, err := domain.GroupByShardKey(testCase.Points, decodeTargetOverrides(t, testCase.Target))
			if err != nil {
				compareJSON(t, golden.Routes[index], map[string]any{"error": true})
				return
			}
			listed := []any{}
			for _, group := range groups {
				listed = append(listed, map[string]any{"key": shardKeyOutput(group.Key), "points": group.Points})
			}
			compareJSON(t, golden.Routes[index], map[string]any{"error": false, "groups": listed})
		})
	}
}

func TestComparePointIDsMatchesPythonOrder(t *testing.T) {
	cases, golden := loadMigration(t)
	for index, ids := range cases.PointOrders {
		sorted := slices.Clone(ids)
		slices.SortFunc(sorted, domain.ComparePointIDs)
		if !slices.Equal(sorted, golden.PointOrders[index]) {
			t.Errorf("order = %v, want %v", sorted, golden.PointOrders[index])
		}
	}
}

func TestPointDigestMatchesPythonGolden(t *testing.T) {
	cases, golden := loadMigration(t)
	for index, testCase := range cases.Digests {
		t.Run(testCase.Name, func(t *testing.T) {
			digest, err := testCase.Point.Digest()
			if err != nil {
				t.Fatalf("digest: %v", err)
			}
			if digest != golden.Digests[index] {
				t.Errorf("digest = %s, want %s", digest, golden.Digests[index])
			}
		})
	}
}

func TestPlanSyncWindowMatchesPythonGolden(t *testing.T) {
	cases, golden := loadMigration(t)
	for index, testCase := range cases.SyncWindows {
		t.Run(testCase.Name, func(t *testing.T) {
			changed, stale, err := domain.PlanSyncWindow(testCase.Source, testCase.Target)
			if err != nil {
				t.Fatalf("plan: %v", err)
			}
			changedIDs := []any{}
			for _, point := range changed {
				changedIDs = append(changedIDs, point.ID)
			}
			staleIDs := []any{}
			for _, id := range stale {
				staleIDs = append(staleIDs, id)
			}
			compareJSON(t, golden.SyncWindows[index], map[string]any{"changed_ids": changedIDs, "stale": staleIDs})
		})
	}
}

func TestMigrationStatusOutputsMatchPythonGolden(t *testing.T) {
	cases, golden := loadMigration(t)
	for index, testCase := range cases.Statuses {
		t.Run(testCase.Name, func(t *testing.T) {
			apiStatus := qdrantv1alpha1.QdrantMigrationStatus{}
			decodeStrict(t, testCase.Status, &apiStatus)
			status, err := translate.MigrationStatusFromAPI(apiStatus)
			if err != nil {
				t.Fatalf("translate: %v", err)
			}
			rendered, err := translate.MigrationStatusToAPI(status)
			if err != nil {
				t.Fatalf("translate back: %v", err)
			}
			progress := status.Progress()
			resumes := map[string]any{}
			for _, name := range testCase.ResumeInputs {
				found, ok := status.ResumePoint(name)
				if !ok {
					resumes[name] = nil
					continue
				}
				collection, err := translate.MigrationStatusToAPI(domain.MigrationStatus{Collections: []domain.CollectionMigration{found}})
				if err != nil {
					t.Fatalf("translate resume point: %v", err)
				}
				resumes[name] = collection.Collections[0]
			}
			compareJSON(t, golden.Statuses[index], map[string]any{
				"api": rendered,
				"progress": map[string]any{
					"collectionsTotal":     progress.CollectionsTotal,
					"collectionsCompleted": progress.CollectionsCompleted,
					"pointsTotal":          progress.PointsTotal,
					"pointsCopied":         progress.PointsCopied,
					"percentage":           progress.Percentage(),
				},
				"resume_points": resumes,
			})
		})
	}
}
