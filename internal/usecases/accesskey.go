package usecases

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/ports"
)

// IssueAccessKey signs a Qdrant RBAC token with the cluster's API key and keeps it in a Secret.
// The token is re-issued when the spec changes, when the cluster's API key changes, when the
// Secret disappears, or when the renewal time is reached.
type IssueAccessKey struct {
	Kubernetes ports.Kubernetes
	Token      ports.Token
}

// Execute returns the new status, equal to current when nothing needed doing. A cluster without jwtRbac, without an
// API key, or a Secret the access key does not own is reported as a Pending status with a Ready=False condition.
func (i IssueAccessKey) Execute(ctx context.Context, spec domain.AccessKeySpec, generation int64, current domain.AccessKeyStatus, owner domain.OwnerReference, now time.Time) (domain.AccessKeyStatus, error) {
	body, err := i.Kubernetes.CustomResource(ctx, spec.ClusterRef.ResourceRef())
	if err != nil {
		return domain.AccessKeyStatus{}, err
	}
	if body == nil {
		return domain.AccessKeyStatus{}, fmt.Errorf("QdrantCluster %s: %w", spec.ClusterRef.Name, ErrClusterNotFound)
	}
	cluster, err := ClusterSpecFromBody(body)
	if err != nil {
		return domain.AccessKeyStatus{}, err
	}
	if !cluster.APIKey.JWTRbac {
		return blockedAccessKey(current, generation, "JwtRbacDisabled", "cluster spec.apiKey.jwtRbac is false", now), nil
	}
	signingKeyRef := cluster.APIKeyRef()
	if signingKeyRef == nil {
		return blockedAccessKey(current, generation, "ApiKeyMissing", "cluster has no apiKey", now), nil
	}

	apiKey, err := i.Kubernetes.SecretValue(ctx, *signingKeyRef)
	if err != nil {
		return domain.AccessKeyStatus{}, err
	}
	fingerprint := domain.KeyFingerprint(apiKey)
	present, err := i.secretPresent(ctx, spec)
	if err != nil {
		return domain.AccessKeyStatus{}, err
	}
	if current.TokenCurrent(generation, fingerprint, present, now) {
		return current, nil
	}

	token, err := i.Token.Sign(spec.Claims(now), apiKey)
	if err != nil {
		return domain.AccessKeyStatus{}, fmt.Errorf("sign token: %w", err)
	}
	data := map[string]string{
		domain.AccessKeyTokenSecretKey: token,
		domain.AccessKeyURLSecretKey:   cluster.ServiceURL(),
	}
	err = i.Kubernetes.ApplySecret(ctx, spec.SecretName, spec.Namespace, data, owner)
	if errors.Is(err, ports.ErrSecretNotOwned) {
		return blockedAccessKey(current, generation, "SecretNotOwned", err.Error(), now), nil
	}
	if err != nil {
		return domain.AccessKeyStatus{}, fmt.Errorf("write secret %s: %w", spec.SecretName, err)
	}
	secretRef := spec.TokenSecretRef()
	condition := domain.Condition{
		Type:               domain.ConditionReady,
		Status:             domain.ConditionTrue,
		LastTransitionTime: now,
		Reason:             "TokenIssued",
		Message:            "Token written to secret " + spec.SecretName,
	}
	return domain.AccessKeyStatus{
		Phase:              domain.AccessKeyReady,
		SecretRef:          &secretRef,
		IssuedAt:           now,
		ExpiresAt:          spec.ExpiresAt(now),
		RenewAt:            spec.RenewAt(now),
		KeyFingerprint:     fingerprint,
		ObservedGeneration: generation,
		Conditions:         domain.SetCondition(current.Conditions, condition),
	}, nil
}

func (i IssueAccessKey) secretPresent(ctx context.Context, spec domain.AccessKeySpec) (bool, error) {
	_, err := i.Kubernetes.SecretValue(ctx, spec.TokenSecretRef())
	if errors.Is(err, ports.ErrSecretKeyNotFound) {
		return false, nil
	}
	return err == nil, err
}

func blockedAccessKey(current domain.AccessKeyStatus, generation int64, reason string, message string, now time.Time) domain.AccessKeyStatus {
	condition := domain.Condition{
		Type:               domain.ConditionReady,
		Status:             domain.ConditionFalse,
		LastTransitionTime: now,
		Reason:             reason,
		Message:            message,
	}
	current.Phase = domain.AccessKeyPending
	current.Error = message
	current.ObservedGeneration = generation
	current.Conditions = domain.SetCondition(current.Conditions, condition)
	return current
}
