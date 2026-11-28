// Package settings reads the operator configuration once at startup from flags and
// QDRANT_OPERATOR_<SECTION>_<KEY> environment variables, flags taking precedence.
package settings

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

const EnvPrefix = "QDRANT_OPERATOR"

var logFormats = []string{"json", "text"}

type Settings struct {
	Logging    Logging    `mapstructure:"logging"`
	Manager    Manager    `mapstructure:"manager"`
	Metrics    Metrics    `mapstructure:"metrics"`
	Kubernetes Kubernetes `mapstructure:"kubernetes"`
	Helm       Helm       `mapstructure:"helm"`
	Qdrant     Qdrant     `mapstructure:"qdrant"`
}

type Logging struct {
	Level  string `mapstructure:"level"`
	Format string `mapstructure:"format"`
}

// Manager configures the controller-runtime manager. Empty Namespaces watches every namespace.
type Manager struct {
	Namespaces            []string      `mapstructure:"namespaces"`
	LeaderElection        bool          `mapstructure:"leader-election"`
	LeaderElectionID      string        `mapstructure:"leader-election-id"`
	LeaseDuration         time.Duration `mapstructure:"lease-duration"`
	HealthProbeAddress    string        `mapstructure:"health-probe-address"`
	MaxConcurrentOneShots int           `mapstructure:"max-concurrent-one-shots"`
}

// Metrics serves Prometheus metrics on Address; an empty Address disables the endpoint.
type Metrics struct {
	Address string `mapstructure:"address"`
}

// Kubernetes uses in-cluster config when Kubeconfig is empty, falling back to the default kubeconfig.
type Kubernetes struct {
	Kubeconfig string `mapstructure:"kubeconfig"`
}

// Helm keeps downloaded charts under CacheDir.
type Helm struct {
	Chart    string        `mapstructure:"chart"`
	Repo     string        `mapstructure:"repo"`
	Timeout  time.Duration `mapstructure:"timeout"`
	CacheDir string        `mapstructure:"cache-dir"`
}

type Qdrant struct {
	Timeout         time.Duration `mapstructure:"timeout"`
	SnapshotTimeout time.Duration `mapstructure:"snapshot-timeout"`
}

// RegisterFlags declares every setting as a flag and binds it, and its environment variable, into v.
func RegisterFlags(flags *pflag.FlagSet, v *viper.Viper) error {
	flags.String("logging-level", "info", "log level: debug, info, warn, error")
	flags.String("logging-format", "json", "log format: json or text")
	flags.StringSlice("manager-namespaces", nil, "namespaces to watch; empty watches all")
	flags.Bool("manager-leader-election", false, "elect a leader so several replicas can run")
	flags.String("manager-leader-election-id", "qdrant-operator.qdrant.io", "name of the leader election Lease")
	flags.Duration("manager-lease-duration", 15*time.Second, "leader election lease duration")
	flags.String("manager-health-probe-address", ":8080", "address of the /healthz and /readyz endpoints")
	flags.Int("manager-max-concurrent-one-shots", 4, "parallel backups, restores and migrations per kind")
	flags.String("metrics-address", ":9090", "address of the Prometheus endpoint; empty disables it")
	flags.String("kubernetes-kubeconfig", "", "kubeconfig path; empty uses in-cluster config")
	flags.String("helm-chart", "qdrant", "Qdrant chart name")
	flags.String("helm-repo", "https://qdrant.github.io/qdrant-helm", "Qdrant chart repository (https:// or oci://)")
	flags.Duration("helm-timeout", 10*time.Minute, "timeout of one Helm install or upgrade")
	flags.String("helm-cache-dir", "/tmp/helm", "directory for downloaded charts")
	flags.Duration("qdrant-timeout", 30*time.Second, "timeout of one Qdrant API call")
	flags.Duration("qdrant-snapshot-timeout", time.Hour, "timeout of one Qdrant snapshot create, upload or recover")

	v.SetEnvPrefix(EnvPrefix)
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))
	v.AutomaticEnv()

	var bindErr error
	flags.VisitAll(func(flag *pflag.Flag) {
		if bindErr != nil {
			return
		}
		bindErr = v.BindPFlag(key(flag.Name), flag)
	})
	return bindErr
}

// Load decodes the bound flags and environment into Settings and validates them.
func Load(v *viper.Viper) (Settings, error) {
	settings := Settings{}
	hooks := viper.DecodeHook(mapstructure.ComposeDecodeHookFunc(
		mapstructure.StringToTimeDurationHookFunc(),
		mapstructure.StringToSliceHookFunc(","),
	))
	if err := v.Unmarshal(&settings, hooks); err != nil {
		return Settings{}, fmt.Errorf("decode settings: %w", err)
	}
	settings.Manager.Namespaces = trimmed(settings.Manager.Namespaces)
	return settings, settings.Validate()
}

func (s Settings) Validate() error {
	if !slices.Contains(logFormats, s.Logging.Format) {
		return fmt.Errorf("logging format %q: want one of %v", s.Logging.Format, logFormats)
	}
	if s.Manager.MaxConcurrentOneShots < 1 {
		return fmt.Errorf("manager max-concurrent-one-shots %d: want at least 1", s.Manager.MaxConcurrentOneShots)
	}
	if s.Helm.Timeout <= 0 {
		return fmt.Errorf("helm timeout %s: want a positive duration", s.Helm.Timeout)
	}
	return nil
}

func key(flagName string) string {
	section, rest, _ := strings.Cut(flagName, "-")
	return section + "." + rest
}

func trimmed(values []string) []string {
	result := []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		result = append(result, value)
	}
	return result
}
