package helm

import (
	"bytes"
	"fmt"
	"reflect"
	"regexp"
	"strings"

	"sigs.k8s.io/yaml"
)

var (
	strategyBlock    = regexp.MustCompile(`(?m)^  updateStrategy:.*\n(?:[ ]{3,}.*\n)*`)
	specLine         = regexp.MustCompile(`(?m)^spec:[ \t]*\n`)
	commentOrDivider = regexp.MustCompile(`^\s*(#.*|---\s*)?$`)
	documentDivider  = regexp.MustCompile(`^---[ \t]*$`)
)

// PartitionPinner is a Helm post-renderer that sets the updateStrategy of one
// StatefulSet to RollingUpdate at a fixed partition. Every other document in
// the rendered stream is returned byte for byte.
type PartitionPinner struct {
	StatefulSet string
	Partition   int
}

func (p PartitionPinner) Run(rendered *bytes.Buffer) (*bytes.Buffer, error) {
	out, err := SetPartition(rendered.String(), p.StatefulSet, p.Partition)
	if err != nil {
		return nil, err
	}
	return bytes.NewBufferString(out), nil
}

// SetPartition rewrites the named StatefulSet's updateStrategy to RollingUpdate
// at partition.
func SetPartition(stream, statefulSet string, partition int) (string, error) {
	var out strings.Builder
	for _, part := range splitDocuments(stream) {
		if !isStatefulSet(parse(part), statefulSet) {
			out.WriteString(part)
			continue
		}
		pinned, err := pinPartition(part, partition)
		if err != nil {
			return "", fmt.Errorf("pin partition of StatefulSet %q: %w", statefulSet, err)
		}
		out.WriteString(pinned)
	}
	return out.String(), nil
}

func splitDocuments(stream string) []string {
	var parts []string
	var current strings.Builder
	for _, line := range strings.SplitAfter(stream, "\n") {
		if documentDivider.MatchString(strings.TrimSuffix(line, "\n")) && current.Len() > 0 {
			parts = append(parts, current.String())
			current.Reset()
		}
		current.WriteString(line)
	}
	if current.Len() > 0 {
		parts = append(parts, current.String())
	}
	return parts
}

func parse(text string) map[string]any {
	var document map[string]any
	if err := yaml.Unmarshal([]byte(text), &document); err != nil {
		return nil
	}
	return document
}

func isStatefulSet(document map[string]any, name string) bool {
	if document["kind"] != "StatefulSet" {
		return false
	}
	metadata, ok := document["metadata"].(map[string]any)
	if !ok {
		return false
	}
	return metadata["name"] == name
}

func strategy(partition int) map[string]any {
	return map[string]any{
		"type":          "RollingUpdate",
		"rollingUpdate": map[string]any{"partition": float64(partition)},
	}
}

func patchText(text string, partition int) string {
	block := fmt.Sprintf("  updateStrategy:\n    type: RollingUpdate\n    rollingUpdate:\n      partition: %d\n", partition)
	if found := strategyBlock.FindStringIndex(text); found != nil {
		return text[:found[0]] + block + text[found[1]:]
	}
	found := specLine.FindStringIndex(text)
	if found == nil {
		return text
	}
	return text[:found[1]] + block + text[found[1]:]
}

func headLength(text string) int {
	kept := 0
	for _, line := range strings.SplitAfter(text, "\n") {
		if !commentOrDivider.MatchString(strings.TrimSuffix(line, "\n")) {
			break
		}
		kept += len(line)
	}
	return kept
}

func dump(text string, partition int) (string, error) {
	document := parse(text)
	spec, ok := document["spec"].(map[string]any)
	if !ok {
		spec = map[string]any{}
	}
	spec["updateStrategy"] = strategy(partition)
	document["spec"] = spec
	body, err := yaml.Marshal(document)
	if err != nil {
		return "", err
	}
	return text[:headLength(text)] + string(body), nil
}

func pinPartition(text string, partition int) (string, error) {
	patched := patchText(text, partition)
	spec, _ := parse(patched)["spec"].(map[string]any)
	if reflect.DeepEqual(spec["updateStrategy"], strategy(partition)) {
		return patched, nil
	}
	return dump(text, partition)
}
