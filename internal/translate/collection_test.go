package translate_test

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	qdrantv1alpha1 "github.com/4thel00z/qdrant-operator/api/v1alpha1"
	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/translate"
)

var collectionNow = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

type collectionGolden struct {
	ResetGroups   map[string]collectionResetGroup `json:"reset_groups"`
	SpecCases     []collectionSpecCase            `json:"spec_cases"`
	SubsetCases   []collectionSubsetCase          `json:"subset_cases"`
	OptimizerCase []collectionOptimizerCase       `json:"optimizer_cases"`
	DefaultCases  []collectionDefaultCase         `json:"default_cases"`
	StatusCases   []collectionStatusCase          `json:"status_cases"`
}

type collectionResetGroup struct {
	PatchKey string           `json:"patch_key"`
	LiveKey  string           `json:"live_key"`
	Defaults map[string][]any `json:"defaults"`
}

type collectionObserveCase struct {
	Current    json.RawMessage `json:"current"`
	Info       domain.JSON     `json:"info"`
	Generation int64           `json:"generation"`
	Phase      string          `json:"phase"`
	Reason     string          `json:"reason"`
	Message    string          `json:"message"`
}

type collectionSpecCase struct {
	Name              string                 `json:"name"`
	Spec              json.RawMessage        `json:"spec"`
	Config            domain.JSON            `json:"config"`
	Applied           map[string][]string    `json:"applied"`
	PayloadSchema     domain.JSON            `json:"payload_schema"`
	ManagedIndexes    []string               `json:"managed_indexes"`
	ActualAliases     map[string]string      `json:"actual_aliases"`
	ManagedAliases    []string               `json:"managed_aliases"`
	ExistingShardKeys []any                  `json:"existing_shard_keys"`
	Observe           *collectionObserveCase `json:"observe"`
	Outputs           map[string]any         `json:"outputs"`
}

type collectionSubsetCase struct {
	Name    string `json:"name"`
	Desired any    `json:"desired"`
	Actual  any    `json:"actual"`
	Want    bool   `json:"want"`
}

type collectionOptimizerCase struct {
	Name   string      `json:"name"`
	Config domain.JSON `json:"config"`
	Want   any         `json:"want"`
}

type collectionDefaultCase struct {
	Name    string `json:"name"`
	Live    any    `json:"live"`
	Default any    `json:"default"`
	Want    bool   `json:"want"`
}

type collectionStatusCase struct {
	Name    string          `json:"name"`
	Status  json.RawMessage `json:"status"`
	Outputs map[string]any  `json:"outputs"`
}

func readCollectionGolden(t *testing.T) collectionGolden {
	t.Helper()
	golden := collectionGolden{}
	readJSON(t, "testdata/collection_golden.json", &golden)
	return golden
}

func TestCollectionOutputsMatchPythonGolden(t *testing.T) {
	for _, testCase := range readCollectionGolden(t).SpecCases {
		t.Run(testCase.Name, func(t *testing.T) {
			spec := qdrantv1alpha1.QdrantCollectionSpec{}
			decodeStrict(t, testCase.Spec, &spec)
			collection, err := translate.CollectionSpecFromAPI("docs", "tenant-a", spec)
			if err != nil {
				compareJSON(t, testCase.Outputs, map[string]any{"error": true})
				return
			}
			compareJSON(t, testCase.Outputs, collectionOutputs(t, collection, testCase))
		})
	}
}

