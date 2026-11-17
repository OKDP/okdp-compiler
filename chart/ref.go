package chart

import (
	"cmp"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"strings"
)

// Ref is an upstream chart as its repository publishes it. It renders the
// values of a chart key, and it is where the chart cache keeps the chart's
// defaults and schema (Dir).
type Ref struct {
	Repository string `yaml:"repository" json:"repository"` // https://... or oci://...
	Chart      string `yaml:"chart" json:"chart"`           // the chart's name in the repository
	Version    string `yaml:"version" json:"version"`       // a pinned version
}

// chartVersion matches a pinned chart version: SemVer, as Helm requires.
var chartVersion = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)

// IsVersion reports whether v is a pinned chart version (SemVer).
func IsVersion(v string) bool {
	return chartVersion.MatchString(v)
}

// CompareVersions returns -1, 0 or +1 when a precedes, equals or follows b
// in SemVer precedence (semver.org, section 11). Both must be versions
// IsVersion accepts. Build metadata is ignored.
func CompareVersions(a, b string) int {
	pa, pb := splitVersion(a), splitVersion(b)
	for i := range 3 {
		if c := compareNumbers(pa.core[i], pb.core[i]); c != 0 {
			return c
		}
	}
	switch {
	case pa.pre == nil && pb.pre == nil:
		return 0
	case pa.pre == nil:
		return 1
	case pb.pre == nil:
		return -1
	}
	for i := 0; i < len(pa.pre) && i < len(pb.pre); i++ {
		x, y := pa.pre[i], pb.pre[i]
		nx, ny := isNumber(x), isNumber(y)
		var c int
		switch {
		case nx && ny:
			c = compareNumbers(x, y)
		case nx:
			c = -1
		case ny:
			c = 1
		default:
			c = strings.Compare(x, y)
		}
		if c != 0 {
			return c
		}
	}
	return cmp.Compare(len(pa.pre), len(pb.pre))
}

type version struct {
	core [3]string // major, minor, patch
	pre  []string  // the pre-release identifiers, nil when none
}

func splitVersion(v string) version {
	v, _, _ = strings.Cut(strings.TrimPrefix(v, "v"), "+")
	core, pre, hasPre := strings.Cut(v, "-")
	var out version
	copy(out.core[:], strings.SplitN(core, ".", 3))
	if hasPre {
		out.pre = strings.Split(pre, ".")
	}
	return out
}

func isNumber(s string) bool {
	return s != "" && strings.Trim(s, "0123456789") == ""
}

// compareNumbers compares by value two digit strings of any length.
func compareNumbers(a, b string) int {
	a, b = strings.TrimLeft(a, "0"), strings.TrimLeft(b, "0")
	if c := cmp.Compare(len(a), len(b)); c != 0 {
		return c
	}
	return strings.Compare(a, b)
}

// Check verifies the reference: a repository URL that names a directory of
// the chart cache (CacheDir), a chart name and a pinned version. http://
// is refused because the chart would be fetched in clear. where names the
// reference in the errors.
func (r Ref) Check(where string) error {
	switch {
	case r == Ref{}:
		return fmt.Errorf("%s is required: the upstream chart (repository, chart, version)", where)
	case strings.HasPrefix(r.Repository, "http://"):
		return fmt.Errorf("%s.repository %q: http://, the chart fetched in clear: the URL of a chart repository (https://... or oci://...)", where, r.Repository)
	case r.CacheDir() == "":
		return fmt.Errorf("%s.repository %q: the URL of a chart repository (https://... or oci://...)", where, r.Repository)
	case !isChartName(r.Chart):
		return fmt.Errorf("%s.chart %q: the name of a chart of the repository", where, r.Chart)
	case !chartVersion.MatchString(r.Version):
		return fmt.Errorf("%s.version %q: a pinned chart version (1.2.3), not a range", where, r.Version)
	}
	return nil
}

// isChartName reports whether name can name a chart and its directory in
// the chart cache: one path element, without a space at either end.
func isChartName(name string) bool {
	return name != "" && name == path.Base(name) && name != "." && name != ".." && strings.TrimSpace(name) == name
}

