package usecases_test

import (
	"context"
	"errors"
	"testing"

	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/fakes"
	"github.com/4thel00z/qdrant-operator/internal/usecases"
)

func clusterBody(spec domain.JSON) domain.JSON {
	return domain.JSON{
		"apiVersion": domain.APIVersion,
		"kind":       "QdrantCluster",
		"metadata":   domain.JSON{"name": "demo", "namespace": "search"},
		"spec":       spec,
	}
}

func TestResolveClusterReadsTheAPIKeyAndCA(t *testing.T) {
	ctx := context.Background()
	kubernetes := fakes.NewKubernetes()
	kubernetes.PutResource(clusterBody(domain.JSON{
		"version":  "v1.15.0",
		"replicas": 2,
		"apiKey":   domain.JSON{"secretRef": domain.JSON{"name": "keys", "key": "admin"}},
		"tls":      domain.JSON{"enabled": true, "secretRef": domain.JSON{"name": "tls"}},
	}))
	kubernetes.Secrets["search/keys"] = map[string]string{"admin": "secret"}
	kubernetes.Secrets["search/tls"] = map[string]string{"ca.crt": "PEM"}
	connection, err := usecases.ResolveCluster{Kubernetes: kubernetes}.Execute(ctx, domain.ClusterRef{Name: "demo", Namespace: "search"})
	if err != nil {
		t.Fatal(err)
	}
	want := domain.QdrantNode{URL: "https://qdrant-demo.search.svc.cluster.local:6333", APIKey: "secret", CACert: "PEM"}
	if connection.Service != want || len(connection.Nodes) != 2 {
		t.Errorf("connection: %+v", connection)
	}
}

func TestResolveClusterWithoutCAKeyConnectsWithoutCA(t *testing.T) {
	ctx := context.Background()
	kubernetes := fakes.NewKubernetes()
	kubernetes.PutResource(clusterBody(domain.JSON{"version": "v1.15.0", "tls": domain.JSON{"enabled": true, "secretRef": domain.JSON{"name": "tls"}}}))
	kubernetes.Secrets["search/tls"] = map[string]string{"tls.crt": "PEM"}
	connection, err := usecases.ResolveCluster{Kubernetes: kubernetes}.Execute(ctx, domain.ClusterRef{Name: "demo", Namespace: "search"})
	if err != nil || connection.Service.CACert != "" {
		t.Fatalf("connection %+v, err %v", connection, err)
	}
}

func TestResolveClusterReportsAMissingClusterAsRetryable(t *testing.T) {
	_, err := usecases.ResolveCluster{Kubernetes: fakes.NewKubernetes()}.Execute(context.Background(), domain.ClusterRef{Name: "gone", Namespace: "search"})
	if !errors.Is(err, usecases.ErrClusterNotFound) || !usecases.Retryable(err) {
		t.Fatalf("want retryable ErrClusterNotFound, got %v", err)
	}
}
