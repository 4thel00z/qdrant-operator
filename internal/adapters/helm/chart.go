package helm

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"helm.sh/helm/v4/pkg/cli"
)

const ociScheme = "oci://"

var exactVersion = regexp.MustCompile(`^v?\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)

// chartSource names a chart for Helm. An OCI repository carries the chart in
// Reference and has no RepoURL; an HTTP repository keeps them apart.
type chartSource struct {
	Reference string
	RepoURL   string
}

func newChartSource(chart, repo string) chartSource {
	if strings.HasPrefix(repo, ociScheme) {
		return chartSource{Reference: strings.TrimRight(repo, "/") + "/" + chart}
	}
	return chartSource{Reference: chart, RepoURL: repo}
}

func (s chartSource) remote() bool {
	return s.RepoURL != "" || strings.HasPrefix(s.Reference, ociScheme)
}

type locateFunc func(reference string, settings *cli.EnvSettings) (string, error)

// chartCache keeps one archive per exact chart version under dir.
type chartCache struct {
	dir string
}

func (c chartCache) archivePath(source chartSource, version string) string {
	sum := sha256.Sum256([]byte(source.RepoURL + "\n" + source.Reference))
	name := path.Base(source.Reference) + "-" + version + ".tgz"
	return filepath.Join(c.dir, "charts", hex.EncodeToString(sum[:8]), name)
}

func cacheable(source chartSource, version string) bool {
	return source.remote() && exactVersion.MatchString(version)
}

// fetch returns the local path of the chart archive, downloading through
// locate only when the version is not cached yet.
func (c chartCache) fetch(source chartSource, version string, settings *cli.EnvSettings, locate locateFunc) (string, error) {
	if !cacheable(source, version) {
		return locate(source.Reference, settings)
	}
	cached := c.archivePath(source, version)
	if _, err := os.Stat(cached); err == nil {
		return cached, nil
	}
	downloaded, err := locate(source.Reference, settings)
	if err != nil {
		return "", err
	}
	if err := copyAtomically(downloaded, cached); err != nil {
		return "", fmt.Errorf("cache chart %s %s: %w", source.Reference, version, err)
	}
	return cached, nil
}

func copyAtomically(from, to string) error {
	content, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	staged, err := os.CreateTemp(filepath.Dir(to), ".partial-*")
	if err != nil {
		return err
	}
	defer os.Remove(staged.Name())
	if _, err := staged.Write(content); err != nil {
		staged.Close()
		return err
	}
	if err := staged.Close(); err != nil {
		return err
	}
	return os.Rename(staged.Name(), to)
}
