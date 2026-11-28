package translate_test

import (
	"encoding/json"
	"reflect"
	"testing"

	qdrantv1alpha1 "github.com/4thel00z/qdrant-operator/api/v1alpha1"
	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/translate"
)

type clusterStatusGolden struct {
	Cases []struct {
		Name   string          `json:"name"`
		Status json.RawMessage `json:"status"`
		Out    map[string]any  `json:"out"`
	} `json:"cases"`
}

func TestClusterStatusRoundTripMatchesPythonGolden(t *testing.T) {
	golden := clusterStatusGolden{}
	readJSON(t, "testdata/cluster_status_golden.json", &golden)
	if len(golden.Cases) == 0 {
		t.Fatal("golden has no cases")
	}
	for _, testCase := range golden.Cases {
		t.Run(testCase.Name, func(t *testing.T) {
			status := qdrantv1alpha1.QdrantClusterStatus{}
			decodeStrict(t, testCase.Status, &status)
			parsed, err := translate.ClusterStatusFromAPI(status)
			if err != nil {
				t.Fatalf("from api: %v", err)
			}
			rendered, err := json.Marshal(translate.ClusterStatusToAPI(parsed))
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			got := map[string]any{}
			if err := json.Unmarshal(rendered, &got); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if !reflect.DeepEqual(got, testCase.Out) {
				t.Errorf("got %v want %v", got, testCase.Out)
			}
		})
	}
}

func TestClusterStatusFromAPIDefaultsToPending(t *testing.T) {
	status, err := translate.ClusterStatusFromAPI(qdrantv1alpha1.QdrantClusterStatus{})
	if err != nil {
		t.Fatal(err)
	}
	if status.Phase != domain.PhasePending {
		t.Fatalf("phase %q", status.Phase)
	}
}

func TestClusterStatusFromAPIRejectsBadConditionTime(t *testing.T) {
	status := qdrantv1alpha1.QdrantClusterStatus{
		Conditions: []qdrantv1alpha1.Condition{{Type: "Ready", Status: qdrantv1alpha1.ConditionTrue, LastTransitionTime: "yesterday"}},
	}
	if _, err := translate.ClusterStatusFromAPI(status); err == nil {
		t.Fatal("want error")
	}
}
