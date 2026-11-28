package v1alpha1_test

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

var crdFiles = []string{
	"qdrantaccesskey-crd.yaml",
	"qdrantbackup-crd.yaml",
	"qdrantbackupschedule-crd.yaml",
	"qdrantcluster-crd.yaml",
	"qdrantcollection-crd.yaml",
	"qdrantmigration-crd.yaml",
	"qdrantrestore-crd.yaml",
}

var rootMetaProperties = []string{"apiVersion", "kind", "metadata"}

func TestGeneratedCRDsMatchBaseline(t *testing.T) {
	for _, name := range crdFiles {
		t.Run(name, func(t *testing.T) {
			baseline := comparable(t, loadYAML(t, filepath.Join("testdata", "baseline", name)))
			generated := comparable(t, loadYAML(t, filepath.Join(generatedDir(), name)))
			differences := diff("", baseline, generated)
			if len(differences) == 0 {
				return
			}
			t.Errorf("%s differs from the baseline:\n%s", name, strings.Join(differences, "\n"))
		})
	}
}

func generatedDir() string {
	dir := os.Getenv("GENERATED_CRD_DIR")
	if dir == "" {
		return filepath.Join("..", "..", "manifests", "crds")
	}
	return dir
}

func loadYAML(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	document := map[string]any{}
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return document
}

func comparable(t *testing.T, crd map[string]any) map[string]any {
	t.Helper()
	spec := mapAt(t, crd, "spec")
	versions, ok := spec["versions"].([]any)
	if !ok || len(versions) != 1 {
		t.Fatalf("expected exactly one version, got %v", spec["versions"])
	}
	version := versions[0].(map[string]any)
	schema := mapAt(t, mapAt(t, version, "schema"), "openAPIV3Schema")
	properties := mapAt(t, schema, "properties")
	for _, key := range rootMetaProperties {
		delete(properties, key)
	}
	return map[string]any{
		"name":    mapAt(t, crd, "metadata")["name"],
		"group":   spec["group"],
		"names":   spec["names"],
		"scope":   spec["scope"],
		"version": normalize(version).(map[string]any),
	}
}

func mapAt(t *testing.T, parent map[string]any, key string) map[string]any {
	t.Helper()
	child, ok := parent[key].(map[string]any)
	if !ok {
		t.Fatalf("missing object %q", key)
	}
	return child
}

func normalize(node any) any {
	switch value := node.(type) {
	case map[string]any:
		result := map[string]any{}
		for key, child := range value {
			result[key] = normalize(child)
		}
		if result["x-kubernetes-int-or-string"] == true {
			delete(result, "anyOf")
		}
		if format, ok := result["format"].(string); ok && (format == "int32" || format == "int64") {
			delete(result, "format")
		}
		if description, ok := result["description"].(string); ok {
			result["description"] = strings.Join(strings.Fields(description), " ")
		}
		if required, ok := result["required"].([]any); ok {
			result["required"] = sortedStrings(required)
		}
		return result
	case []any:
		result := make([]any, len(value))
		for index, child := range value {
			result[index] = normalize(child)
		}
		return result
	}
	return node
}

func sortedStrings(values []any) []any {
	result := slices.Clone(values)
	sort.Slice(result, func(i, j int) bool { return fmt.Sprint(result[i]) < fmt.Sprint(result[j]) })
	return result
}

func diff(path string, expected any, actual any) []string {
	expectedMap, expectedIsMap := expected.(map[string]any)
	actualMap, actualIsMap := actual.(map[string]any)
	if expectedIsMap && actualIsMap {
		return mapDiff(path, expectedMap, actualMap)
	}
	expectedList, expectedIsList := expected.([]any)
	actualList, actualIsList := actual.([]any)
	if expectedIsList && actualIsList && len(expectedList) == len(actualList) {
		return listDiff(path, expectedList, actualList)
	}
	if reflect.DeepEqual(expected, actual) {
		return nil
	}
	return []string{fmt.Sprintf("  %s: baseline %v, generated %v", path, expected, actual)}
}

func mapDiff(path string, expected map[string]any, actual map[string]any) []string {
	keys := map[string]bool{}
	for key := range expected {
		keys[key] = true
	}
	for key := range actual {
		keys[key] = true
	}
	ordered := make([]string, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)
	var differences []string
	for _, key := range ordered {
		differences = append(differences, diff(path+"."+key, expected[key], actual[key])...)
	}
	return differences
}

func listDiff(path string, expected []any, actual []any) []string {
	var differences []string
	for index := range expected {
		differences = append(differences, diff(fmt.Sprintf("%s[%d]", path, index), expected[index], actual[index])...)
	}
	return differences
}
