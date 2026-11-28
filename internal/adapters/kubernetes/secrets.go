package kubernetes

import (
	"context"
	"fmt"
	"slices"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/ports"
)

func (a *Adapter) SecretValue(ctx context.Context, ref domain.SecretRef) (string, error) {
	secret := &corev1.Secret{}
	err := a.Reader.Get(ctx, client.ObjectKey{Namespace: ref.Namespace, Name: ref.Name}, secret)
	if apierrors.IsNotFound(err) {
		return "", fmt.Errorf("secret %s/%s: %w", ref.Namespace, ref.Name, ports.ErrSecretKeyNotFound)
	}
	if err != nil {
		return "", fmt.Errorf("get secret %s/%s: %w", ref.Namespace, ref.Name, err)
	}
	value := secret.Data[ref.Key]
	if len(value) == 0 {
		return "", fmt.Errorf("secret %s/%s key %s: %w", ref.Namespace, ref.Name, ref.Key, ports.ErrSecretKeyNotFound)
	}
	return string(value), nil
}

func (a *Adapter) ApplySecret(ctx context.Context, name, namespace string, data map[string]string, owner domain.OwnerReference) error {
	desired := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion:         owner.APIVersion,
				Kind:               owner.Kind,
				Name:               owner.Name,
				UID:                types.UID(owner.UID),
				Controller:         ptr.To(true),
				BlockOwnerDeletion: ptr.To(true),
			}},
		},
		Type:       corev1.SecretTypeOpaque,
		StringData: data,
	}
	err := a.Writer.Create(ctx, desired)
	if err == nil {
		return nil
	}
	if !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("create secret %s/%s: %w", namespace, name, err)
	}
	existing := &corev1.Secret{}
	if err := a.Reader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, existing); err != nil {
		return fmt.Errorf("get secret %s/%s: %w", namespace, name, err)
	}
	owned := slices.ContainsFunc(existing.OwnerReferences, func(reference metav1.OwnerReference) bool {
		return string(reference.UID) == owner.UID
	})
	if !owned {
		return fmt.Errorf("secret %s/%s: %w", namespace, name, ports.ErrSecretNotOwned)
	}
	desired.ResourceVersion = existing.ResourceVersion
	if err := a.Writer.Update(ctx, desired); err != nil {
		return fmt.Errorf("update secret %s/%s: %w", namespace, name, err)
	}
	return nil
}
