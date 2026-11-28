package usecases_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	qdrantv1alpha1 "github.com/4thel00z/qdrant-operator/api/v1alpha1"
	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/fakes"
	"github.com/4thel00z/qdrant-operator/internal/translate"
	"github.com/4thel00z/qdrant-operator/internal/usecases"
)

const backupNamespace = "tenant-a"

var backupTestNow = time.Date(2026, 9, 23, 2, 30, 0, 0, time.UTC)

func backupTestClock() time.Time {
	return backupTestNow
}

func backupS3() domain.JSON {
	return domain.JSON{"bucket": "bucket", "prefix": "backups", "credentialsSecretRef": domain.JSON{"name": "s3-creds"}}
}

func backupClusterBody(name string, replicas int, spec domain.JSON) domain.JSON {
	body := domain.JSON{"version": "1.16.3", "replicas": replicas}
	for key, value := range spec {
		body[key] = value
	}
	return domain.JSON{
		"apiVersion": domain.APIVersion,
		"kind":       "QdrantCluster",
		"metadata":   domain.JSON{"name": name, "namespace": backupNamespace},
		"spec":       body,
	}
}

func backupNodeURL(index int, cluster string) string {
	return fmt.Sprintf("http://qdrant-%s-%d.qdrant-%s-headless.%s.svc.cluster.local:6333", cluster, index, cluster, backupNamespace)
}

func backupServiceURL(cluster string) string {
	return fmt.Sprintf("http://qdrant-%s.%s.svc.cluster.local:6333", cluster, backupNamespace)
}

type backupWorld struct {
	kubernetes *fakes.Kubernetes
	qdrant     *fakes.Qdrant
	storage    *fakes.Storage
}

func newBackupWorld() backupWorld {
	kubernetes := fakes.NewKubernetes()
	kubernetes.Clock = backupTestNow
	kubernetes.Secrets[backupNamespace+"/s3-creds"] = map[string]string{"AWS_ACCESS_KEY_ID": "AK", "AWS_SECRET_ACCESS_KEY": "SK"}
	kubernetes.Secrets[backupNamespace+"/api-keys"] = map[string]string{"key": "topsecret"}
	kubernetes.PutResource(backupClusterBody("db", 2, domain.JSON{"apiKey": domain.JSON{"secretRef": domain.JSON{"name": "api-keys", "key": "key"}}}))
	qdrant := fakes.NewQdrant()
	for index := range 2 {
		qdrant.AddCollection(backupNodeURL(index, "db"), "docs", 10, nil)
		qdrant.AddCollection(backupNodeURL(index, "db"), "images", 5, nil)
	}
	qdrant.Routes[backupServiceURL("db")] = backupNodeURL(0, "db")
	return backupWorld{kubernetes: kubernetes, qdrant: qdrant, storage: fakes.NewStorage()}
}

func (w backupWorld) executeBackup() usecases.ExecuteBackup {
	return usecases.ExecuteBackup{Qdrant: w.qdrant, Storage: w.storage, Kubernetes: w.kubernetes, Clock: backupTestClock}
}

func (w backupWorld) executeRestore() usecases.ExecuteRestore {
	return usecases.ExecuteRestore{Qdrant: w.qdrant, Storage: w.storage, Kubernetes: w.kubernetes, Clock: backupTestClock}
}

func backupSpecOf(t *testing.T, name string, spec domain.JSON) domain.BackupSpec {
	t.Helper()
	merged := domain.JSON{"clusterRef": domain.JSON{"name": "db"}, "storage": domain.JSON{"s3": backupS3()}}
	for key, value := range spec {
		merged[key] = value
	}
	api, err := usecases.DecodeBody[qdrantv1alpha1.QdrantBackupSpec](merged)
	if err != nil {
		t.Fatal(err)
	}
	backup, err := translate.BackupSpecFromAPI(name, backupNamespace, api)
	if err != nil {
		t.Fatal(err)
	}
	return backup
}

func backupRef(name string) domain.ResourceRef {
	return domain.ResourceRef{Kind: domain.Backups, Name: name, Namespace: backupNamespace}
}

func backupStatusBody(t *testing.T, status domain.BackupStatus) domain.JSON {
	t.Helper()
	body, err := usecases.EncodeObject(translate.BackupStatusToAPI(status))
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func backupRun(t *testing.T, spec domain.BackupSpec, world backupWorld) domain.BackupStatus {
	t.Helper()
	status, err := world.executeBackup().Execute(context.Background(), spec, backupRef(spec.Name), nil)
	if err != nil {
		t.Fatal(err)
	}
	return status
}

func backupResource(name string, spec domain.JSON, status domain.JSON) domain.JSON {
	return domain.JSON{
		"apiVersion": domain.APIVersion,
		"kind":       "QdrantBackup",
		"metadata":   domain.JSON{"name": name, "namespace": backupNamespace},
		"spec":       spec,
		"status":     status,
	}
}
