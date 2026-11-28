package kubernetes

import (
	"context"
	"fmt"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/4thel00z/qdrant-operator/internal/domain"
)

func (a *Adapter) ListVolumeClaims(ctx context.Context, namespace, labelSelector string) ([]domain.VolumeClaim, error) {
	options, err := listOptions(namespace, labelSelector)
	if err != nil {
		return nil, err
	}
	claims := &corev1.PersistentVolumeClaimList{}
	if err := a.Reader.List(ctx, claims, options...); err != nil {
		return nil, fmt.Errorf("list persistent volume claims in %s: %w", namespace, err)
	}
	result := make([]domain.VolumeClaim, 0, len(claims.Items))
	for _, claim := range claims.Items {
		result = append(result, volumeClaim(claim))
	}
	return result, nil
}

func volumeClaim(claim corev1.PersistentVolumeClaim) domain.VolumeClaim {
	modes := []string{}
	for _, mode := range claim.Spec.AccessModes {
		modes = append(modes, string(mode))
	}
	if len(modes) == 0 {
		modes = []string{string(corev1.ReadWriteOnce)}
	}
	storageClass := ""
	if claim.Spec.StorageClassName != nil {
		storageClass = *claim.Spec.StorageClassName
	}
	return domain.VolumeClaim{
		Name:         claim.Name,
		Size:         requestedSize(claim.Spec.Resources.Requests),
		StorageClass: storageClass,
		AccessModes:  modes,
		VolumeName:   claim.Spec.VolumeName,
	}
}

// CreateVolumeClaim treats an existing claim as success.
func (a *Adapter) CreateVolumeClaim(ctx context.Context, body domain.JSON) error {
	object := &unstructured.Unstructured{Object: body}
	if object.GetAPIVersion() == "" {
		object.SetAPIVersion("v1")
	}
	if object.GetKind() == "" {
		object.SetKind("PersistentVolumeClaim")
	}
	if err := ignoreAlreadyExists(a.Writer.Create(ctx, object)); err != nil {
		return fmt.Errorf("create persistent volume claim %s/%s: %w", object.GetNamespace(), object.GetName(), err)
	}
	return nil
}

func (a *Adapter) ResizeVolumeClaim(ctx context.Context, name, namespace, size string) error {
	patch, err := mergePatch(domain.JSON{"spec": domain.JSON{"resources": domain.JSON{"requests": domain.JSON{"storage": size}}}})
	if err != nil {
		return err
	}
	claim := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}}
	if err := a.Writer.Patch(ctx, claim, patch); err != nil {
		return fmt.Errorf("resize persistent volume claim %s/%s: %w", namespace, name, err)
	}
	return nil
}

func (a *Adapter) DeleteVolumeClaim(ctx context.Context, name, namespace string) error {
	claim := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}}
	if err := ignoreNotFound(a.Writer.Delete(ctx, claim)); err != nil {
		return fmt.Errorf("delete persistent volume claim %s/%s: %w", namespace, name, err)
	}
	return nil
}

// VolumeZone returns the zone a volume's node affinity pins it to, or nil when it has none or the volume is absent.
func (a *Adapter) VolumeZone(ctx context.Context, volumeName string) (*domain.Zone, error) {
	volume := &corev1.PersistentVolume{}
	err := a.Reader.Get(ctx, client.ObjectKey{Name: volumeName}, volume)
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get persistent volume %s: %w", volumeName, err)
	}
	return zoneOf(volume), nil
}

func zoneOf(volume *corev1.PersistentVolume) *domain.Zone {
	affinity := volume.Spec.NodeAffinity
	if affinity == nil || affinity.Required == nil {
		return nil
	}
	for _, term := range affinity.Required.NodeSelectorTerms {
		for _, expression := range term.MatchExpressions {
			if !strings.HasSuffix(expression.Key, "zone") || expression.Operator != corev1.NodeSelectorOpIn || len(expression.Values) != 1 {
				continue
			}
			return &domain.Zone{Label: expression.Key, Name: expression.Values[0]}
		}
	}
	return nil
}

// NodeInZone returns the alphabetically first Ready, schedulable node carrying the zone label, or "" when there is none.
func (a *Adapter) NodeInZone(ctx context.Context, zone domain.Zone) (string, error) {
	nodes := &corev1.NodeList{}
	if err := a.Reader.List(ctx, nodes, client.MatchingLabels{zone.Label: zone.Name}); err != nil {
		return "", fmt.Errorf("list nodes in zone %s=%s: %w", zone.Label, zone.Name, err)
	}
	names := []string{}
	for _, node := range nodes.Items {
		if node.Spec.Unschedulable || !nodeReady(node) {
			continue
		}
		names = append(names, node.Name)
	}
	if len(names) == 0 {
		return "", nil
	}
	return slices.Min(names), nil
}

func nodeReady(node corev1.Node) bool {
	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeReady && condition.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}
