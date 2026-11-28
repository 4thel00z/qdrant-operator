package controller

import (
	"fmt"

	ctrl "sigs.k8s.io/controller-runtime"
)

// Setup registers every reconciler with the manager.
func Setup(manager ctrl.Manager, deps Dependencies, concurrency Concurrency) error {
	registrations := []struct {
		kind  string
		setup func() error
	}{
		{"QdrantCollection", func() error {
			return (&CollectionReconciler{Dependencies: deps}).SetupWithManager(manager, concurrency.Levels)
		}},
	}
	for _, registration := range registrations {
		if err := registration.setup(); err != nil {
			return fmt.Errorf("set up %s controller: %w", registration.kind, err)
		}
	}
	return nil
}
