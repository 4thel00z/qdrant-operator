package kubernetes_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgo "k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	adapter "github.com/4thel00z/qdrant-operator/internal/adapters/kubernetes"
)

type cluster struct {
	direct  client.Client
	adapter *adapter.Adapter
}

var (
	startOnce   sync.Once
	started     *cluster
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

func startCluster(t *testing.T) *cluster {
	t.Helper()
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS is unset; run: KUBEBUILDER_ASSETS=$(go run sigs.k8s.io/controller-runtime/tools/setup-envtest@release-0.22 use 1.37.0 -p path) go test ./internal/adapters/kubernetes/")
	}
	startOnce.Do(func() {
		started, startErr = launch()
	})
	if startErr != nil {
		t.Fatalf("start envtest: %v", startErr)
	}
	return started
}

func launch() (*cluster, error) {
	environment = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "..", "manifests", "crds"), "testdata"},
		ErrorIfCRDPathMissing: true,
	}
	config, err := environment.Start()
	if err != nil {
		return nil, err
	}
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		return nil, err
	}
	direct, err := client.New(config, client.Options{Scheme: scheme})
	if err != nil {
		return nil, err
	}
	logs, err := clientgo.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	return &cluster{direct: direct, adapter: adapter.New(direct, direct, logs)}, nil
}

func newNamespace(t *testing.T, env *cluster) string {
	t.Helper()
	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "adapter-"}}
	if err := env.direct.Create(context.Background(), namespace); err != nil {
		t.Fatalf("create namespace: %v", err)
	}
	return namespace.Name
}

func check(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func equal[T comparable](t *testing.T, want, got T) {
	t.Helper()
	if want != got {
		t.Fatalf("want %v, got %v", want, got)
	}
}
