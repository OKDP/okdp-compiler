package okdp

import (
	"strings"
	"testing"
)

// TestClaimIDForm: DNS-1123 labels joined by dots, 63 characters at most.
func TestClaimIDForm(t *testing.T) {
	for id, want := range map[string]bool{
		"demo.demo-app.db":                true,
		"demo.analytics":                  true,
		"demo":                            true,
		"":                                false,
		"demo..db":                        false,
		".demo":                           false,
		"demo.":                           false,
		"demo.-app":                       false,
		"demo.App":                        false,
		"demo_app.db":                     false,
		"demo." + strings.Repeat("a", 58): true,
		"demo." + strings.Repeat("a", 59): false,
	} {
		if got := claimIDForm(id); got != want {
			t.Errorf("%q: got %v", id, got)
		}
	}
}
