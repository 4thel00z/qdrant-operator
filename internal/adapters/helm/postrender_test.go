package helm

import (
	"bytes"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

const statefulSetDocument = `---
# Source: qdrant/templates/statefulset.yaml
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: qdrant-db
spec:
  replicas: 3
  updateStrategy:
    type: RollingUpdate
    rollingUpdate:
      partition: 0
  template:
    spec:
      containers:
        - name: qdrant
          image: "qdrant/qdrant:v1.16.3"
`

const serviceDocument = `---
# Source: qdrant/templates/service.yaml
apiVersion: v1
kind: Service
metadata:
  name: qdrant-db
spec:
  ports:
    - port: 6333   # kept as is
`

const configMapDocument = "---\nkind: ConfigMap\nmetadata:\n  name: qdrant-db\ndata:\n  a: |\n    x\n"

const strategyBlockText = "  updateStrategy:\n    type: RollingUpdate\n    rollingUpdate:\n      partition: 0\n"

func setPartition(t *testing.T, stream, name string, partition int) string {
	t.Helper()
	out, err := SetPartition(stream, name, partition)
	if err != nil {
		t.Fatalf("SetPartition: %v", err)
	}
	return out
}

func documents(t *testing.T, stream string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, part := range splitDocuments(stream) {
		var document map[string]any
		if err := yaml.Unmarshal([]byte(part), &document); err != nil {
			t.Fatalf("parse %q: %v", part, err)
		}
		if len(document) > 0 {
			out = append(out, document)
		}
	}
	return out
}

func strategyOf(t *testing.T, stream, name string) map[string]any {
	t.Helper()
	for _, document := range documents(t, stream) {
		if isStatefulSet(document, name) {
			spec, _ := document["spec"].(map[string]any)
			updateStrategy, _ := spec["updateStrategy"].(map[string]any)
			return updateStrategy
		}
	}
	t.Fatalf("no StatefulSet %q in %q", name, stream)
	return nil
}

func partitionOf(t *testing.T, stream, name string) float64 {
	t.Helper()
	updateStrategy := strategyOf(t, stream, name)
	if updateStrategy["type"] != "RollingUpdate" {
		t.Fatalf("type = %v, want RollingUpdate", updateStrategy["type"])
	}
	rolling, _ := updateStrategy["rollingUpdate"].(map[string]any)
	partition, _ := rolling["partition"].(float64)
	return partition
}

func TestMatchingStatefulSetGetsThePartition(t *testing.T) {
	out := setPartition(t, serviceDocument+statefulSetDocument, "qdrant-db", 3)
	if got := partitionOf(t, out, "qdrant-db"); got != 3 {
		t.Fatalf("partition = %v, want 3", got)
	}
}

func TestOnlyThePartitionLineChanges(t *testing.T) {
	out := setPartition(t, statefulSetDocument, "qdrant-db", 3)
	want := strings.Replace(statefulSetDocument, "partition: 0", "partition: 3", 1)
	if out != want {
		t.Fatalf("got\n%s\nwant\n%s", out, want)
	}
}

func TestMissingUpdateStrategyIsAdded(t *testing.T) {
	stream := strings.Replace(statefulSetDocument, strategyBlockText, "", 1)
	out := setPartition(t, stream, "qdrant-db", 2)
	if got := partitionOf(t, out, "qdrant-db"); got != 2 {
		t.Fatalf("partition = %v, want 2", got)
	}
}

func TestFlowStyleUpdateStrategyIsReplaced(t *testing.T) {
	stream := strings.Replace(statefulSetDocument, strategyBlockText, "  updateStrategy: {type: OnDelete}\n", 1)
	out := setPartition(t, stream, "qdrant-db", 2)
	if got := partitionOf(t, out, "qdrant-db"); got != 2 {
		t.Fatalf("partition = %v, want 2", got)
	}
	spec, _ := documents(t, out)[0]["spec"].(map[string]any)
	if spec["replicas"] != float64(3) {
		t.Fatalf("replicas = %v, want 3", spec["replicas"])
	}
}

func TestOtherKindsAreReturnedByteForByte(t *testing.T) {
	stream := serviceDocument + configMapDocument + statefulSetDocument
	out := setPartition(t, stream, "qdrant-db", 3)
	if !strings.HasPrefix(out, serviceDocument+configMapDocument) {
		t.Fatalf("leading documents changed:\n%s", out)
	}
}

func TestStatefulSetWithAnotherNameIsUntouched(t *testing.T) {
	other := strings.Replace(statefulSetDocument, "name: qdrant-db", "name: qdrant-other", 1)
	stream := serviceDocument + other
	if out := setPartition(t, stream, "qdrant-db", 3); out != stream {
		t.Fatalf("stream changed:\n%s", out)
	}
}

func TestStreamWithoutStatefulSetIsUnchanged(t *testing.T) {
	stream := serviceDocument + configMapDocument
	if out := setPartition(t, stream, "qdrant-db", 3); out != stream {
		t.Fatalf("stream changed:\n%s", out)
	}
}

func TestEmptyStreamIsUnchanged(t *testing.T) {
	if out := setPartition(t, "", "qdrant-db", 3); out != "" {
		t.Fatalf("out = %q, want empty", out)
	}
}

func TestStreamWithoutLeadingSeparatorIsHandled(t *testing.T) {
	stream := strings.TrimPrefix(statefulSetDocument, "---\n")
	out := setPartition(t, stream, "qdrant-db", 3)
	if got := partitionOf(t, out, "qdrant-db"); got != 3 {
		t.Fatalf("partition = %v, want 3", got)
	}
}

func TestPartitionPinnerRunsAsHelmPostRenderer(t *testing.T) {
	stream := serviceDocument + statefulSetDocument
	out, err := PartitionPinner{StatefulSet: "qdrant-db", Partition: 3}.Run(bytes.NewBufferString(stream))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if want := setPartition(t, stream, "qdrant-db", 3); out.String() != want {
		t.Fatalf("got\n%s\nwant\n%s", out.String(), want)
	}
}
