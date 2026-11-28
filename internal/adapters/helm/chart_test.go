package helm

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/chart/common"
	"helm.sh/helm/v4/pkg/chart/loader"
	chartv2 "helm.sh/helm/v4/pkg/chart/v2"
	chartutil "helm.sh/helm/v4/pkg/chart/v2/util"
	"helm.sh/helm/v4/pkg/cli"
)

func TestNewChartSource(t *testing.T) {
	cases := []struct {
		name  string
		chart string
		repo  string
		want  chartSource
	}{
		{"http repository", "qdrant", "https://qdrant.github.io/qdrant-helm", chartSource{Reference: "qdrant", RepoURL: "https://qdrant.github.io/qdrant-helm"}},
		{"oci repository", "qdrant", "oci://registry.example.com/charts", chartSource{Reference: "oci://registry.example.com/charts/qdrant"}},
		{"oci repository trailing slash", "qdrant", "oci://registry.example.com/charts/", chartSource{Reference: "oci://registry.example.com/charts/qdrant"}},
		{"local chart", "./chart", "", chartSource{Reference: "./chart"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := newChartSource(tc.chart, tc.repo); got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestCacheable(t *testing.T) {
	remote := chartSource{Reference: "qdrant", RepoURL: "https://example.com"}
	cases := []struct {
		source  chartSource
		version string
		want    bool
	}{
		{remote, "1.16.3", true},
		{remote, "v1.16.3-rc.1+build.5", true},
		{remote, "", false},
		{remote, ">=1.0.0", false},
		{remote, "1.x", false},
		{chartSource{Reference: "./chart"}, "1.0.0", false},
		{chartSource{Reference: "oci://example.com/qdrant"}, "1.0.0", true},
	}
	for _, tc := range cases {
		if got := cacheable(tc.source, tc.version); got != tc.want {
			t.Errorf("cacheable(%+v, %q) = %v, want %v", tc.source, tc.version, got, tc.want)
		}
	}
}

func TestFetchDownloadsOncePerVersion(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "downloaded.tgz")
	if err := os.WriteFile(archive, []byte("archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	locate := func(string, *cli.EnvSettings) (string, error) {
		calls++
		return archive, nil
	}
	cache := chartCache{dir: filepath.Join(dir, "cache")}
	source := chartSource{Reference: "qdrant", RepoURL: "https://example.com"}

	first, err := cache.fetch(source, "1.2.3", nil, locate)
	if err != nil {
		t.Fatal(err)
	}
	second, err := cache.fetch(source, "1.2.3", nil, locate)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cache.fetch(source, "1.2.4", nil, locate); err != nil {
		t.Fatal(err)
	}

	if calls != 2 {
		t.Fatalf("locate called %d times, want 2 (one per version)", calls)
	}
	if first != second {
		t.Fatalf("paths differ: %s vs %s", first, second)
	}
	content, err := os.ReadFile(first)
	if err != nil || string(content) != "archive" {
		t.Fatalf("cached content = %q, err = %v", content, err)
	}
}

func TestFetchDoesNotCacheVersionRanges(t *testing.T) {
	calls := 0
	locate := func(string, *cli.EnvSettings) (string, error) {
		calls++
		return "located", nil
	}
	cache := chartCache{dir: t.TempDir()}
	source := chartSource{Reference: "qdrant", RepoURL: "https://example.com"}

	for range 2 {
		if _, err := cache.fetch(source, ">=1.0.0", nil, locate); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 {
		t.Fatalf("locate called %d times, want 2", calls)
	}
}

func TestFetchReportsLocateError(t *testing.T) {
	locate := func(string, *cli.EnvSettings) (string, error) {
		return "", fmt.Errorf("chart not found")
	}
	cache := chartCache{dir: t.TempDir()}
	source := chartSource{Reference: "qdrant", RepoURL: "https://example.com"}
	if _, err := cache.fetch(source, "1.0.0", nil, locate); err == nil {
		t.Fatal("want error")
	}
}

func packagedChart(t *testing.T, version string) []byte {
	t.Helper()
	chart := &chartv2.Chart{
		Metadata: &chartv2.Metadata{APIVersion: "v2", Name: "tiny", Version: version},
		Templates: []*common.File{
			{Name: "templates/configmap.yaml", Data: []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: tiny\n")},
		},
	}
	path, err := chartutil.Save(chart, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func TestFetchFromHTTPRepositoryReusesTheCache(t *testing.T) {
	archive := packagedChart(t, "1.0.0")
	downloads := 0
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.yaml":
			fmt.Fprintf(w, "apiVersion: v1\nentries:\n  tiny:\n    - apiVersion: v2\n      name: tiny\n      version: 1.0.0\n      urls:\n        - %s/tiny-1.0.0.tgz\ngenerated: \"2026-01-01T00:00:00Z\"\n", server.URL)
		case "/tiny-1.0.0.tgz":
			downloads++
			w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	adapter := &Adapter{
		source: newChartSource("tiny", server.URL),
		cache:  chartCache{dir: t.TempDir()},
	}
	upgrade := action.NewUpgrade(&action.Configuration{})
	upgrade.RepoURL = adapter.source.RepoURL
	upgrade.Version = "1.0.0"

	for range 2 {
		chart, err := adapter.loadChart(upgrade.LocateChart, "1.0.0")
		if err != nil {
			t.Fatalf("loadChart: %v", err)
		}
		if chart == nil {
			t.Fatal("nil chart")
		}
	}
	if downloads != 1 {
		t.Fatalf("chart downloaded %d times, want 1", downloads)
	}
	if _, err := loader.Load(adapter.cache.archivePath(adapter.source, "1.0.0")); err != nil {
		t.Fatalf("cached archive does not load: %v", err)
	}
}

func TestLoadChartFromLocalDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tiny")
	chart := &chartv2.Chart{
		Metadata:  &chartv2.Metadata{APIVersion: "v2", Name: "tiny", Version: "0.1.0"},
		Templates: []*common.File{{Name: "templates/a.yaml", Data: []byte("kind: ConfigMap\n")}},
	}
	if err := chartutil.SaveDir(chart, filepath.Dir(dir)); err != nil {
		t.Fatal(err)
	}
	adapter := &Adapter{source: newChartSource(dir, ""), cache: chartCache{dir: t.TempDir()}}
	upgrade := action.NewUpgrade(&action.Configuration{})

	if _, err := adapter.loadChart(upgrade.LocateChart, "0.1.0"); err != nil {
		t.Fatalf("loadChart: %v", err)
	}
}
