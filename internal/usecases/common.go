// Package usecases holds the operator's application logic. Use cases depend on ports only.
package usecases

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	qdrantv1alpha1 "github.com/4thel00z/qdrant-operator/api/v1alpha1"
	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/ports"
	"github.com/4thel00z/qdrant-operator/internal/translate"
)

const (
	RecentBackupsLimit = 10
	PresignedURLTTL    = time.Hour
	CrashLogLines      = 50
	HelmErrorChars     = 2000
)

// Callers retry on these instead of failing the resource.
var (
	ErrClusterNotFound = errors.New("QdrantCluster not found")
	ErrSourceNotReady  = errors.New("QdrantBackup has not completed yet")
	ErrClusterNotReady = errors.New("QdrantCluster does not answer /readyz yet")
)

// Retryable reports whether an error is one a controller should requeue rather than mark as failed.
func Retryable(err error) bool {
	return errors.Is(err, ErrClusterNotFound) ||
		errors.Is(err, ErrSourceNotReady) ||
		errors.Is(err, ErrClusterNotReady) ||
		errors.Is(err, ports.ErrSecretKeyNotFound)
}

type Clock func() time.Time

func SystemClock() time.Time {
	return time.Now().UTC()
}

// DecodeBody converts a custom resource body into its API type.
func DecodeBody[T any](body domain.JSON) (T, error) {
	var object T
	raw, err := json.Marshal(body)
	if err != nil {
		return object, fmt.Errorf("encode resource body: %w", err)
	}
	if err := json.Unmarshal(raw, &object); err != nil {
		return object, fmt.Errorf("decode resource body: %w", err)
	}
	return object, nil
}

// EncodeObject converts an API object into a JSON body, for PatchStatus and CreateCustomResource.
// Numbers decode as json.Number so large integer point IDs stay exact.
func EncodeObject(object any) (domain.JSON, error) {
	raw, err := json.Marshal(object)
	if err != nil {
		return nil, fmt.Errorf("encode object: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	body := domain.JSON{}
	if err := decoder.Decode(&body); err != nil {
		return nil, fmt.Errorf("decode object: %w", err)
	}
	return body, nil
}

func ClusterSpecFromBody(body domain.JSON) (domain.ClusterSpec, error) {
	cluster, err := DecodeBody[qdrantv1alpha1.QdrantCluster](body)
	if err != nil {
		return domain.ClusterSpec{}, err
	}
	return translate.ClusterSpecFromAPI(cluster.Name, cluster.Namespace, cluster.Spec)
}

func ReadCredentials(ctx context.Context, kubernetes ports.Kubernetes, ref domain.CredentialsSecretRef) (domain.S3Credentials, error) {
	accessKeyID, err := kubernetes.SecretValue(ctx, ref.AccessKeyRef())
	if err != nil {
		return domain.S3Credentials{}, err
	}
	secretAccessKey, err := kubernetes.SecretValue(ctx, ref.SecretKeyRef())
	if err != nil {
		return domain.S3Credentials{}, err
	}
	return domain.S3Credentials{AccessKeyID: accessKeyID, SecretAccessKey: secretAccessKey}, nil
}

type ResolveCluster struct {
	Kubernetes ports.Kubernetes
}

func (r ResolveCluster) Execute(ctx context.Context, ref domain.ClusterRef) (domain.ClusterConnection, error) {
	body, err := r.Kubernetes.CustomResource(ctx, ref.ResourceRef())
	if err != nil {
		return domain.ClusterConnection{}, err
	}
	if body == nil {
		return domain.ClusterConnection{}, fmt.Errorf("QdrantCluster %s/%s: %w", ref.Namespace, ref.Name, ErrClusterNotFound)
	}
	spec, err := ClusterSpecFromBody(body)
	if err != nil {
		return domain.ClusterConnection{}, err
	}
	return r.Connect(ctx, spec)
}

func (r ResolveCluster) Connect(ctx context.Context, spec domain.ClusterSpec) (domain.ClusterConnection, error) {
	apiKey, err := r.apiKey(ctx, spec)
	if err != nil {
		return domain.ClusterConnection{}, err
	}
	caCert, err := r.caCert(ctx, spec)
	if err != nil {
		return domain.ClusterConnection{}, err
	}
	nodes := []domain.QdrantNode{}
	for _, url := range spec.NodeURLs() {
		nodes = append(nodes, domain.QdrantNode{URL: url, APIKey: apiKey, CACert: caCert})
	}
	return domain.ClusterConnection{
		Service: domain.QdrantNode{URL: spec.ServiceURL(), APIKey: apiKey, CACert: caCert},
		Nodes:   nodes,
	}, nil
}

func (r ResolveCluster) apiKey(ctx context.Context, spec domain.ClusterSpec) (string, error) {
	ref := spec.APIKeyRef()
	if ref == nil {
		return "", nil
	}
	return r.Kubernetes.SecretValue(ctx, *ref)
}

func (r ResolveCluster) caCert(ctx context.Context, spec domain.ClusterSpec) (string, error) {
	ref := spec.TLS.CASecretRef(spec.Namespace)
	if ref == nil {
		return "", nil
	}
	value, err := r.Kubernetes.SecretValue(ctx, *ref)
	if errors.Is(err, ports.ErrSecretKeyNotFound) {
		return "", nil
	}
	return value, err
}
