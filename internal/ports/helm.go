package ports

import (
	"context"

	"github.com/4thel00z/qdrant-operator/internal/domain"
)

// ReleaseRequest is one desired Helm release state. The StatefulSet named
// StatefulSet is pinned to a RollingUpdate strategy with the given Partition.
type ReleaseRequest struct {
	Name         string
	Namespace    string
	ChartVersion string
	Values       domain.JSON
	StatefulSet  string
	Partition    int
}

// Helm installs, upgrades and removes releases. Apply is an upgrade-or-install
// that first recovers a release left in a pending state.
type Helm interface {
	Apply(ctx context.Context, release ReleaseRequest) error
	Uninstall(ctx context.Context, name, namespace string) error
	ReleaseExists(ctx context.Context, name, namespace string) (bool, error)
}
