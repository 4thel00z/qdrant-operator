package admission_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	qdrantv1alpha1 "github.com/4thel00z/qdrant-operator/api/v1alpha1"
)

var (
	startOnce   sync.Once
	restConfig  *rest.Config
	startErr    error
	environment *envtest.Environment
	names       atomic.Int64
)

func TestMain(m *testing.M) {
	code := m.Run()
	if environment != nil {
		_ = environment.Stop()
	}
	os.Exit(code)
}

type world struct {
	client    client.Client
	namespace string
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

func startWorld(t *testing.T) *world {
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
	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "admission-"}}
	if err := direct.Create(context.Background(), namespace); err != nil {
		t.Fatalf("create namespace: %v", err)
	}
	return &world{client: direct, namespace: namespace.Name}
}

func (w *world) meta() metav1.ObjectMeta {
	return metav1.ObjectMeta{Name: fmt.Sprintf("object-%d", names.Add(1)), Namespace: w.namespace}
}

func (w *world) dryCreate(object client.Object) error {
	return w.client.Create(context.Background(), object, client.DryRunAll)
}

func dryUpdate[T client.Object](w *world, base T, change func(T)) error {
	if err := w.client.Create(context.Background(), base); err != nil {
		return fmt.Errorf("create base object: %w", err)
	}
	changed, ok := base.DeepCopyObject().(T)
	if !ok {
		return fmt.Errorf("deep copy of %T changed its type", base)
	}
	change(changed)
	return w.client.Update(context.Background(), changed, client.DryRunAll)
}

func isInvalid(err error) bool {
	return err != nil && apierrors.IsInvalid(err)
}

func ptr[T any](value T) *T {
	return &value
}
