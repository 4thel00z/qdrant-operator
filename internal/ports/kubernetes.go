package ports

import (
	"context"
	"errors"

	"github.com/4thel00z/qdrant-operator/internal/domain"
)

var (
	ErrSecretKeyNotFound = errors.New("secret or secret key not found")
	ErrSecretNotOwned    = errors.New("secret exists and is not owned by this resource")
)

// Kubernetes reads and writes cluster objects. Custom resources travel as their JSON bodies; a
// nil body means the resource is absent. PatchStatus is a JSON merge patch on the status
// subresource, so a nil value clears a field.
type Kubernetes interface {
	SecretValue(ctx context.Context, ref domain.SecretRef) (string, error)
	ApplySecret(ctx context.Context, name, namespace string, data map[string]string, owner domain.OwnerReference) error

	CustomResource(ctx context.Context, ref domain.ResourceRef) (domain.JSON, error)
	ListCustomResources(ctx context.Context, kind domain.ResourceKind, namespace, labelSelector string) ([]domain.JSON, error)
	CreateCustomResource(ctx context.Context, body domain.JSON) (domain.JSON, error)
	DeleteCustomResource(ctx context.Context, ref domain.ResourceRef) error
	PatchStatus(ctx context.Context, ref domain.ResourceRef, status domain.JSON) error

	StatefulSetStatus(ctx context.Context, name, namespace string) (*domain.StatefulSetStatus, error)
	SetStatefulSetPartition(ctx context.Context, name, namespace string, partition int) error
	OrphanStatefulSet(ctx context.Context, name, namespace string) error
	StatefulSetClaims(ctx context.Context, name, namespace string) (map[string]string, error)
	ListPodStates(ctx context.Context, namespace, labelSelector string) ([]domain.PodState, error)
	CrashLog(ctx context.Context, name, namespace string, tailLines int) (string, error)

	ListVolumeClaims(ctx context.Context, namespace, labelSelector string) ([]domain.VolumeClaim, error)
	CreateVolumeClaim(ctx context.Context, body domain.JSON) error
	ResizeVolumeClaim(ctx context.Context, name, namespace, size string) error
	DeleteVolumeClaim(ctx context.Context, name, namespace string) error
	VolumeZone(ctx context.Context, volumeName string) (*domain.Zone, error)
	NodeInZone(ctx context.Context, zone domain.Zone) (string, error)

	CreateVolumeSnapshot(ctx context.Context, body domain.JSON) error
	VolumeSnapshot(ctx context.Context, name, namespace string) (*domain.VolumeSnapshotState, error)
	ListVolumeSnapshots(ctx context.Context, namespace, labelSelector string) ([]string, error)
	DeleteVolumeSnapshot(ctx context.Context, name, namespace string) error
}
