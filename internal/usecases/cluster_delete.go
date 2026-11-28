package usecases

import (
	"context"
	"fmt"

	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/ports"
)

// DeleteCluster uninstalls the release; with reclaimPolicy Delete it also deletes the volume claims.
type DeleteCluster struct {
	Helm       ports.Helm
	Kubernetes ports.Kubernetes
}

func (d DeleteCluster) Execute(ctx context.Context, spec domain.ClusterSpec) error {
	exists, err := d.Helm.ReleaseExists(ctx, spec.ReleaseName(), spec.Namespace)
	if err != nil {
		return fmt.Errorf("check helm release: %w", err)
	}
	if exists {
		if err := d.Helm.Uninstall(ctx, spec.ReleaseName(), spec.Namespace); err != nil {
			return fmt.Errorf("uninstall helm release: %w", err)
		}
	}
	if spec.Persistence.ReclaimPolicy != domain.ReclaimDelete {
		return nil
	}
	claims, err := d.Kubernetes.ListVolumeClaims(ctx, spec.Namespace, spec.VolumeSelector())
	if err != nil {
		return fmt.Errorf("list volume claims: %w", err)
	}
	for _, claim := range claims {
		if err := d.Kubernetes.DeleteVolumeClaim(ctx, claim.Name, spec.Namespace); err != nil {
			return fmt.Errorf("delete claim %s: %w", claim.Name, err)
		}
	}
	return nil
}
