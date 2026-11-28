package helm

import (
	"context"
	"testing"
	"time"

	"k8s.io/client-go/rest"

	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/ports"
	"github.com/4thel00z/qdrant-operator/internal/settings"
)

func TestNewDerivesTheChartSource(t *testing.T) {
	adapter := New(settings.Helm{
		Chart:    "qdrant",
		Repo:     "oci://registry.example.com/charts",
		Timeout:  time.Minute,
		CacheDir: "/tmp/helm",
	}, &rest.Config{})

	if adapter.source.Reference != "oci://registry.example.com/charts/qdrant" || adapter.source.RepoURL != "" {
		t.Fatalf("source = %+v", adapter.source)
	}
	if adapter.timeout != time.Minute || adapter.cache.dir != "/tmp/helm" {
		t.Fatalf("adapter = %+v", adapter)
	}
}

func TestNormalizeValuesProducesJSONTypes(t *testing.T) {
	values := domain.JSON{"replicas": 3, "resources": domain.JSON{"limits": map[string]string{"cpu": "1"}}}

	got, err := normalizeValues(values)
	if err != nil {
		t.Fatal(err)
	}
	if got["replicas"] != float64(3) {
		t.Fatalf("replicas = %#v, want float64(3)", got["replicas"])
	}
	limits, _ := got["resources"].(map[string]any)["limits"].(map[string]any)
	if limits["cpu"] != "1" {
		t.Fatalf("limits = %#v", limits)
	}
}

func TestNormalizeValuesRejectsUnserializableValues(t *testing.T) {
	if _, err := normalizeValues(domain.JSON{"bad": make(chan int)}); err == nil {
		t.Fatal("want error")
	}
}

func TestNormalizeValuesOfNilIsEmpty(t *testing.T) {
	got, err := normalizeValues(nil)
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("got %#v, err %v", got, err)
	}
}

func TestRESTGetterServesTheRESTConfig(t *testing.T) {
	getter := newRESTGetter(&rest.Config{Host: "https://cluster.example"}, "qdrant")

	config, err := getter.ToRESTConfig()
	if err != nil || config.Host != "https://cluster.example" {
		t.Fatalf("config = %+v, err %v", config, err)
	}
	namespace, _, err := getter.ToRawKubeConfigLoader().Namespace()
	if err != nil || namespace != "qdrant" {
		t.Fatalf("namespace = %q, err %v", namespace, err)
	}
}

func TestApplyRejectsUnserializableValuesBeforeTouchingTheCluster(t *testing.T) {
	adapter := New(settings.Helm{Chart: "qdrant", Repo: "https://example.com", Timeout: time.Second, CacheDir: t.TempDir()}, &rest.Config{})

	err := adapter.Apply(context.Background(), ports.ReleaseRequest{
		Name:      "db",
		Namespace: "qdrant",
		Values:    domain.JSON{"bad": make(chan int)},
	})
	if err == nil {
		t.Fatal("want error")
	}
}
