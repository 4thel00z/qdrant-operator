package translate_test

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	qdrantv1alpha1 "github.com/4thel00z/qdrant-operator/api/v1alpha1"
	"github.com/4thel00z/qdrant-operator/internal/translate"
)

type goldenCase struct {
	Name   string          `json:"name"`
	Spec   json.RawMessage `json:"spec"`
	Values any             `json:"values"`
}

func TestClusterHelmValuesMatchPythonGolden(t *testing.T) {
	raw, err := os.ReadFile("testdata/cluster_values_golden.json")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	cases := []goldenCase{}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatalf("decode golden: %v", err)
	}
	for _, golden := range cases {
		t.Run(golden.Name, func(t *testing.T) {
			spec := qdrantv1alpha1.QdrantClusterSpec{}
			if err := json.Unmarshal(golden.Spec, &spec); err != nil {
				t.Fatalf("decode spec: %v", err)
			}
			cluster, err := translate.ClusterSpecFromAPI("demo", "search", spec)
			if err != nil {
				t.Fatalf("translate: %v", err)
			}
			rendered, err := json.Marshal(cluster.HelmValues())
			if err != nil {
				t.Fatalf("encode values: %v", err)
			}
			var got any
			if err := json.Unmarshal(rendered, &got); err != nil {
				t.Fatalf("decode values: %v", err)
			}
			if reflect.DeepEqual(got, golden.Values) {
				return
			}
			want, _ := json.MarshalIndent(golden.Values, "", "  ")
			have, _ := json.MarshalIndent(got, "", "  ")
			t.Errorf("values differ\nwant:\n%s\ngot:\n%s", want, have)
		})
	}
}