func collectionOutputs(t *testing.T, spec domain.CollectionSpec, testCase collectionSpecCase) map[string]any {
	t.Helper()
	declared := map[string]any{}
	for group, names := range spec.DeclaredFields() {
		declared[group] = names
	}
	shardKeyBodies := []any{}
	for _, shardKey := range spec.ShardKeys {
		shardKeyBodies = append(shardKeyBodies, shardKey.Body())
	}
	indexSchemas := map[string]any{}
	for _, index := range spec.PayloadIndexes {
		indexSchemas[index.Field] = index.FieldSchema()
	}
	outputs := map[string]any{
		"collection_name":   spec.CollectionName,
		"cluster_ref":       map[string]any{"name": spec.ClusterRef.Name, "namespace": spec.ClusterRef.Namespace},
		"deletion_policy":   string(spec.DeletionPolicy),
		"single_unnamed":    spec.SingleUnnamed(),
		"create_body":       spec.CreateBody(),
		"create_body_exact": marshalExact(t, spec.CreateBody()),
		"immutable_config":  spec.ImmutableConfig(),
		"mutable_config":    spec.MutableConfig(),
		"declared_fields":   declared,
		"shard_key_bodies":  shardKeyBodies,
		"index_schemas":     indexSchemas,
		"alias_names":       spec.AliasNames(),
	}
	if testCase.Config != nil {
		resets := spec.Resets(testCase.Config, testCase.Applied)
		plain := spec.UpdateBody(testCase.Config, nil)
		withResets := spec.UpdateBody(testCase.Config, resets)
		resetBodies := map[string]any{}
		for key, fields := range resets {
			resetBodies[key] = fields
		}
		outputs["immutable_ok"] = domain.IsSubset(spec.ImmutableConfig(), testCase.Config)
		outputs["update_body"] = plain
		outputs["update_body_exact"] = marshalExact(t, plain)
		outputs["update_body_with_resets"] = withResets
		outputs["update_body_with_resets_exact"] = marshalExact(t, withResets)
		outputs["resets"] = resetBodies
		outputs["reset_names"] = domain.ResetNames(resets)
	}
	if testCase.PayloadSchema != nil {
		outputs["index_plan"] = indexPlanOutput(spec.IndexPlan(testCase.PayloadSchema, testCase.ManagedIndexes))
		satisfied := map[string]any{}
		for _, index := range spec.PayloadIndexes {
			actual, ok := testCase.PayloadSchema[index.Field].(domain.JSON)
			if !ok || len(actual) == 0 {
				continue
			}
			satisfied[index.Field] = index.SatisfiedBy(actual)
		}
		outputs["index_satisfied"] = satisfied
	}
	if testCase.ActualAliases != nil {
		actions := []any{}
		for _, action := range spec.AliasActions(testCase.ActualAliases, testCase.ManagedAliases) {
			actions = append(actions, action)
		}
		outputs["alias_plan"] = map[string]any{"actions": actions, "managed": spec.AliasNames()}
	}
	if testCase.ExistingShardKeys != nil {
		plan := spec.ShardKeyPlan(testCase.ExistingShardKeys)
		created := []any{}
		for _, shardKey := range plan.Create {
			created = append(created, shardKey.Body())
		}
		outputs["shard_key_plan"] = map[string]any{"create": created, "keys": plan.Keys, "manages": spec.ManagesShardKeys()}
	}
	if testCase.Observe != nil {
		outputs["observed_status"] = observedOutput(t, spec, *testCase.Observe)
	}
	return outputs
}

func indexPlanOutput(plan domain.IndexPlan) map[string]any {
	operations := []any{}
	for _, operation := range plan.Operations {
		if operation.Delete {
			operations = append(operations, map[string]any{"op": "delete", "field": operation.Field})
			continue
		}
		operations = append(operations, map[string]any{"op": "create", "field": operation.Field, "schema": operation.Schema})
	}
	return map[string]any{"operations": operations, "managed": plan.Managed}
}

func observedOutput(t *testing.T, spec domain.CollectionSpec, observe collectionObserveCase) any {
	t.Helper()
	currentStatus := qdrantv1alpha1.QdrantCollectionStatus{}
	decodeStrict(t, observe.Current, &currentStatus)
	current, err := translate.CollectionStatusFromAPI(currentStatus)
	if err != nil {
		t.Fatalf("current status: %v", err)
	}
	observed := spec.Observe(current, observe.Info, observe.Generation, domain.CollectionPhase(observe.Phase), observe.Reason, observe.Message, collectionNow)
	return statusOutput(t, observed)
}

func statusOutput(t *testing.T, status domain.CollectionStatus) any {
	t.Helper()
	rendered, err := json.Marshal(translate.CollectionStatusToAPI(status))
	if err != nil {
		t.Fatalf("encode status: %v", err)
	}
	var decoded any
	if err := json.Unmarshal(rendered, &decoded); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	return decoded
}

func marshalExact(t *testing.T, value any) string {
	t.Helper()
	rendered, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return string(rendered)
}

func TestCollectionResetGroupsMatchPython(t *testing.T) {
	golden := readCollectionGolden(t)
	if len(golden.ResetGroups) != len(domain.ResetGroups) {
		t.Fatalf("want %d groups, got %d", len(golden.ResetGroups), len(domain.ResetGroups))
	}
	for _, group := range domain.ResetGroups {
		want, ok := golden.ResetGroups[group.Name]
		if !ok {
			t.Errorf("group %s has no golden value", group.Name)
			continue
		}
		defaults := map[string][]any{}
		for name, fallback := range group.Defaults {
			defaults[name] = []any{fallback.Patch, fallback.Live}
		}
		got := collectionResetGroup{PatchKey: group.PatchKey, LiveKey: group.LiveKey, Defaults: defaults}
		rendered, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		normalized := collectionResetGroup{}
		if err := json.Unmarshal(rendered, &normalized); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(want, normalized) {
			t.Errorf("group %s differs\nwant %+v\ngot  %+v", group.Name, want, normalized)
		}
	}
}

