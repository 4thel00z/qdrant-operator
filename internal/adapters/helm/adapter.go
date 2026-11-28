package helm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"helm.sh/helm/v4/pkg/action"
	helmchart "helm.sh/helm/v4/pkg/chart"
	"helm.sh/helm/v4/pkg/chart/loader"
	"helm.sh/helm/v4/pkg/cli"
	"helm.sh/helm/v4/pkg/kube"
	"helm.sh/helm/v4/pkg/registry"
	releasecommon "helm.sh/helm/v4/pkg/release/common"
	release "helm.sh/helm/v4/pkg/release/v1"
	"helm.sh/helm/v4/pkg/storage/driver"
	"k8s.io/client-go/rest"

	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/ports"
	"github.com/4thel00z/qdrant-operator/internal/settings"
)

const (
	storageDriver = "secrets"
	maxHistory    = 10
)

// Adapter drives Helm in process. Every call builds its own action
// configuration, so releases can be applied concurrently.
type Adapter struct {
	source  chartSource
	timeout time.Duration
	cache   chartCache
	rest    *rest.Config
}

var _ ports.Helm = (*Adapter)(nil)

func New(cfg settings.Helm, config *rest.Config) *Adapter {
	return &Adapter{
		source:  newChartSource(cfg.Chart, cfg.Repo),
		timeout: cfg.Timeout,
		cache:   chartCache{dir: cfg.CacheDir},
		rest:    config,
	}
}

func (a *Adapter) Apply(ctx context.Context, request ports.ReleaseRequest) error {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()
	target := fmt.Sprintf("%s/%s", request.Namespace, request.Name)

	values, err := normalizeValues(request.Values)
	if err != nil {
		return fmt.Errorf("helm apply %s: values: %w", target, err)
	}
	cfg, err := a.configuration(request.Namespace)
	if err != nil {
		return fmt.Errorf("helm apply %s: %w", target, err)
	}
	if err := a.recoverPending(cfg, request.Name); err != nil {
		return fmt.Errorf("helm apply %s: recover pending release: %w", target, err)
	}
	last, found, err := lastRelease(cfg, request.Name)
	if err != nil {
		return fmt.Errorf("helm apply %s: %w", target, err)
	}

	upgrade := action.NewUpgrade(cfg)
	registryClient, err := a.registryClient()
	if err != nil {
		return fmt.Errorf("helm apply %s: %w", target, err)
	}
	upgrade.SetRegistryClient(registryClient)
	upgrade.RepoURL = a.source.RepoURL
	upgrade.Version = request.ChartVersion
	chart, err := a.loadChart(upgrade.LocateChart, request.ChartVersion)
	if err != nil {
		return fmt.Errorf("helm apply %s: chart %s %s: %w", target, a.source.Reference, request.ChartVersion, err)
	}
	renderer := PartitionPinner{StatefulSet: request.StatefulSet, Partition: request.Partition}

	if !found || last.Info.Status == releasecommon.StatusUninstalled {
		install := action.NewInstall(cfg)
		install.ChartPathOptions = upgrade.ChartPathOptions
		install.ReleaseName = request.Name
		install.Namespace = request.Namespace
		install.Timeout = a.timeout
		install.WaitStrategy = kube.HookOnlyStrategy
		install.HideNotes = true
		install.Replace = found
		install.PostRenderer = renderer
		if _, err := install.RunWithContext(ctx, chart, values); err != nil {
			return fmt.Errorf("helm install %s: %w", target, err)
		}
		return nil
	}

	upgrade.Namespace = request.Namespace
	upgrade.Timeout = a.timeout
	upgrade.WaitStrategy = kube.HookOnlyStrategy
	upgrade.HideNotes = true
	upgrade.MaxHistory = maxHistory
	upgrade.PostRenderer = renderer
	if _, err := upgrade.RunWithContext(ctx, request.Name, chart, values); err != nil {
		return fmt.Errorf("helm upgrade %s: %w", target, err)
	}
	return nil
}

