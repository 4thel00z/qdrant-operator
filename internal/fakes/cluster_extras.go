package fakes

import (
	"context"
	"sync/atomic"

	"github.com/4thel00z/qdrant-operator/internal/ports"
)

// CountingHelm is a Helm that counts every Apply call, failed ones included.
type CountingHelm struct {
	*Helm
	calls atomic.Int64
}

var _ ports.Helm = (*CountingHelm)(nil)

func NewCountingHelm() *CountingHelm {
	return &CountingHelm{Helm: NewHelm()}
}

func (h *CountingHelm) Apply(ctx context.Context, release ports.ReleaseRequest) error {
	h.calls.Add(1)
	return h.Helm.Apply(ctx, release)
}

func (h *CountingHelm) ApplyCalls() int {
	return int(h.calls.Load())
}
