package controller_test

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgo "k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	qdrantv1alpha1 "github.com/4thel00z/qdrant-operator/api/v1alpha1"
	"github.com/4thel00z/qdrant-operator/internal/adapters/cron"
	"github.com/4thel00z/qdrant-operator/internal/adapters/jwt"
	"github.com/4thel00z/qdrant-operator/internal/adapters/kubernetes"
	"github.com/4thel00z/qdrant-operator/internal/controller"
	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/fakes"
	"github.com/4thel00z/qdrant-operator/internal/usecases"
)

const settleTimeout = 30 * time.Second

var (
	startOnce   sync.Once
	restConfig  *rest.Config
	startErr    error
	environment *envtest.Environment
)

func TestMain(m *testing.M) {
	code := m.Run()
	if environment != nil {
		_ = environment.Stop()
	}
	os.Exit(code)
}

// world is one namespace with a running manager whose reconcilers talk to the envtest API server
// through the real Kubernetes adapter and to fake Qdrant, Helm and S3.
type world struct {
	Direct    client.Client
	Namespace string
	Deps      controller.Dependencies
	Qdrant    *fakes.Qdrant
	Helm      *fakes.Helm
	Storage   *fakes.Storage
}

// Arrange runs before the manager starts, so it may fill the fakes without racing the reconcilers.
type Arrange func(w *world)

// Register adds the reconcilers under test to the manager.
type Register func(manager ctrl.Manager, deps controller.Dependencies) error

func startWorld(t *testing.T, arrange Arrange, register Register) *world {
	t.Helper()
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS is unset")
	}
	startOnce.Do(func() {
		restConfig, startErr = launch()
	})
	if startErr != nil {
		t.Fatalf("start envtest: %v", startErr)
	}
	scheme, err := newScheme()
	if err != nil {
		t.Fatal(err)
	}
	direct, err := client.New(restConfig, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "controller-"}}
	if err := direct.Create(context.Background(), namespace); err != nil {
		t.Fatalf("create namespace: %v", err)
	}
	skip := true
	manager, err := ctrl.NewManager(restConfig, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
		Cache:                  cache.Options{DefaultNamespaces: map[string]cache.Config{namespace.Name: {}}},
		Controller:             config.Controller{SkipNameValidation: &skip},
	})
	if err != nil {
		t.Fatal(err)
	}
	logs, err := clientgo.NewForConfig(restConfig)
	if err != nil {
		t.Fatal(err)
	}
	metrics, err := controller.NewMetrics(prometheus.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	w := &world{
		Direct:    direct,
		Namespace: namespace.Name,
		Qdrant:    fakes.NewQdrant(),
		Helm:      fakes.NewHelm(),
		Storage:   fakes.NewStorage(),
	}
	w.Deps = controller.Dependencies{
		Client:     manager.GetClient(),
		Live:       manager.GetAPIReader(),
		Kubernetes: kubernetes.New(manager.GetAPIReader(), manager.GetClient(), logs),
		Helm:       w.Helm,
		Qdrant:     w.Qdrant,
		Storage:    w.Storage,
		Token:      jwt.Adapter{},
		Cron:       func(expression string) (domain.CronSchedule, error) { return cron.Parse(expression) },
		Clock:      usecases.SystemClock,
		Random:     rand.Reader,
		Metrics:    metrics,
		Timing:     fastTiming(),
	}
	if arrange != nil {
		arrange(w)
	}
	if err := register(manager, w.Deps); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- manager.Start(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("manager: %v", err)
		}
	})
	return w
}

// fastTiming keeps every requeue short so retries and timers show up within a test.
func fastTiming() controller.Timing {
	return controller.Timing{
		ClusterObserve:      time.Second,
		ScheduleTick:        time.Second,
		CollectionReconcile: time.Second,
		AccessKeyRenew:      time.Second,
		BackupExpiry:        time.Second,
		PeerRecovery:        time.Second,
		Retry:               500 * time.Millisecond,
		ScaleDownPoll:       500 * time.Millisecond,
		HelmRetry:           time.Second,
	}
}

func launch() (*rest.Config, error) {
	environment = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "manifests", "crds")},
		ErrorIfCRDPathMissing: true,
	}
	return environment.Start()
}

func newScheme() (*runtime.Scheme, error) {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		return nil, err
	}
	if err := qdrantv1alpha1.AddToScheme(scheme); err != nil {
		return nil, err
	}
	return scheme, nil
}

// eventually polls condition until it reports done, failing the test with its last message.
func eventually(t *testing.T, condition func() (bool, string)) {
	t.Helper()
	deadline := time.Now().Add(settleTimeout)
	message := ""
	for time.Now().Before(deadline) {
		done, latest := condition()
		if done {
			return
		}
		message = latest
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s: %s", settleTimeout, message)
}

// consistently fails the test if condition stops holding during window.
func consistently(t *testing.T, window time.Duration, condition func() (bool, string)) {
	t.Helper()
	deadline := time.Now().Add(window)
	for time.Now().Before(deadline) {
		held, message := condition()
		if !held {
			t.Fatalf("condition broke: %s", message)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func fetch[T client.Object](t *testing.T, w *world, name string, object T) T {
	t.Helper()
	if err := w.Direct.Get(context.Background(), client.ObjectKey{Namespace: w.Namespace, Name: name}, object); err != nil {
		t.Fatalf("get %s: %v", name, err)
	}
	return object
}

func create(t *testing.T, w *world, object client.Object) {
	t.Helper()
	object.SetNamespace(w.Namespace)
	if err := w.Direct.Create(context.Background(), object); err != nil {
		t.Fatalf("create %s: %v", object.GetName(), err)
	}
}

func clusterObject(name string, spec qdrantv1alpha1.QdrantClusterSpec) *qdrantv1alpha1.QdrantCluster {
	return &qdrantv1alpha1.QdrantCluster{ObjectMeta: metav1.ObjectMeta{Name: name}, Spec: spec}
}

func serviceURL(cluster, namespace string) string {
	return fmt.Sprintf("http://qdrant-%s.%s.svc.cluster.local:6333", cluster, namespace)
}

func nodeURL(index int, cluster, namespace string) string {
	return fmt.Sprintf("http://qdrant-%s-%d.qdrant-%s-headless.%s.svc.cluster.local:6333", cluster, index, cluster, namespace)
}
