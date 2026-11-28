package kubernetes

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/4thel00z/qdrant-operator/internal/domain"
)

func (a *Adapter) CustomResource(ctx context.Context, ref domain.ResourceRef) (domain.JSON, error) {
	object := unstructuredOf(resourceGVK(ref.Kind), ref.Name, ref.Namespace)
	err := a.Reader.Get(ctx, client.ObjectKeyFromObject(object), object)
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get %s %s/%s: %w", ref.Kind.Kind, ref.Namespace, ref.Name, err)
	}
	return object.Object, nil
}

func (a *Adapter) ListCustomResources(ctx context.Context, kind domain.ResourceKind, namespace, labelSelector string) ([]domain.JSON, error) {
	options, err := listOptions(namespace, labelSelector)
	if err != nil {
		return nil, err
	}
	list := listOf(resourceGVK(kind))
	if err := a.Reader.List(ctx, list, options...); err != nil {
		return nil, fmt.Errorf("list %s in %s: %w", kind.Kind, namespace, err)
	}
	return bodies(list.Items), nil
}

func (a *Adapter) CreateCustomResource(ctx context.Context, body domain.JSON) (domain.JSON, error) {
	object := &unstructured.Unstructured{Object: body}
	if object.GetAPIVersion() == "" {
		object.SetAPIVersion(domain.APIVersion)
	}
	if err := a.Writer.Create(ctx, object); err != nil {
		return nil, fmt.Errorf("create %s %s/%s: %w", object.GetKind(), object.GetNamespace(), object.GetName(), err)
	}
	return object.Object, nil
}

func (a *Adapter) DeleteCustomResource(ctx context.Context, ref domain.ResourceRef) error {
	object := unstructuredOf(resourceGVK(ref.Kind), ref.Name, ref.Namespace)
	if err := ignoreNotFound(a.Writer.Delete(ctx, object)); err != nil {
		return fmt.Errorf("delete %s %s/%s: %w", ref.Kind.Kind, ref.Namespace, ref.Name, err)
	}
	return nil
}

// PatchStatus ignores an absent resource: a status written after the resource was deleted has nowhere to go.
func (a *Adapter) PatchStatus(ctx context.Context, ref domain.ResourceRef, status domain.JSON) error {
	patch, err := mergePatch(domain.JSON{"status": status})
	if err != nil {
		return err
	}
	object := unstructuredOf(resourceGVK(ref.Kind), ref.Name, ref.Namespace)
	if err := ignoreNotFound(a.Writer.Status().Patch(ctx, object, patch)); err != nil {
		return fmt.Errorf("patch status of %s %s/%s: %w", ref.Kind.Kind, ref.Namespace, ref.Name, err)
	}
	return nil
}
