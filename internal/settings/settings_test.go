package settings_test

import (
	"slices"
	"testing"
	"time"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	"github.com/4thel00z/qdrant-operator/internal/settings"
)

func load(t *testing.T, args []string) (settings.Settings, error) {
	t.Helper()
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	v := viper.New()
	if err := settings.RegisterFlags(flags, v); err != nil {
		t.Fatalf("register flags: %v", err)
	}
	if err := flags.Parse(args); err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	return settings.Load(v)
}

func TestDefaults(t *testing.T) {
	got, err := load(t, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Helm.Timeout != 10*time.Minute || got.Helm.Repo != "https://qdrant.github.io/qdrant-helm" {
		t.Errorf("helm defaults: %+v", got.Helm)
	}
	if got.Manager.MaxConcurrentOneShots != 4 || got.Manager.MaxConcurrentLevels != 4 {
		t.Errorf("concurrency defaults: %+v", got.Manager)
	}
	if got.Logging.Format != "json" || got.Metrics.Address != ":9090" || len(got.Manager.Namespaces) != 0 {
		t.Errorf("defaults: %+v", got)
	}
}

func TestEnvironmentOverridesDefaults(t *testing.T) {
	t.Setenv("QDRANT_OPERATOR_HELM_TIMEOUT", "90s")
	t.Setenv("QDRANT_OPERATOR_MANAGER_NAMESPACES", " team-a, ,team-b ")
	t.Setenv("QDRANT_OPERATOR_MANAGER_LEADER_ELECTION", "true")
	t.Setenv("QDRANT_OPERATOR_LOGGING_FORMAT", "text")
	got, err := load(t, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Helm.Timeout != 90*time.Second {
		t.Errorf("helm timeout: %s", got.Helm.Timeout)
	}
	if !slices.Equal(got.Manager.Namespaces, []string{"team-a", "team-b"}) {
		t.Errorf("namespaces: %q", got.Manager.Namespaces)
	}
	if !got.Manager.LeaderElection || got.Logging.Format != "text" {
		t.Errorf("manager/logging: %+v %+v", got.Manager, got.Logging)
	}
}

func TestFlagsOverrideEnvironment(t *testing.T) {
	t.Setenv("QDRANT_OPERATOR_QDRANT_TIMEOUT", "5s")
	got, err := load(t, []string{"--qdrant-timeout=7s", "--manager-namespaces=a,b"})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Qdrant.Timeout != 7*time.Second {
		t.Errorf("qdrant timeout: %s", got.Qdrant.Timeout)
	}
	if !slices.Equal(got.Manager.Namespaces, []string{"a", "b"}) {
		t.Errorf("namespaces: %q", got.Manager.Namespaces)
	}
}

func TestRejectsInvalidValues(t *testing.T) {
	cases := map[string][]string{
		"log format":   {"--logging-format=xml"},
		"one-shots":    {"--manager-max-concurrent-one-shots=0"},
		"helm timeout": {"--helm-timeout=0s"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := load(t, args); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}
