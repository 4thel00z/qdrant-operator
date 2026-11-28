package usecases

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/ports"
)

// ExpandVolumes grows PVCs and recreates the StatefulSet (pods kept) when its volume claims change.
type ExpandVolumes struct {
	Kubernetes ports.Kubernetes
}

// Execute reports whether the StatefulSet was deleted, so the helm apply must recreate it.
// It returns RolloutInProgressError instead of recreating while a held rollout runs, since the new StatefulSet would lose its partition.
func (e ExpandVolumes) Execute(ctx context.Context, spec domain.ClusterSpec, rolling bool) (bool, error) {
	live, err := e.Kubernetes.StatefulSetClaims(ctx, spec.ReleaseName(), spec.Namespace)
	if err != nil {
		return false, fmt.Errorf("read statefulset claims: %w", err)
	}
	if live == nil {
		return false, nil
	}
	desired := spec.ClaimSizes()
	plan, err := domain.PlanVolumes(desired, live)
	if err != nil {
		return false, err
	}
	if err := e.GrowClaims(ctx, spec, desired); err != nil {
		return false, err
	}
	if !plan.Recreate {
		return false, nil
	}
	if rolling {
		return false, &RolloutInProgressError{Message: "volume claims change while pods are restarting"}
	}
	if err := e.Kubernetes.OrphanStatefulSet(ctx, spec.ReleaseName(), spec.Namespace); err != nil {
		return false, fmt.Errorf("orphan statefulset: %w", err)
	}
	return true, nil
}

// GrowClaims patches every PVC that requests less than the spec; it never asks for a shrink.
func (e ExpandVolumes) GrowClaims(ctx context.Context, spec domain.ClusterSpec, desired map[string]string) error {
	claims, err := e.Kubernetes.ListVolumeClaims(ctx, spec.Namespace, spec.VolumeSelector())
	if err != nil {
		return fmt.Errorf("list volume claims: %w", err)
	}
	for _, template := range slices.Sorted(maps.Keys(desired)) {
		size := desired[template]
		prefix := template + "-" + spec.ReleaseName() + "-"
		wanted, err := domain.ParseQuantity(size)
		if err != nil {
			return fmt.Errorf("desired claim %s: %w", template, err)
		}
		for _, claim := range claims {
			if !strings.HasPrefix(claim.Name, prefix) {
				continue
			}
			requested, err := domain.ParseQuantity(claim.Size)
			if err != nil {
				return fmt.Errorf("claim %s: %w", claim.Name, err)
			}
			if requested >= wanted {
				continue
			}
			if err := e.Kubernetes.ResizeVolumeClaim(ctx, claim.Name, spec.Namespace, size); err != nil {
				return fmt.Errorf("resize claim %s: %w", claim.Name, err)
			}
		}
	}
	return nil
}