// IsFileName reports whether s is one element of a path: a file or
// directory name, not . or .., without a / or a \.
func IsFileName(s string) bool {
	return fs.ValidPath(s) && s != "." && !strings.ContainsAny(s, `/\`)
}

// ID returns <chart>-<version>, as the chart cache and the errors name it.
func (r Ref) ID() string {
	return r.Chart + "-" + r.Version
}

// repoDir returns the directory of the chart cache that holds the charts
// of the repository: the URL without its scheme and trailing /
// (quay.io/adaltas for oci://quay.io/adaltas). It returns "" when the
// scheme is not https:// or oci://, or when the rest is not a path inside
// the chart cache (a segment that is empty, . or .., or holds \, ? or #).
// http:// is accepted only when http is true.
func (r Ref) repoDir(http bool) string {
	schemes := []string{"oci://", "https://"}
	if http {
		schemes = append(schemes, "http://")
	}
	for _, scheme := range schemes {
		if rest, ok := strings.CutPrefix(r.Repository, scheme); ok {
			dir := strings.TrimSuffix(rest, "/")
			for segment := range strings.SplitSeq(dir, "/") {
				if segment == "" || segment == "." || segment == ".." || strings.ContainsAny(segment, `\?#`) {
					return ""
				}
			}
			return dir
		}
	}
	return ""
}

// CacheDir returns the chart cache directory of the repository, for a
// chart that is fetched, or "" when it has none. http:// has none: a
// chart is not fetched in clear.
func (r Ref) CacheDir() string { return r.repoDir(false) }

// Dir returns the directory of the chart in the chart cache, with slashes:
//
//	<repository without scheme>/<chart>-<version>/<chart>
//
// Unlike CacheDir, Dir gives http:// a directory, for a cache that pulls
// from a host the platform trusts (RepositoryPuller.InsecureHosts). Check
// still refuses http:// in a package. Dir fails when the repository, the
// chart or the version cannot be a directory of the cache.
func (r Ref) Dir() (string, error) {
	repo := r.repoDir(true)
	if repo == "" {
		return "", fmt.Errorf("repository %q is no directory of the chart cache", r.Repository)
	}
	for _, s := range []string{r.Chart, r.Version} {
		if s == "" || strings.ContainsAny(s, `/\?#`) || s == "." || s == ".." {
			return "", fmt.Errorf("chart %q %q is no directory of the chart cache", r.Chart, r.Version)
		}
	}
	return path.Join(repo, r.ID(), r.Chart), nil
}

// Pull returns the command that downloads the chart and unpacks it into
// its entry of the chart cache. helm pull --untar creates the <chart>
// directory itself.
func (r Ref) Pull() string {
	to := path.Join("<chart cache>", r.CacheDir(), r.ID())
	if strings.HasPrefix(r.Repository, "oci://") {
		return fmt.Sprintf("helm pull %s/%s --version %s --untar -d %s", strings.TrimSuffix(r.Repository, "/"), r.Chart, r.Version, to)
	}
	return fmt.Sprintf("helm pull %s --repo %s --version %s --untar -d %s", r.Chart, r.Repository, r.Version, to)
}

// dnsLabel matches the characters of a DNS-1123 label.
var dnsLabel = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// IsDNSLabel reports whether s is a DNS-1123 label, the form Kubernetes
// requires of a namespace name: lowercase letters, digits and "-", a
// letter or digit at each end, 63 characters at most.
func IsDNSLabel(s string) bool {
	return len(s) <= 63 && dnsLabel.MatchString(s)
}

// IsClaimID reports whether s is a claim ID: DNS-1123 labels joined by
// dots, 63 characters at most. Such an ID is a Kubernetes name (a DNS
// subdomain), and the dots separate its parts.
func IsClaimID(s string) bool {
	if len(s) > 63 {
		return false
	}
	for l := range strings.SplitSeq(s, ".") {
		if !dnsLabel.MatchString(l) {
			return false
		}
	}
	return true
}

// ReleaseMax is the longest release name Helm accepts. It leaves room for
// the suffixes of the names derived from the release.
const ReleaseMax = 53

// IsRelease reports whether name is a release name: a DNS-1123 label of
// ReleaseMax characters at most.
func IsRelease(name string) bool {
	return len(name) <= ReleaseMax && dnsLabel.MatchString(name)
}
