package kubernetes

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/4thel00z/qdrant-operator/internal/domain"
)

// CreateVolumeSnapshot treats an existing snapshot as success.
func (a *Adapter) CreateVolumeSnapshot(ctx context.Context, body domain.JSON) error {
	object := &unstructured.Unstructured{Object: body}
	if object.GetAPIVersion() == "" {
		object.SetAPIVersion(snapshotGVK().GroupVersion().String())
	}
	if object.GetKind() == "" {
		object.SetKind(volumeSnapshotKind)
	}
	if err := ignoreAlreadyExists(a.Writer.Create(ctx, object)); err != nil {
		return fmt.Errorf("create volume snapshot %s/%s: %w", object.GetNamespace(), object.GetName(), err)
	}
	return nil
}

func (a *Adapter) VolumeSnapshot(ctx context.Context, name, namespace string) (*domain.VolumeSnapshotState, error) {
	object := unstructuredOf(snapshotGVK(), name, namespace)
	err := a.Reader.Get(ctx, client.ObjectKeyFromObject(object), object)
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get volume snapshot %s/%s: %w", namespace, name, err)
	}
	ready, _, _ := unstructured.NestedBool(object.Object, "status", "readyToUse")
	message, _, _ := unstructured.NestedString(object.Object, "status", "error", "message")
	return &domain.VolumeSnapshotState{Name: name, Ready: ready, Error: message}, nil
}

func (a *Adapter) ListVolumeSnapshots(ctx context.Context, namespace, labelSelector string) ([]string, error) {
	options, err := listOptions(namespace, labelSelector)
	if err != nil {
		return nil, err
	}
	list := listOf(snapshotGVK())
	if err := a.Reader.List(ctx, list, options...); err != nil {
		return nil, fmt.Errorf("list volume snapshots in %s: %w", namespace, err)
	}
	names := make([]string, 0, len(list.Items))
	for _, item := range list.Items {
		names = append(names, item.GetName())
	}
	return names, nil
}

func (a *Adapter) DeleteVolumeSnapshot(ctx context.Context, name, namespace string) error {
	object := unstructuredOf(snapshotGVK(), name, namespace)
	if err := ignoreNotFound(a.Writer.Delete(ctx, object)); err != nil {
		return fmt.Errorf("delete volume snapshot %s/%s: %w", namespace, name, err)
	}
	return nil
}
