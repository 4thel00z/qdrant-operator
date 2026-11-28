package chart_test

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func yamlNames(t *testing.T, directory string) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(directory, "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(paths))
	for _, path := range paths {
		names = append(names, filepath.Base(path))
	}
	slices.Sort(names)
	return names
}

func TestChartCRDsMatchManifests(t *testing.T) {
	manifests := filepath.Join("..", "..", "manifests", "crds")
	chart := filepath.Join("..", "..", "charts", "qdrant-operator", "crds")
	names := yamlNames(t, manifests)

	if len(names) == 0 {
		t.Fatal("manifests/crds holds no CRDs")
	}
	if got := yamlNames(t, chart); !slices.Equal(names, got) {
		t.Fatalf("chart CRDs %v differ from manifests CRDs %v", got, names)
	}
	for _, name := range names {
		want, err := os.ReadFile(filepath.Join(manifests, name))
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(chart, name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(want, got) {
			t.Errorf("%s differs between manifests/crds and the chart; run make crds-sync", name)
		}
	}
}
