package helmfn

import "testing"

// TestTrunc: cut to n bytes, short strings unchanged.
func TestTrunc(t *testing.T) {
	if got := Trunc("abcdef", 3); got != "abc" {
		t.Errorf("got %q", got)
	}
	if got := Trunc("ab", 3); got != "ab" {
		t.Errorf("got %q", got)
	}
}
