package translate_test

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"testing"

	qdrantv1alpha1 "github.com/4thel00z/qdrant-operator/api/v1alpha1"
	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/translate"
)

type clusterGolden struct {
	ClaimNames []string            `json:"claim_names"`
	Cases      []clusterGoldenCase `json:"cases"`
}

type clusterGoldenCase struct {
	Name    string          `json:"name"`
	Spec    json.RawMessage `json:"spec"`
	Outputs map[string]any  `json:"outputs"`
}

func TestClusterOutputsMatchPythonGolden(t *testing.T) {
	golden := clusterGolden{}
	readJSON(t, "testdata/cluster_values_golden.json", &golden)
	for _, testCase := range golden.Cases {
		t.Run(testCase.Name, func(t *testing.T) {
			spec := qdrantv1alpha1.QdrantClusterSpec{}
			decodeStrict(t, testCase.Spec, &spec)
			cluster, err := translate.ClusterSpecFromAPI("demo", "search", spec)
			if err != nil {
				t.Fatalf("translate: %v", err)
			}
			compareJSON(t, testCase.Outputs, clusterOutputs(cluster, golden.ClaimNames))
		})
	}
}

func clusterOutputs(cluster domain.ClusterSpec, claimNames []string) map[string]any {
	var apiKeyRef any
	if ref := cluster.APIKeyRef(); ref != nil {
		apiKeyRef = map[string]any{"name": ref.Name, "key": ref.Key, "namespace": ref.Namespace}
	}
	return map[string]any{
		"values":             cluster.HelmValues(),
		"claim_sizes":        cluster.ClaimSizes(),
		"departed_claims":    cluster.DepartedClaims(claimNames),
		"node_urls":          cluster.NodeURLs(),
		"service_url":        cluster.ServiceURL(),
		"pod_label_selector": cluster.PodLabelSelector(),
		"volume_selector":    cluster.VolumeSelector(),
		"generated_keys":     cluster.GeneratedKeys(),
		"api_key_ref":        apiKeyRef,
	}
}

func readJSON(t *testing.T, path string, target any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := json.Unmarshal(raw, target); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
}

// decodeStrict rejects fields the API type does not declare, so a misspelled json tag fails the test.
func decodeStrict(t *testing.T, raw json.RawMessage, target any) {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		t.Fatalf("decode spec: %v", err)
	}
}

// compareJSON compares after a JSON round trip, so Go ints and Python numbers meet as float64.
func compareJSON(t *testing.T, want map[string]any, got map[string]any) {
	t.Helper()
	rendered, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	normalized := map[string]any{}
	if err := json.Unmarshal(rendered, &normalized); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for key, value := range want {
		if reflect.DeepEqual(normalized[key], value) {
			continue
		}
		wantJSON, _ := json.MarshalIndent(value, "", "  ")
		gotJSON, _ := json.MarshalIndent(normalized[key], "", "  ")
		t.Errorf("%s differs\nwant:\n%s\ngot:\n%s", key, wantJSON, gotJSON)
	}
	for key := range normalized {
		if _, ok := want[key]; !ok {
			t.Errorf("%s has no golden value", key)
		}
	}
}
