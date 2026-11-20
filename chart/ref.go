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
// values of a chart key. The compiler never fetches it: a host does, to
// check the final values against it (okdp-gitops).
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

// Check verifies the reference: a repository URL whose path is plain
// segments (repoPath), a chart name and a pinned version. http:// is
// refused because the chart would be fetched in clear. where names the
// reference in the errors.
func (r Ref) Check(where string) error {
	switch {
	case r == Ref{}:
		return fmt.Errorf("%s is required: the upstream chart (repository, chart, version)", where)
	case strings.HasPrefix(r.Repository, "http://"):
		return fmt.Errorf("%s.repository %q: http://, the chart fetched in clear: the URL of a chart repository (https://... or oci://...)", where, r.Repository)
	case r.repoPath() == "":
		return fmt.Errorf("%s.repository %q: the URL of a chart repository (https://... or oci://...)", where, r.Repository)
	case !isChartName(r.Chart):
		return fmt.Errorf("%s.chart %q: the name of a chart of the repository", where, r.Chart)
	case !chartVersion.MatchString(r.Version):
		return fmt.Errorf("%s.version %q: a pinned chart version (1.2.3), not a range", where, r.Version)
	}
	return nil
}

// isChartName reports whether name can name a chart: one path element,
// without a space at either end.
func isChartName(name string) bool {
	return name != "" && name == path.Base(name) && name != "." && name != ".." && strings.TrimSpace(name) == name
}

// IsFileName reports whether s is one element of a path: a file or
// directory name, not . or .., without a / or a \.
func IsFileName(s string) bool {
	return fs.ValidPath(s) && s != "." && !strings.ContainsAny(s, `/\`)
}

// ID returns <chart>-<version>, the name of the chart at its version.
func (r Ref) ID() string {
	return r.Chart + "-" + r.Version
}

// repoPath returns the URL of the repository without its scheme and
// trailing / (quay.io/adaltas for oci://quay.io/adaltas): plain path
// segments. It returns "" when the scheme is not https:// or oci://, or
// when the rest is not a relative path (a segment that is empty, . or
// .., or holds \, ? or #).
func (r Ref) repoPath() string {
	for _, scheme := range []string{"oci://", "https://"} {
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

// dnsLabel matches the characters of a DNS-1123 label.
var dnsLabel = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// IsDNSLabel reports whether s is a DNS-1123 label, the form Kubernetes
// requires of a namespace name: lowercase letters, digits and "-", a
// letter or digit at each end, 63 characters at most.
func IsDNSLabel(s string) bool {
	return len(s) <= 63 && dnsLabel.MatchString(s)
}

// ReleaseMax is the longest release name Helm accepts. It leaves room for
// the suffixes of the names derived from the release.
const ReleaseMax = 53

// IsRelease reports whether name is a release name: a DNS-1123 label of
// ReleaseMax characters at most.
func IsRelease(name string) bool {
	return len(name) <= ReleaseMax && dnsLabel.MatchString(name)
}
