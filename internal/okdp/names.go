// Names: the names an instance gives its objects, as Kubernetes and Helm
// accept them.
package okdp

import "regexp"

// dnsLabel: a DNS-1123 label, as Kubernetes names a namespace: lowercase
// letters, digits and "-", a letter or digit at each end, 63 characters at
// most (checked apart).
var dnsLabel = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// releaseMax: the longest release name Helm accepts, leaving room for the
// suffixes of the names derived from it.
const releaseMax = 53

// isRelease: name is a release name, as Helm accepts it and OKDP derives
// object names from it (<release>-okdp, Secrets): a DNS-1123 label of 53
// characters at most.
func isRelease(name string) bool {
	return len(name) <= releaseMax && dnsLabel.MatchString(name)
}
