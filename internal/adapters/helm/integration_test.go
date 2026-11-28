//go:build integration

package helm

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"helm.sh/helm/v4/pkg/chart/common"
	chartv2 "helm.sh/helm/v4/pkg/chart/v2"
	chartutil "helm.sh/helm/v4/pkg/chart/v2/util"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/ports"
	"github.com/4thel00z/qdrant-operator/internal/settings"
)

func TestInstallUpgradeAndUninstallAgainACluster(t *testing.T) {
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		t.Skip("KUBECONFIG not set")
	}
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	chart := &chartv2.Chart{
		Metadata: &chartv2.Metadata{APIVersion: "v2", Name: "tiny", Version: "0.1.0"},
		Templates: []*common.File{{
			Name: "templates/configmap.yaml",
			Data: []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: {{ .Release.Name }}\ndata:\n  value: {{ .Values.value | quote }}\n"),
		}},
	}
	if err := chartutil.SaveDir(chart, parent); err != nil {
		t.Fatal(err)
	}
	adapter := New(settings.Helm{
		Chart:    filepath.Join(parent, "tiny"),
		Timeout:  2 * time.Minute,
		CacheDir: t.TempDir(),
	}, config)
	ctx := context.Background()
	request := ports.ReleaseRequest{
		Name:         "tiny-integration",
		Namespace:    "default",
		ChartVersion: "0.1.0",
		Values:       domain.JSON{"value": "one"},
		StatefulSet:  "absent",
	}

	if err := adapter.Apply(ctx, request); err != nil {
		t.Fatalf("install: %v", err)
	}
	request.Values = domain.JSON{"value": "two"}
	if err := adapter.Apply(ctx, request); err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	exists, err := adapter.ReleaseExists(ctx, request.Name, request.Namespace)
	if err != nil || !exists {
		t.Fatalf("exists = %v, err %v", exists, err)
	}
	if err := adapter.Uninstall(ctx, request.Name, request.Namespace); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	exists, err = adapter.ReleaseExists(ctx, request.Name, request.Namespace)
	if err != nil || exists {
		t.Fatalf("exists after uninstall = %v, err %v", exists, err)
	}
}
