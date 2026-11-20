package chart

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/okdp/okdp-compiler/value"
)

// parsePathCases: paths and their keys, for TestParsePath and
// FuzzParsePath.
var parsePathCases = map[string][]PathSeg{
	"fullnameOverride": {{Key: "fullnameOverride"}},
	"controller.extraArgs.enable-ssl-passthrough": {{Key: "controller"}, {Key: "extraArgs"}, {Key: "enable-ssl-passthrough"}},
	`config["spark.ui.filters"]`:                  {{Key: "config"}, {Key: "spark.ui.filters"}},
	`config["spark.history.*"]`:                   {{Key: "config"}, {Key: "spark.history.", Glob: true}},
	`filer.servers.http.annotations["example.org/auth-*"]`: {
		{Key: "filer"}, {Key: "servers"}, {Key: "http"}, {Key: "annotations"}, {Key: "example.org/auth-", Glob: true},
	},
	`["a.b"]`:            {{Key: "a.b"}},
	`["a.b"]["c"].d`:     {{Key: "a.b"}, {Key: "c"}, {Key: "d"}},
	`servers["*"].tls`:   {{Key: "servers"}, {Key: "", Glob: true}, {Key: "tls"}},
	`env.VITE_API_SCOPE`: {{Key: "env"}, {Key: "VITE_API_SCOPE"}},
}

// parsePathErrors: malformed paths, for TestParsePath and FuzzParsePath.
var parsePathErrors = []string{
	"", "a..b", ".a", "a.", "a.*", "annotations.x.io/*", "a]", `a"b`, `a\b`, "a[b]", "a['b']",
	`a.["b"]`, `a["b"`, `a["b`, `a["b"]c`, `a[""]`, `a["x*y"]`, `a["**"]`, `a["q\"b"]`, `a["b"]["c"]..d`, `a["nginx.io/x*"]x`,
}

// TestParsePath: plain keys, bracketed literal keys (dots) and globs,
// anywhere in the path; anything else is not a path.
func TestParsePath(t *testing.T) {
	for p, want := range parsePathCases {
		got, err := ParsePath(p)
		if err != nil {
			t.Errorf("%s: %v", p, err)
		} else if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %#v, want %#v", p, got, want)
		}
	}
	for _, p := range parsePathErrors {
		if _, err := ParsePath(p); !errors.Is(err, errPath) {
			t.Errorf("%q: got %v, want %v", p, err, errPath)
		}
	}
}

// FuzzParsePath: what ParsePath reads is keys, none empty but a glob's
// prefix; written back, plain where it can be (FormatPath) and a glob as
// ["<prefix>*"], it reads as the same keys. Seeds: the paths of
// TestParsePath.
func FuzzParsePath(f *testing.F) {
	for p := range parsePathCases {
		f.Add(p)
	}
	for _, p := range parsePathErrors {
		f.Add(p)
	}
	f.Fuzz(func(t *testing.T, p string) {
		segs, err := ParsePath(p)
		if err != nil {
			return
		}
		written := ""
		for _, s := range segs {
			switch {
			case s.Glob:
				written += `["` + s.Key + `*"]`
			case s.Key == "":
				t.Fatalf("%q: an empty key, in %#v", p, segs)
			default:
				written = value.Key(written, s.Key)
			}
		}
		if back, err := ParsePath(written); err != nil || !reflect.DeepEqual(back, segs) {
			t.Errorf("%q: read as %#v, written %s, read back as %#v (%v)", p, segs, written, back, err)
		}
	})
}

// formatPathCases: keys and their path, for TestFormatPath and
// FuzzFormatPath.
var formatPathCases = map[string][]string{
	"service.ports": {"service", "ports"},
	`filer.annotations["example.org/auth-type"]`: {"filer", "annotations", "example.org/auth-type"},
	`["spark.ui.filters"]`:                       {"spark.ui.filters"},
	`a[""]`:                                      {"a", ""},
}

// TestFormatPath: the key a value was found at, as the errors show it;
// ParsePath reads it back as the same literal keys.
func TestFormatPath(t *testing.T) {
	for want, keys := range formatPathCases {
		if got := FormatPath(keys); got != want {
			t.Errorf("%q: got %s, want %s", keys, got, want)
		}
		if want == `a[""]` {
			continue // an empty key is printed, never written in a path
		}
		segs, err := ParsePath(want)
		if err != nil {
			t.Errorf("%s: %v", want, err)
			continue
		}
		var back []string
		for _, s := range segs {
			if s.Glob {
				t.Errorf("%s: a glob", want)
			}
			back = append(back, s.Key)
		}
		if !reflect.DeepEqual(back, keys) {
			t.Errorf("%s: read back as %q", want, back)
		}
	}
}

// FuzzFormatPath: ParsePath reads what FormatPath writes of any keys without
// a quote, a backslash or a star (the fuzzed text split at \xff, a byte
// UTF-8 never holds) as the same literal keys; with an empty key, which no
// path names, it fails. Seeds: the keys of TestFormatPath.
func FuzzFormatPath(f *testing.F) {
	for _, keys := range formatPathCases {
		f.Add(strings.Join(keys, "\xff"))
	}
	f.Fuzz(func(t *testing.T, s string) {
		if strings.ContainsAny(s, `"\*`) {
			return
		}
		keys := strings.Split(s, "\xff")
		p := FormatPath(keys)
		segs, err := ParsePath(p)
		if slices.Contains(keys, "") {
			if err == nil {
				t.Errorf("%q: written %s, read as %#v, an empty key", keys, p, segs)
			}
			return
		}
		back := []string{}
		for _, s := range segs {
			if s.Glob {
				t.Errorf("%q: written %s, read with a glob", keys, p)
			}
			back = append(back, s.Key)
		}
		if err != nil || !reflect.DeepEqual(back, keys) {
			t.Errorf("%q: written %s, read back as %q (%v)", keys, p, back, err)
		}
	})
}
