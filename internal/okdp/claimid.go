package okdp

import (
	"strings"

	"github.com/okdp/okdp-compiler/chart"
)

// claimIDForm reports whether s has the form of a claim ID: DNS-1123
// labels joined by dots, 63 characters at most. Such an ID is a
// Kubernetes name (a DNS subdomain), and the dots separate its parts. A
// single label passes: IsClaimID also requires a dot.
func claimIDForm(s string) bool {
	if len(s) > 63 {
		return false
	}
	for l := range strings.SplitSeq(s, ".") {
		if !chart.IsDNSLabel(l) {
			return false
		}
	}
	return true
}
