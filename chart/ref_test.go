package chart

import (
	"cmp"
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

// TestCompareVersions: SemVer precedence, the build metadata ignored.
func TestCompareVersions(t *testing.T) {
	ordered := []string{"1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta", "1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-rc.1", "1.0.0", "1.0.1", "1.2.0", "1.10.0", "2.0.0-1.0.0", "2.0.0-1.0.2", "2.0.0", "10.0.0"}
	for i, a := range ordered {
		for j, b := range ordered {
			if got, want := CompareVersions(a, b), cmp.Compare(i, j); got != want {
				t.Errorf("%s, %s: got %d, want %d", a, b, got, want)
			}
		}
	}
	for _, same := range [][2]string{{"1.0.0", "v1.0.0"}, {"1.0.0+a", "1.0.0+b"}, {"1.0.0-rc.1+x", "1.0.0-rc.1"}, {"01.0.0", "1.0.0"}} {
		if got := CompareVersions(same[0], same[1]); got != 0 {
			t.Errorf("%s, %s: got %d", same[0], same[1], got)
		}
	}
}
