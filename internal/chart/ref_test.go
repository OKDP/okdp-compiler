package chart

import (
	"strings"
	"testing"
)

// TestIsClaimID: DNS-1123 labels joined by dots, 63 characters at most.
func TestIsClaimID(t *testing.T) {
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
		if got := IsClaimID(id); got != want {
			t.Errorf("%q: got %v", id, got)
		}
	}
}

// TestIsFileName: one element of a path, not . or .., without a / or a \.
func TestIsFileName(t *testing.T) {
	for name, want := range map[string]bool{
		"database-server": true,
		"1.0.0":           true,
		"":                false,
		".":               false,
		"..":              false,
		"a/b":             false,
		`a\b`:             false,
		"/a":              false,
	} {
		if got := IsFileName(name); got != want {
			t.Errorf("%q: got %v", name, got)
		}
	}
}
