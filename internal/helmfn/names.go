// Names: the names Kubernetes and Helm accept, and the names charts derive
// from a release.
package helmfn

import (
	"regexp"
	"strings"
)

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
// object names from it (<release>-okdp, Secrets): a DNS-1123 label of
// ReleaseMax characters at most.
func IsRelease(name string) bool {
	return len(name) <= ReleaseMax && dnsLabel.MatchString(name)
}

// TruncName: s cut as a chart cuts a name, `trunc n | trimSuffix "-"`: to
// n bytes, without a trailing "-".
func TruncName(s string, n int) string {
	return strings.TrimSuffix(Trunc(s, n), "-")
}
