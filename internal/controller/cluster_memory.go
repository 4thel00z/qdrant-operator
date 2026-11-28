package controller

import (
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/types"
)

// clusterMemory is what one process remembers about a cluster between reconciles.
type clusterMemory struct {
	lastApply     time.Time
	lastRecover   time.Time
	lastRebalance time.Time
}

type clusterRegistry struct {
	mu      sync.Mutex
	entries map[types.NamespacedName]clusterMemory
}

func (g *clusterRegistry) load(key types.NamespacedName, now time.Time) clusterMemory {
	g.mu.Lock()
	defer g.mu.Unlock()
	memory, found := g.entries[key]
	if found {
		return memory
	}
	memory = clusterMemory{lastRecover: now, lastRebalance: now}
	if g.entries == nil {
		g.entries = map[types.NamespacedName]clusterMemory{}
	}
	g.entries[key] = memory
	return memory
}

func (g *clusterRegistry) store(key types.NamespacedName, memory clusterMemory) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.entries == nil {
		g.entries = map[types.NamespacedName]clusterMemory{}
	}
	g.entries[key] = memory
}

func (g *clusterRegistry) forget(key types.NamespacedName) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.entries, key)
}
