package fakes

import (
	"context"
	"sort"
	"sync"

	"github.com/4thel00z/qdrant-operator/internal/ports"
)

// Helm is an in-memory ports.Helm recording the last applied request per release.
type Helm struct {
	mu           sync.Mutex
	releases     map[string]ports.ReleaseRequest
	ApplyErr     error
	UninstallErr error
}

var _ ports.Helm = (*Helm)(nil)

func NewHelm() *Helm {
	return &Helm{releases: map[string]ports.ReleaseRequest{}}
}

func releaseKey(name, namespace string) string {
	return namespace + "/" + name
}

func (h *Helm) Apply(ctx context.Context, release ports.ReleaseRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.ApplyErr != nil {
		return h.ApplyErr
	}
	h.releases[releaseKey(release.Name, release.Namespace)] = release
	return nil
}

func (h *Helm) Uninstall(ctx context.Context, name, namespace string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.UninstallErr != nil {
		return h.UninstallErr
	}
	delete(h.releases, releaseKey(name, namespace))
	return nil
}

func (h *Helm) ReleaseExists(ctx context.Context, name, namespace string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	_, found := h.releases[releaseKey(name, namespace)]
	return found, nil
}

func (h *Helm) Release(name, namespace string) (ports.ReleaseRequest, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	release, found := h.releases[releaseKey(name, namespace)]
	return release, found
}

func (h *Helm) Releases() []ports.ReleaseRequest {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]ports.ReleaseRequest, 0, len(h.releases))
	for _, release := range h.releases {
		out = append(out, release)
	}
	sort.Slice(out, func(i, j int) bool {
		return releaseKey(out[i].Name, out[i].Namespace) < releaseKey(out[j].Name, out[j].Namespace)
	})
	return out
}
