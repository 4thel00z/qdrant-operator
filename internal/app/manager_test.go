package app_test

import (
	"bytes"
	"strings"
	"testing"

	qdrantv1alpha1 "github.com/4thel00z/qdrant-operator/api/v1alpha1"
	"github.com/4thel00z/qdrant-operator/internal/app"
	"github.com/4thel00z/qdrant-operator/internal/settings"
)

func TestSchemeRegistersEveryKind(t *testing.T) {
	scheme, err := app.NewScheme()
	if err != nil {
		t.Fatalf("new scheme: %v", err)
	}
	kinds := []string{
		"QdrantCluster", "QdrantBackup", "QdrantBackupSchedule", "QdrantRestore",
		"QdrantCollection", "QdrantAccessKey", "QdrantMigration",
	}
	registered := scheme.KnownTypes(qdrantv1alpha1.GroupVersion)
	for _, kind := range kinds {
		if _, ok := registered[kind]; !ok {
			t.Errorf("%s not registered", kind)
		}
		if _, ok := registered[kind+"List"]; !ok {
			t.Errorf("%sList not registered", kind)
		}
	}
}

func TestNamespaceConfigs(t *testing.T) {
	if configs := app.NamespaceConfigs(nil); configs != nil {
		t.Errorf("no namespaces should watch all, got %v", configs)
	}
	configs := app.NamespaceConfigs([]string{"team-a", "team-b"})
	if len(configs) != 2 {
		t.Errorf("want 2 namespaces, got %v", configs)
	}
}

func TestManagerOptionsDisableMetricsOnEmptyAddress(t *testing.T) {
	options := app.ManagerOptions(settings.Settings{}, nil)
	if options.Metrics.BindAddress != "0" {
		t.Errorf("metrics bind address: %q", options.Metrics.BindAddress)
	}
}

func TestLoggerFormats(t *testing.T) {
	output := &bytes.Buffer{}
	logger, err := app.NewLogger(settings.Logging{Level: "info", Format: "json"}, output)
	if err != nil {
		t.Fatalf("new logger: %v", err)
	}
	logger.Info("hello", "key", "value")
	if !strings.Contains(output.String(), `"key":"value"`) {
		t.Errorf("json log: %s", output.String())
	}
	if _, err := app.NewLogger(settings.Logging{Level: "loud", Format: "json"}, output); err == nil {
		t.Error("expected an error for an unknown level")
	}
}
