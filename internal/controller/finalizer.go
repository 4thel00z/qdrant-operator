package controller

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// EnsureFinalizer adds the operator finalizer with a merge patch on metadata.
func EnsureFinalizer(ctx context.Context, writer client.Client, object client.Object) error {
	if controllerutil.ContainsFinalizer(object, Finalizer) {
		return nil
	}
	base, err := copyOf(object)
	if err != nil {
		return err
	}
	controllerutil.AddFinalizer(object, Finalizer)
	if err := writer.Patch(ctx, object, client.MergeFrom(base)); err != nil {
		return fmt.Errorf("add finalizer to %s/%s: %w", object.GetNamespace(), object.GetName(), err)
	}
	return nil
}

// ReleaseFinalizer removes the operator finalizer so the API server can finish the deletion.
func ReleaseFinalizer(ctx context.Context, writer client.Client, object client.Object) error {
	if !controllerutil.ContainsFinalizer(object, Finalizer) {
		return nil
	}
	base, err := copyOf(object)
	if err != nil {
		return err
	}
	controllerutil.RemoveFinalizer(object, Finalizer)
	if err := client.IgnoreNotFound(writer.Patch(ctx, object, client.MergeFrom(base))); err != nil {
		return fmt.Errorf("remove finalizer from %s/%s: %w", object.GetNamespace(), object.GetName(), err)
	}
	return nil
}

func copyOf(object client.Object) (client.Object, error) {
	copied, ok := object.DeepCopyObject().(client.Object)
	if !ok {
		return nil, fmt.Errorf("copy %T: deep copy is not a client.Object", object)
	}
	return copied, nil
}
