package usecases

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"math/big"

	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/ports"
)

const (
	apiKeyLength   = 32
	apiKeyAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
)

// GenerateAPIKeys creates the cluster's generated API keys before helm renders them.
// A key the chart already generated (release installed with apiKey.autoGenerate) is carried over,
// so switching to the operator-generated Secret changes neither the key nor the pods.
// Random is crypto/rand when nil.
type GenerateAPIKeys struct {
	Kubernetes ports.Kubernetes
	Random     io.Reader
}

func (g GenerateAPIKeys) Execute(ctx context.Context, spec domain.ClusterSpec) error {
	wanted := spec.GeneratedKeys()
	if len(wanted) == 0 {
		return nil
	}
	current := map[string]string{}
	missing := false
	for _, key := range wanted {
		value, err := g.read(ctx, spec.GeneratedKeyRef(key))
		if err != nil {
			return err
		}
		current[key] = value
		missing = missing || value == ""
	}
	if !missing {
		return nil
	}
	body, err := g.Kubernetes.CustomResource(ctx, domain.ResourceRef{Kind: domain.Clusters, Name: spec.Name, Namespace: spec.Namespace})
	if err != nil {
		return fmt.Errorf("read QdrantCluster: %w", err)
	}
	if body == nil {
		return fmt.Errorf("QdrantCluster %s/%s: %w", spec.Namespace, spec.Name, ErrClusterNotFound)
	}
	metadata, _ := body["metadata"].(domain.JSON)
	uid, _ := metadata["uid"].(string)
	if uid == "" {
		return fmt.Errorf("QdrantCluster %s/%s has no uid", spec.Namespace, spec.Name)
	}
	values := map[string]string{}
	for _, key := range wanted {
		value, err := g.keyValue(ctx, spec, key, current[key])
		if err != nil {
			return err
		}
		values[key] = value
	}
	owner := domain.OwnerReference{APIVersion: domain.APIVersion, Kind: domain.Clusters.Kind, Name: spec.Name, UID: uid}
	if err := g.Kubernetes.ApplySecret(ctx, spec.GeneratedKeysSecret(), spec.Namespace, values, owner); err != nil {
		return fmt.Errorf("apply generated keys secret: %w", err)
	}
	return nil
}

func (g GenerateAPIKeys) keyValue(ctx context.Context, spec domain.ClusterSpec, key string, existing string) (string, error) {
	if existing != "" {
		return existing, nil
	}
	carried, err := g.read(ctx, domain.SecretRef{Name: spec.ChartKeysSecret(), Key: key, Namespace: spec.Namespace})
	if err != nil || carried != "" {
		return carried, err
	}
	reader := g.Random
	if reader == nil {
		reader = rand.Reader
	}
	return RandomKey(reader)
}

func (g GenerateAPIKeys) read(ctx context.Context, ref domain.SecretRef) (string, error) {
	value, err := g.Kubernetes.SecretValue(ctx, ref)
	if errors.Is(err, ports.ErrSecretKeyNotFound) {
		return "", nil
	}
	return value, err
}

// RandomKey draws 32 alphanumeric characters from reader without modulo bias.
func RandomKey(reader io.Reader) (string, error) {
	limit := big.NewInt(int64(len(apiKeyAlphabet)))
	key := make([]byte, 0, apiKeyLength)
	for len(key) < apiKeyLength {
		index, err := rand.Int(reader, limit)
		if err != nil {
			return "", fmt.Errorf("generate api key: %w", err)
		}
		key = append(key, apiKeyAlphabet[index.Int64()])
	}
	return string(key), nil
}