func TestIsSubsetMatchesPythonGolden(t *testing.T) {
	for _, testCase := range readCollectionGolden(t).SubsetCases {
		t.Run(testCase.Name, func(t *testing.T) {
			if got := domain.IsSubset(testCase.Desired, testCase.Actual); got != testCase.Want {
				t.Errorf("want %v, got %v", testCase.Want, got)
			}
		})
	}
}

func TestLiveOptimizersMatchPythonGolden(t *testing.T) {
	for _, testCase := range readCollectionGolden(t).OptimizerCase {
		t.Run(testCase.Name, func(t *testing.T) {
			var got any
			if live := domain.LiveOptimizers(testCase.Config); live != nil {
				got = live
			}
			if !reflect.DeepEqual(got, testCase.Want) {
				t.Errorf("want %v, got %v", testCase.Want, got)
			}
		})
	}
}

func TestAtDefaultMatchesPythonGolden(t *testing.T) {
	for _, testCase := range readCollectionGolden(t).DefaultCases {
		t.Run(testCase.Name, func(t *testing.T) {
			if got := domain.AtDefault(testCase.Live, testCase.Default); got != testCase.Want {
				t.Errorf("want %v, got %v", testCase.Want, got)
			}
		})
	}
}

func TestCollectionStatusRoundTripMatchesPythonGolden(t *testing.T) {
	for _, testCase := range readCollectionGolden(t).StatusCases {
		t.Run(testCase.Name, func(t *testing.T) {
			status := qdrantv1alpha1.QdrantCollectionStatus{}
			decodeStrict(t, testCase.Status, &status)
			converted, err := translate.CollectionStatusFromAPI(status)
			if err != nil {
				compareJSON(t, testCase.Outputs, map[string]any{"error": true})
				return
			}
			compareJSON(t, testCase.Outputs, map[string]any{"status": statusOutput(t, converted)})
		})
	}
}

func TestCollectionSpecDefaultsAndNumberTypes(t *testing.T) {
	shards, replicas := int32(6), int32(2)
	collection, err := translate.CollectionSpecFromAPI("docs", "tenant-a", qdrantv1alpha1.QdrantCollectionSpec{
		ClusterRef:        qdrantv1alpha1.ClusterRef{Name: "db"},
		Vectors:           []qdrantv1alpha1.DenseVector{{Size: 4, Distance: "Dot"}},
		ShardNumber:       &shards,
		ReplicationFactor: &replicas,
		Optimizers:        &apiextensionsv1.JSON{Raw: []byte(`{"indexing_threshold": 10000, "deleted_threshold": 0.25}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if collection.ClusterRef.Namespace != "tenant-a" || collection.CollectionName != "docs" || collection.DeletionPolicy != domain.DeletionRetain {
		t.Errorf("defaults not applied: %+v", collection)
	}
	want := `{"optimizers_config":{"deleted_threshold":0.25,"indexing_threshold":10000},"replication_factor":2,"shard_number":6,"vectors":{"distance":"Dot","size":4}}`
	if got := marshalExact(t, collection.CreateBody()); got != want {
		t.Errorf("create body\nwant %s\ngot  %s", want, got)
	}
}

func TestCollectionStatusKeepsAbsentCountersAbsent(t *testing.T) {
	status := qdrantv1alpha1.QdrantCollectionStatus{Phase: "Ready", PointsCount: 5}
	converted, err := translate.CollectionStatusFromAPI(status)
	if err != nil {
		t.Fatal(err)
	}
	if converted.PointsCount == nil || *converted.PointsCount != 5 || converted.SegmentsCount != nil {
		t.Errorf("counters: %+v", converted)
	}
	if got := translate.CollectionStatusToAPI(converted); !reflect.DeepEqual(got, status) {
		t.Errorf("round trip: %+v", got)
	}
}

func TestCollectionStatusShardKeysFromJSONNumbers(t *testing.T) {
	status := domain.CollectionStatus{ShardKeys: []any{json.Number("7"), "tenant-a", 3}}
	converted := translate.CollectionStatusToAPI(status)
	got := []string{}
	for _, key := range converted.ShardKeys {
		got = append(got, key.String())
	}
	want := []string{"7", "tenant-a", "3"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("shard keys = %v, want %v", got, want)
	}
	if converted.ShardKeys[0].Type != converted.ShardKeys[2].Type {
		t.Fatalf("json.Number key became %v, want an int like 3", converted.ShardKeys[0].Type)
	}
}
