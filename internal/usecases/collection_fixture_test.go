package usecases_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	qdrantv1alpha1 "github.com/4thel00z/qdrant-operator/api/v1alpha1"
	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/fakes"
	"github.com/4thel00z/qdrant-operator/internal/translate"
	"github.com/4thel00z/qdrant-operator/internal/usecases"
)

const collectionFixtureNS = "tenant-a"

var collectionFixtureNow = time.Date(2026, 9, 23, 2, 30, 0, 0, time.UTC)

type collectionFixture struct {
	kubernetes *fakes.Kubernetes
	qdrant     *fakes.Qdrant
}

func collectionClusterBody(name string, replicas int, spec domain.JSON) domain.JSON {
	body := domain.JSON{"version": "1.16.3", "replicas": replicas}
	for key, value := range spec {
		body[key] = value
	}
	return domain.JSON{
		"apiVersion": domain.APIVersion,
		"kind":       "QdrantCluster",
		"metadata":   domain.JSON{"name": name, "namespace": collectionFixtureNS},
		"spec":       body,
	}
}

func collectionKeyed() domain.JSON {
	return domain.JSON{"apiKey": domain.JSON{"secretRef": domain.JSON{"name": "api-keys", "key": "key"}}}
}

func collectionNodeURL(index int, cluster string) string {
	return fmt.Sprintf("http://qdrant-%s-%d.qdrant-%s-headless.%s.svc.cluster.local:6333", cluster, index, cluster, collectionFixtureNS)
}

func collectionServiceURL(cluster string) string {
	return fmt.Sprintf("http://qdrant-%s.%s.svc.cluster.local:6333", cluster, collectionFixtureNS)
}

// newCollectionFixture holds a cluster "db" with an API key secret and a service route to its first node.
func newCollectionFixture() *collectionFixture {
	kubernetes := fakes.NewKubernetes()
	kubernetes.Clock = collectionFixtureNow
	kubernetes.Secrets[collectionFixtureNS+"/api-keys"] = map[string]string{"key": "topsecret"}
	kubernetes.PutResource(collectionClusterBody("db", 2, collectionKeyed()))
	qdrant := fakes.NewQdrant()
	qdrant.Routes[collectionServiceURL("db")] = collectionNodeURL(0, "db")
	return &collectionFixture{kubernetes: kubernetes, qdrant: qdrant}
}

func (f *collectionFixture) routeTarget() {
	f.kubernetes.PutResource(collectionClusterBody("target", 2, nil))
	f.qdrant.Routes[collectionServiceURL("target")] = collectionNodeURL(0, "target")
}

func (f *collectionFixture) clock() usecases.Clock {
	tick := 0
	return func() time.Time {
		tick++
		return collectionFixtureNow.Add(time.Duration(tick) * time.Minute)
	}
}

func collectionMerge(base domain.JSON, overrides domain.JSON) domain.JSON {
	merged := domain.JSON{}
	for key, value := range base {
		merged[key] = value
	}
	for key, value := range overrides {
		merged[key] = value
	}
	return merged
}

func collectionStrictDecode[T any](t *testing.T, body domain.JSON) T {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var decoded T
	if err := decoder.Decode(&decoded); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return decoded
}

func collectionSpec(t *testing.T, name string, overrides domain.JSON) domain.CollectionSpec {
	t.Helper()
	body := collectionMerge(domain.JSON{
		"clusterRef": domain.JSON{"name": "db"},
		"vectors":    []any{domain.JSON{"size": 4, "distance": "Cosine"}},
	}, overrides)
	api := collectionStrictDecode[qdrantv1alpha1.QdrantCollectionSpec](t, body)
	spec, err := translate.CollectionSpecFromAPI(name, collectionFixtureNS, api)
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

func accessKeySpec(t *testing.T, overrides domain.JSON) domain.AccessKeySpec {
	t.Helper()
	body := domain.JSON{"clusterRef": domain.JSON{"name": "db"}}
	if _, scoped := overrides["collections"]; !scoped {
		body["access"] = "r"
	}
	api := collectionStrictDecode[qdrantv1alpha1.QdrantAccessKeySpec](t, collectionMerge(body, overrides))
	spec, err := translate.AccessKeySpecFromAPI("app-token", collectionFixtureNS, api)
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

func accessKeyOwner() domain.OwnerReference {
	return domain.OwnerReference{APIVersion: domain.APIVersion, Kind: "QdrantAccessKey", Name: "app-token", UID: "uid-app-token"}
}

func migrationSpec(t *testing.T, overrides domain.JSON) domain.MigrationSpec {
	t.Helper()
	body := collectionMerge(domain.JSON{
		"source":           domain.JSON{"clusterRef": domain.JSON{"name": "db"}},
		"targetClusterRef": domain.JSON{"name": "target"},
	}, overrides)
	api := collectionStrictDecode[qdrantv1alpha1.QdrantMigrationSpec](t, body)
	spec, err := translate.MigrationSpecFromAPI("move", collectionFixtureNS, api)
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

func migrationRef() domain.ResourceRef {
	return domain.ResourceRef{Kind: domain.Migrations, Name: "move", Namespace: collectionFixtureNS}
}
