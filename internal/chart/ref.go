// Ref: the upstream chart that renders a chart key's values, pinned, as its
// repository publishes it.
package chart

import (
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"strings"
)

// Ref: an upstream chart as its repository publishes it: what renders
// the values of a chart key (a Flux HelmRelease, an Argo CD source), and
// what the chart cache holds its defaults and schema under
// (<repository>/<chart>-<version>/<chart>, cacheDir).
type Ref struct {
	Repository string `yaml:"repository" json:"repository"` // https://... or oci://...
	Chart      string `yaml:"chart" json:"chart"`           // the chart's name in the repository
	Version    string `yaml:"version" json:"version"`       // a pinned version
}

// chartVersion: a pinned chart version, SemVer as Helm requires it.
var chartVersion = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)

// IsVersion: v is a pinned chart version, SemVer as Helm requires it.
func IsVersion(v string) bool {
	return chartVersion.MatchString(v)
}

// Check: a repository URL that names a directory of the chart cache
// (cacheDir), the name of a chart, a pinned version. where names the
// reference in the errors.
func (r Ref) Check(where string) error {
	switch {
	case r == Ref{}:
		return fmt.Errorf("%s is required: the upstream chart (repository, chart, version)", where)
	case r.cacheDir() == "":
		return fmt.Errorf("%s.repository %q: the URL of a chart repository (https://... or oci://...)", where, r.Repository)
	case !isChartName(r.Chart):
		return fmt.Errorf("%s.chart %q: the name of a chart of the repository", where, r.Chart)
	case !chartVersion.MatchString(r.Version):
		return fmt.Errorf("%s.version %q: a pinned chart version (1.2.3), not a range", where, r.Version)
	}
	return nil
}

// isChartName: name may name a chart of a repository, and its directory
// in the chart cache: not empty, one path element, not . or .., without
// spaces at either end.
func isChartName(name string) bool {
	return name != "" && name == path.Base(name) && name != "." && name != ".." && strings.TrimSpace(name) == name
}

// id: <chart>-<version>, the chart's name in the chart cache and in the
// errors.
func (r Ref) id() string {
	return r.Chart + "-" + r.Version
}

// cacheDir: the directory of the chart cache that holds the charts of the
// repository: its URL without the scheme and a trailing /, host, port and
// path kept (quay.io/adaltas for oci://quay.io/adaltas). "" for a
// repository that is not https://, http:// or oci://, or whose rest is not
// a path inside the chart cache (fs.ValidPath, without \): an empty host, an
// empty, . or .. segment. Check refuses such a repository.
func (r Ref) cacheDir() string {
	for _, scheme := range []string{"oci://", "https://", "http://"} {
		if rest, ok := strings.CutPrefix(r.Repository, scheme); ok {
			dir := strings.TrimSuffix(rest, "/")
			if dir == "." || !fs.ValidPath(dir) || strings.Contains(dir, `\`) {
				return ""
			}
			return dir
		}
	}
	return ""
}

// pull: the command that downloads the chart and unpacks it into its entry
// of the chart cache, <repository>/<chart>-<version>/<chart> (helm pull
// --untar creates the directory).
func (r Ref) pull() string {
	to := path.Join("<chart cache>", r.cacheDir(), r.id())
	if strings.HasPrefix(r.Repository, "oci://") {
		return fmt.Sprintf("helm pull %s/%s --version %s --untar -d %s", strings.TrimSuffix(r.Repository, "/"), r.Chart, r.Version, to)
	}
	return fmt.Sprintf("helm pull %s --repo %s --version %s --untar -d %s", r.Chart, r.Repository, r.Version, to)
}

// dnsLabel: the characters of a DNS-1123 label: lowercase letters, digits
// and "-", a letter or digit at each end.
var dnsLabel = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// IsDNSLabel: s is a DNS-1123 label, as Kubernetes names a namespace:
// lowercase letters, digits and "-", a letter or digit at each end, 63
// characters at most.
func IsDNSLabel(s string) bool {
	return len(s) <= 63 && dnsLabel.MatchString(s)
}

// ReleaseMax: the longest release name Helm accepts, leaving room for the
// suffixes of the names derived from it.
const ReleaseMax = 53

// IsRelease: name is a release name, as Helm accepts it and OKDP derives
// object names from it (<release>-<suffix>, Secrets): a DNS-1123 label of
// ReleaseMax characters at most.
func IsRelease(name string) bool {
	return len(name) <= ReleaseMax && dnsLabel.MatchString(name)
}
