// Ref: the upstream chart that renders a chart key's values, pinned, as its
// repository publishes it.
package chart

import (
	"fmt"
	"path"
	"regexp"
	"strings"
)

// Ref: an upstream chart as its repository publishes it: what renders
// the values of a chart key (a Flux HelmRelease, an Argo CD source), and
// what the chart cache holds its defaults and schema under.
type Ref struct {
	Repository string `yaml:"repository" json:"repository"` // https://... or oci://...
	Chart      string `yaml:"chart" json:"chart"`           // the chart's name in the repository
	Version    string `yaml:"version" json:"version"`       // a pinned version
}

// chartVersion: a pinned chart version, SemVer as Helm requires it.
var chartVersion = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)

// Check: a repository URL, the name of a chart, a pinned version. where
// names the reference in the errors.
func (r Ref) Check(where string) error {
	switch {
	case r == Ref{}:
		return fmt.Errorf("%s is required: the upstream chart (repository, chart, version)", where)
	case !strings.HasPrefix(r.Repository, "https://") && !strings.HasPrefix(r.Repository, "http://") && !strings.HasPrefix(r.Repository, "oci://"):
		return fmt.Errorf("%s.repository %q: the URL of a chart repository (https://... or oci://...)", where, r.Repository)
	case r.Chart == "" || r.Chart != path.Base(r.Chart) || r.Chart == "." || r.Chart == ".." || strings.TrimSpace(r.Chart) != r.Chart:
		return fmt.Errorf("%s.chart %q: the name of a chart of the repository", where, r.Chart)
	case !chartVersion.MatchString(r.Version):
		return fmt.Errorf("%s.version %q: a pinned chart version (1.2.3), not a range", where, r.Version)
	}
	return nil
}

// id: <chart>-<version>, the chart's name in the chart cache and in the
// errors.
func (r Ref) id() string {
	return r.Chart + "-" + r.Version
}

// pull: the helm command that downloads the chart.
func (r Ref) pull() string {
	if strings.HasPrefix(r.Repository, "oci://") {
		return fmt.Sprintf("helm pull %s/%s --version %s", strings.TrimSuffix(r.Repository, "/"), r.Chart, r.Version)
	}
	return fmt.Sprintf("helm pull %s --repo %s --version %s", r.Chart, r.Repository, r.Version)
}
