package translate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	qdrantv1alpha1 "github.com/4thel00z/qdrant-operator/api/v1alpha1"
	"github.com/4thel00z/qdrant-operator/internal/domain"
)

func AccessKeySpecFromAPI(name string, namespace string, spec qdrantv1alpha1.QdrantAccessKeySpec) (domain.AccessKeySpec, error) {
	key := domain.DefaultAccessKeySpec(name, namespace, accessMigrationClusterRef(spec.ClusterRef, namespace))
	if spec.SecretName != "" {
		key.SecretName = spec.SecretName
	}
	key.GlobalAccess = spec.Access
	for _, collection := range spec.Collections {
		key.Collections = append(key.Collections, domain.CollectionAccess{Name: collection.Name, Access: string(collection.Access)})
	}
	key.Subject = spec.Subject
	if spec.TTL != "" {
		ttl, err := domain.ParseDuration(spec.TTL)
		if err != nil {
			return domain.AccessKeySpec{}, fmt.Errorf("ttl: %w", err)
		}
		key.TTL = ttl
	}
	if spec.RenewBefore != "" {
		renewBefore, err := domain.ParseDuration(spec.RenewBefore)
		if err != nil {
			return domain.AccessKeySpec{}, fmt.Errorf("renewBefore: %w", err)
		}
		key.RenewBefore = renewBefore
	}
	valueExists, err := valueExistsFromAPI(spec.ValueExists)
	if err != nil {
		return domain.AccessKeySpec{}, fmt.Errorf("valueExists: %w", err)
	}
	key.ValueExists = valueExists
	if err := key.Validate(); err != nil {
		return domain.AccessKeySpec{}, err
	}
	return key, nil
}

// valueExistsFromAPI keeps match values exact by decoding numbers as json.Number.
func valueExistsFromAPI(spec *qdrantv1alpha1.AccessKeyValueExists) (domain.JSON, error) {
	if spec == nil {
		return nil, nil
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	object := domain.JSON{}
	if err := decoder.Decode(&object); err != nil {
		return nil, err
	}
	return object, nil
}

func AccessKeyStatusFromAPI(namespace string, status qdrantv1alpha1.QdrantAccessKeyStatus) (domain.AccessKeyStatus, error) {
	result := domain.AccessKeyStatus{
		Phase:              domain.AccessKeyPhase(status.Phase),
		KeyFingerprint:     status.KeyFingerprint,
		Error:              status.Error,
		ObservedGeneration: status.ObservedGeneration,
	}
	if result.Phase == "" {
		result.Phase = domain.AccessKeyPending
	}
	if status.SecretRef != nil {
		result.SecretRef = &domain.SecretRef{Name: status.SecretRef.Name, Key: status.SecretRef.Key, Namespace: namespace}
	}
	times := []struct {
		field string
		text  string
		into  *time.Time
	}{
		{"issuedAt", status.IssuedAt, &result.IssuedAt},
		{"expiresAt", status.ExpiresAt, &result.ExpiresAt},
		{"renewAt", status.RenewAt, &result.RenewAt},
	}
	for _, entry := range times {
		parsed, err := domain.ParseTime(entry.text)
		if err != nil {
			return domain.AccessKeyStatus{}, fmt.Errorf("%s: %w", entry.field, err)
		}
		*entry.into = parsed
	}
	conditions, err := accessMigrationConditionsFromAPI(status.Conditions)
	if err != nil {
		return domain.AccessKeyStatus{}, err
	}
	result.Conditions = conditions
	return result, nil
}

func AccessKeyStatusToAPI(status domain.AccessKeyStatus) qdrantv1alpha1.QdrantAccessKeyStatus {
	result := qdrantv1alpha1.QdrantAccessKeyStatus{
		Phase:              qdrantv1alpha1.AccessKeyPhase(status.Phase),
		IssuedAt:           accessMigrationTime(status.IssuedAt),
		ExpiresAt:          accessMigrationTime(status.ExpiresAt),
		RenewAt:            accessMigrationTime(status.RenewAt),
		KeyFingerprint:     status.KeyFingerprint,
		Error:              status.Error,
		ObservedGeneration: status.ObservedGeneration,
		Conditions:         accessMigrationConditionsToAPI(status.Conditions),
	}
	if status.SecretRef != nil {
		result.SecretRef = &qdrantv1alpha1.AccessKeySecretRef{Name: status.SecretRef.Name, Key: status.SecretRef.Key}
	}
	return result
}
