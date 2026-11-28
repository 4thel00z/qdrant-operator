// Package app builds the controller-runtime manager from Settings and runs it.
package app

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	qdrantv1alpha1 "github.com/4thel00z/qdrant-operator/api/v1alpha1"
	"github.com/4thel00z/qdrant-operator/internal/controller"
	"github.com/4thel00z/qdrant-operator/internal/settings"
)

const metricsDisabled = "0"

func Run(ctx context.Context, config settings.Settings, logOutput io.Writer) error {
	logger, err := NewLogger(config.Logging, logOutput)
	if err != nil {
		return err
	}
	ctrl.SetLogger(logger)

	restConfig, err := RESTConfig(config.Kubernetes)
	if err != nil {
		return err
	}
	scheme, err := NewScheme()
	if err != nil {
		return err
	}
	manager, err := ctrl.NewManager(restConfig, ManagerOptions(config, scheme))
	if err != nil {
		return fmt.Errorf("create manager: %w", err)
	}
	deps, err := Dependencies(manager, restConfig, config)
	if err != nil {
		return err
	}
	if err := controller.Setup(manager, deps, Concurrency(config.Manager)); err != nil {
		return err
	}
	if err := manager.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		return fmt.Errorf("add healthz check: %w", err)
	}
	if err := manager.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		return fmt.Errorf("add readyz check: %w", err)
	}
	logger.Info("starting manager", "namespaces", config.Manager.Namespaces, "leaderElection", config.Manager.LeaderElection)
	return manager.Start(ctx)
}

func NewScheme() (*runtime.Scheme, error) {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("register client-go types: %w", err)
	}
	if err := qdrantv1alpha1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("register qdrant.io types: %w", err)
	}
	return scheme, nil
}

func ManagerOptions(config settings.Settings, scheme *runtime.Scheme) ctrl.Options {
	leaseDuration := config.Manager.LeaseDuration
	metricsAddress := config.Metrics.Address
	if metricsAddress == "" {
		metricsAddress = metricsDisabled
	}
	return ctrl.Options{
		Scheme:                        scheme,
		Metrics:                       metricsserver.Options{BindAddress: metricsAddress},
		HealthProbeBindAddress:        config.Manager.HealthProbeAddress,
		LeaderElection:                config.Manager.LeaderElection,
		LeaderElectionID:              config.Manager.LeaderElectionID,
		LeaderElectionReleaseOnCancel: true,
		LeaseDuration:                 &leaseDuration,
		Cache:                         cache.Options{DefaultNamespaces: NamespaceConfigs(config.Manager.Namespaces)},
	}
}

func NamespaceConfigs(namespaces []string) map[string]cache.Config {
	if len(namespaces) == 0 {
		return nil
	}
	configs := make(map[string]cache.Config, len(namespaces))
	for _, namespace := range namespaces {
		configs[namespace] = cache.Config{}
	}
	return configs
}

func RESTConfig(config settings.Kubernetes) (*rest.Config, error) {
	if config.Kubeconfig == "" {
		restConfig, err := ctrl.GetConfig()
		if err != nil {
			return nil, fmt.Errorf("load kubernetes config: %w", err)
		}
		return restConfig, nil
	}
	restConfig, err := clientcmd.BuildConfigFromFlags("", config.Kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig %s: %w", config.Kubeconfig, err)
	}
	return restConfig, nil
}

func NewLogger(config settings.Logging, output io.Writer) (logr.Logger, error) {
	var level slog.Level
	if err := level.UnmarshalText([]byte(config.Level)); err != nil {
		return logr.Logger{}, fmt.Errorf("logging level %q: %w", config.Level, err)
	}
	options := &slog.HandlerOptions{Level: level}
	if config.Format == "text" {
		return logr.FromSlogHandler(slog.NewTextHandler(output, options)), nil
	}
	return logr.FromSlogHandler(slog.NewJSONHandler(output, options)), nil
}
