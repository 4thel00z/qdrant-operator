// Package kubernetes implements ports.Kubernetes on controller-runtime and client-go.
package kubernetes

import (
	"encoding/json"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	clientgo "k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/ports"
)

const (
	revisionLabel      = "controller-revision-hash"
	defaultOrphanWait  = 60 * time.Second
	defaultOrphanPoll  = time.Second
	volumeSnapshotKind = "VolumeSnapshot"
)

// Adapter reads through Reader and writes through Writer. The operator passes the manager's
// API reader (mgr.GetAPIReader()) as Reader so every read is live and needs no watch
// permission or informer; Writer is the manager's client.
type Adapter struct {
	Reader        client.Reader
	Writer        client.Client
	Logs          clientgo.Interface
	OrphanTimeout time.Duration
	OrphanPoll    time.Duration
}

var _ ports.Kubernetes = (*Adapter)(nil)

func New(reader client.Reader, writer client.Client, logs clientgo.Interface) *Adapter {
	return &Adapter{
		Reader:        reader,
		Writer:        writer,
		Logs:          logs,
		OrphanTimeout: defaultOrphanWait,
		OrphanPoll:    defaultOrphanPoll,
	}
}

func resourceGVK(kind domain.ResourceKind) schema.GroupVersionKind {
	return schema.GroupVersionKind{Group: domain.Group, Version: domain.Version, Kind: kind.Kind}
}

func snapshotGVK() schema.GroupVersionKind {
	return schema.GroupVersionKind{Group: domain.SnapshotAPIGroup, Version: domain.SnapshotAPIVersion, Kind: volumeSnapshotKind}
}

func unstructuredOf(gvk schema.GroupVersionKind, name, namespace string) *unstructured.Unstructured {
	object := &unstructured.Unstructured{}
	object.SetGroupVersionKind(gvk)
	object.SetName(name)
	object.SetNamespace(namespace)
	return object
}

func listOf(gvk schema.GroupVersionKind) *unstructured.UnstructuredList {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(gvk.GroupVersion().WithKind(gvk.Kind + "List"))
	return list
}

func bodies(items []unstructured.Unstructured) []domain.JSON {
	result := make([]domain.JSON, 0, len(items))
	for _, item := range items {
		result = append(result, item.Object)
	}
	return result
}

func mergePatch(document any) (client.Patch, error) {
	raw, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("encode merge patch: %w", err)
	}
	return client.RawPatch(types.MergePatchType, raw), nil
}

func ignoreNotFound(err error) error {
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

func ignoreAlreadyExists(err error) error {
	if apierrors.IsAlreadyExists(err) {
		return nil
	}
	return err
}

func listOptions(namespace, selector string) ([]client.ListOption, error) {
	options := []client.ListOption{}
	if namespace != "" {
		options = append(options, client.InNamespace(namespace))
	}
	if selector == "" {
		return options, nil
	}
	parsed, err := labels.Parse(selector)
	if err != nil {
		return nil, fmt.Errorf("label selector %q: %w", selector, err)
	}
	return append(options, client.MatchingLabelsSelector{Selector: parsed}), nil
}