func (a *Adapter) Uninstall(ctx context.Context, name, namespace string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	target := fmt.Sprintf("%s/%s", namespace, name)

	cfg, err := a.configuration(namespace)
	if err != nil {
		return fmt.Errorf("helm uninstall %s: %w", target, err)
	}
	if err := a.uninstall(cfg, name); err != nil {
		return fmt.Errorf("helm uninstall %s: %w", target, err)
	}
	return nil
}

func (a *Adapter) ReleaseExists(ctx context.Context, name, namespace string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	cfg, err := a.configuration(namespace)
	if err != nil {
		return false, fmt.Errorf("helm status %s/%s: %w", namespace, name, err)
	}
	_, found, err := lastRelease(cfg, name)
	if err != nil {
		return false, fmt.Errorf("helm status %s/%s: %w", namespace, name, err)
	}
	return found, nil
}

func (a *Adapter) uninstall(cfg *action.Configuration, name string) error {
	uninstall := action.NewUninstall(cfg)
	uninstall.Timeout = a.timeout
	uninstall.WaitStrategy = kube.StatusWatcherStrategy
	_, err := uninstall.Run(name)
	return err
}

// recoverPending undoes a release another Helm process left mid-operation;
// Helm refuses to touch a release in a pending state.
func (a *Adapter) recoverPending(cfg *action.Configuration, name string) error {
	last, found, err := lastRelease(cfg, name)
	if err != nil || !found || !last.Info.Status.IsPending() {
		return err
	}
	if last.Version == 1 {
		return a.uninstall(cfg, name)
	}
	rollback := action.NewRollback(cfg)
	rollback.Timeout = a.timeout
	rollback.MaxHistory = maxHistory
	return rollback.Run(name)
}

func (a *Adapter) configuration(namespace string) (*action.Configuration, error) {
	cfg := &action.Configuration{}
	if err := cfg.Init(newRESTGetter(a.rest, namespace), namespace, storageDriver); err != nil {
		return nil, fmt.Errorf("configure helm: %w", err)
	}
	return cfg, nil
}

func (a *Adapter) envSettings() *cli.EnvSettings {
	env := cli.New()
	env.RepositoryCache = filepath.Join(a.cache.dir, "repository")
	env.ContentCache = filepath.Join(a.cache.dir, "content")
	env.RepositoryConfig = filepath.Join(a.cache.dir, "repositories.yaml")
	env.RegistryConfig = filepath.Join(a.cache.dir, "registry.json")
	return env
}

func (a *Adapter) registryClient() (*registry.Client, error) {
	client, err := registry.NewClient(
		registry.ClientOptCredentialsFile(a.envSettings().RegistryConfig),
		registry.ClientOptEnableCache(true),
	)
	if err != nil {
		return nil, fmt.Errorf("registry client: %w", err)
	}
	return client, nil
}

func (a *Adapter) loadChart(locate locateFunc, version string) (helmchart.Charter, error) {
	archive, err := a.cache.fetch(a.source, version, a.envSettings(), locate)
	if err != nil {
		return nil, err
	}
	return loader.Load(archive)
}

// lastRelease returns the highest revision of a release.
func lastRelease(cfg *action.Configuration, name string) (*release.Release, bool, error) {
	history, err := action.NewHistory(cfg).Run(name)
	if errors.Is(err, driver.ErrReleaseNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var last *release.Release
	for _, entry := range history {
		revision, ok := entry.(*release.Release)
		if !ok {
			return nil, false, fmt.Errorf("unexpected release type %T", entry)
		}
		if last == nil || revision.Version > last.Version {
			last = revision
		}
	}
	return last, last != nil, nil
}

func normalizeValues(values domain.JSON) (map[string]any, error) {
	encoded, err := json.Marshal(values)
	if err != nil {
		return nil, err
	}
	var normalized map[string]any
	if err := json.Unmarshal(encoded, &normalized); err != nil {
		return nil, err
	}
	if normalized == nil {
		return map[string]any{}, nil
	}
	return normalized, nil
}
