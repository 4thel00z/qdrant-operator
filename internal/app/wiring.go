package app

import (
	"crypto/rand"
	"fmt"

	clientgo "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/4thel00z/qdrant-operator/internal/adapters/cron"
	"github.com/4thel00z/qdrant-operator/internal/adapters/helm"
	"github.com/4thel00z/qdrant-operator/internal/adapters/jwt"
	"github.com/4thel00z/qdrant-operator/internal/adapters/kubernetes"
	"github.com/4thel00z/qdrant-operator/internal/adapters/qdrant"
	"github.com/4thel00z/qdrant-operator/internal/adapters/s3"
	"github.com/4thel00z/qdrant-operator/internal/controller"
	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/settings"
	"github.com/4thel00z/qdrant-operator/internal/usecases"
)

// Dependencies builds the adapters the reconcilers share. Kubernetes reads go through the API
// reader, so they are always live and need no informer for the objects the operator does not watch.
func Dependencies(manager ctrl.Manager, restConfig *rest.Config, config settings.Settings) (controller.Dependencies, error) {
	logs, err := clientgo.NewForConfig(restConfig)
	if err != nil {
		return controller.Dependencies{}, fmt.Errorf("create clientset: %w", err)
	}
	qdrantClient, err := qdrant.New(config.Qdrant)
	if err != nil {
		return controller.Dependencies{}, err
	}
	metrics, err := controller.NewMetrics(ctrlmetrics.Registry)
	if err != nil {
		return controller.Dependencies{}, err
	}
	return controller.Dependencies{
		Client:     manager.GetClient(),
		Recorder:   manager.GetEventRecorder(EventSource),
		Live:       manager.GetAPIReader(),
		Kubernetes: kubernetes.New(manager.GetAPIReader(), manager.GetClient(), logs),
		Helm:       helm.New(config.Helm, restConfig),
		Qdrant:     qdrantClient,
		Storage:    s3.Adapter{},
		Token:      jwt.Adapter{},
		Cron:       ParseCron,
		Clock:      usecases.SystemClock,
		Random:     rand.Reader,
		Metrics:    metrics,
		Timing:     controller.DefaultTiming(),
	}, nil
}

const EventSource = "qdrant-operator"

func ParseCron(expression string) (domain.CronSchedule, error) {
	return cron.Parse(expression)
}

func Concurrency(config settings.Manager) controller.Concurrency {
	return controller.Concurrency{OneShots: config.MaxConcurrentOneShots, Levels: config.MaxConcurrentLevels}
}
