package value

import (
	"testing"
)

// TestPath: plain keys joined by dots, a key that is empty or holds a
// special character in brackets, as it is, an item [i]: the form chart's
// ParsePath reads back (TestFormatPath).
func TestPath(t *testing.T) {
	for _, tc := range []struct{ got, want string }{
		{Key("", "a"), "a"},
		{Key("a", "b"), "a.b"},
		{Key("a", "nginx.io/auth-type"), `a["nginx.io/auth-type"]`},
		{Key("", "a.b"), `["a.b"]`},
		{Key("a", ""), `a[""]`},
		{Key("a", `q"b\c*[]`), `a["q"b\c*[]"]`},
		{Key("env[1]", "name"), "env[1].name"},
		{Index("env", 2), "env[2]"},
		{Index(`["a.b"]`, 0), `["a.b"][0]`},
	} {
		if tc.got != tc.want {
			t.Errorf("got %s, want %s", tc.got, tc.want)
		}
	}
}

// TestDig: a dotted path, through maps only.
func TestDig(t *testing.T) {
	m := M{"a": M{"b": M{"c": "x"}, "s": "y"}}
	for path, want := range map[string]any{"a.b.c": "x", "a.s": "y", "a.s.t": nil, "a.missing": nil, "z": nil} {
		if got := Dig(m, path); got != want {
			t.Errorf("Dig(%q) = %#v, want %#v", path, got, want)
		}
	}
}

// A path splits into its keys and indexes, joins back, and is a JSON
// pointer.
func TestPathTokens(t *testing.T) {
	for p, want := range map[string]string{
		"":                           "",
		"a":                          "/a",
		"env[2].name":                "/env/2/name",
		`config["spark.ui.filters"]`: "/config/spark.ui.filters",
		`annotations["a.b/c"].x`:     "/annotations/a.b~1c/x",
		"[0]":                        "/0",
	} {
		tokens := Split(p)
		if got := Pointer(tokens); got != want {
			t.Errorf("Pointer(Split(%q)) = %q, want %q", p, got, want)
		}
		if got := Join(tokens); got != p {
			t.Errorf("Join(Split(%q)) = %q", p, got)
		}
	}
}
