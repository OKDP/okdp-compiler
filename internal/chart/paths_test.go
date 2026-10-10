package chart

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/okdp/okdp-compiler/internal/value"
)

// parsePathCases: paths and their keys, for TestParsePath and
// FuzzParsePath.
var parsePathCases = map[string][]pathSeg{
	"fullnameOverride": {{key: "fullnameOverride"}},
	"controller.extraArgs.enable-ssl-passthrough": {{key: "controller"}, {key: "extraArgs"}, {key: "enable-ssl-passthrough"}},
	`config["spark.ui.filters"]`:                  {{key: "config"}, {key: "spark.ui.filters"}},
	`config["spark.history.*"]`:                   {{key: "config"}, {key: "spark.history.", glob: true}},
	`filer.ingresses.http.annotations["nginx.ingress.kubernetes.io/auth-*"]`: {
		{key: "filer"}, {key: "ingresses"}, {key: "http"}, {key: "annotations"}, {key: "nginx.ingress.kubernetes.io/auth-", glob: true},
	},
	`["a.b"]`:             {{key: "a.b"}},
	`["a.b"]["c"].d`:      {{key: "a.b"}, {key: "c"}, {key: "d"}},
	`ingresses["*"].tls`:  {{key: "ingresses"}, {key: "", glob: true}, {key: "tls"}},
	`env.VITE_OIDC_SCOPE`: {{key: "env"}, {key: "VITE_OIDC_SCOPE"}},
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
		got, err := parsePath(p)
		if err != nil {
			t.Errorf("%s: %v", p, err)
		} else if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %#v, want %#v", p, got, want)
		}
	}
	for _, p := range parsePathErrors {
		if _, err := parsePath(p); !errors.Is(err, errPath) {
			t.Errorf("%q: got %v, want %v", p, err, errPath)
		}
	}
}

// FuzzParsePath: what parsePath reads is keys, none empty but a glob's
// prefix; written back, plain where it can be (formatPath) and a glob as
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
		segs, err := parsePath(p)
		if err != nil {
			return
		}
		written := ""
		for _, s := range segs {
			switch {
			case s.glob:
				written += `["` + s.key + `*"]`
			case s.key == "":
				t.Fatalf("%q: an empty key, in %#v", p, segs)
			default:
				written = value.Key(written, s.key)
			}
		}
		if back, err := parsePath(written); err != nil || !reflect.DeepEqual(back, segs) {
			t.Errorf("%q: read as %#v, written %s, read back as %#v (%v)", p, segs, written, back, err)
		}
	})
}

// formatPathCases: keys and their path, for TestFormatPath and
// FuzzFormatPath.
var formatPathCases = map[string][]string{
	"ingress.hosts": {"ingress", "hosts"},
	`filer.annotations["nginx.ingress.kubernetes.io/auth-type"]`: {"filer", "annotations", "nginx.ingress.kubernetes.io/auth-type"},
	`["spark.ui.filters"]`: {"spark.ui.filters"},
	`a[""]`:                {"a", ""},
}

// TestFormatPath: the key a value was found at, as the errors show it;
// parsePath reads it back as the same literal keys.
func TestFormatPath(t *testing.T) {
	for want, keys := range formatPathCases {
		if got := formatPath(keys); got != want {
			t.Errorf("%q: got %s, want %s", keys, got, want)
		}
		if want == `a[""]` {
			continue // an empty key is printed, never written in a path
		}
		segs, err := parsePath(want)
		if err != nil {
			t.Errorf("%s: %v", want, err)
			continue
		}
		var back []string
		for _, s := range segs {
			if s.glob {
				t.Errorf("%s: a glob", want)
			}
			back = append(back, s.key)
		}
		if !reflect.DeepEqual(back, keys) {
			t.Errorf("%s: read back as %q", want, back)
		}
	}
}

// FuzzFormatPath: parsePath reads what formatPath writes of any keys without
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
		p := formatPath(keys)
		segs, err := parsePath(p)
		if slices.Contains(keys, "") {
			if err == nil {
				t.Errorf("%q: written %s, read as %#v, an empty key", keys, p, segs)
			}
			return
		}
		back := []string{}
		for _, s := range segs {
			if s.glob {
				t.Errorf("%q: written %s, read with a glob", keys, p)
			}
			back = append(back, s.key)
		}
		if err != nil || !reflect.DeepEqual(back, keys) {
			t.Errorf("%q: written %s, read back as %q (%v)", keys, p, back, err)
		}
	})
}

// TestUpstreamBrackets: protect and append paths with bracketed literal
// keys (dots in them) and globs: the keys a plain dotted path cannot name.
func TestUpstreamBrackets(t *testing.T) {
	protect := []string{
		`annotations["nginx.ingress.kubernetes.io/auth-*"]`,
		`config["spark.ui.filters"]`,
		`config["spark.history.ui.acls.*"]`,
		`ingresses["*"].tls`,
	}
	appendPaths := []string{`config["spark.extra.list"]`, `envs["*"]`, `envs["main"]`}
	computed := func() M {
		return M{
			"annotations": M{"nginx.ingress.kubernetes.io/auth-type": "basic", "nginx.ingress.kubernetes.io/auth-url": "u"},
			"config":      M{"spark.ui.filters": "f", "spark.extra.list": []any{"a"}},
			"envs":        M{"main": []any{"A"}},
			"ingresses":   M{"http": M{"tls": []any{"t"}, "host": "h"}},
		}
	}
	for name, tc := range map[string]struct {
		user M
		want string // the error, or "" for none
		path string // the key changed, when no error
		v    any    // its value
	}{
		"other annotation":           {M{"annotations": M{"nginx.ingress.kubernetes.io/proxy-body-size": "1m"}}, "", "annotations", M{"nginx.ingress.kubernetes.io/auth-type": "basic", "nginx.ingress.kubernetes.io/auth-url": "u", "nginx.ingress.kubernetes.io/proxy-body-size": "1m"}},
		"a glob's annotation":        {M{"annotations": M{"nginx.ingress.kubernetes.io/auth-type": nil}}, `upstream.c.annotations["nginx.ingress.kubernetes.io/auth-type"]: annotations["nginx.ingress.kubernetes.io/auth-*"] is set by the platform and cannot be changed`, "", nil},
		"a new key of the glob":      {M{"annotations": M{"nginx.ingress.kubernetes.io/auth-signin": "x"}}, `upstream.c.annotations["nginx.ingress.kubernetes.io/auth-signin"]: annotations["nginx.ingress.kubernetes.io/auth-*"]`, "", nil},
		"the glob's parent":          {M{"annotations": nil}, `upstream.c.annotations: annotations["nginx.ingress.kubernetes.io/auth-*"] is set by the platform`, "", nil},
		"a literal dotted key":       {M{"config": M{"spark.ui.filters": "other"}}, `upstream.c.config["spark.ui.filters"]: config["spark.ui.filters"] is set by the platform`, "", nil},
		"a key the literal is not":   {M{"config": M{"spark.ui.filters.x": "1"}}, "", "config", M{"spark.ui.filters": "f", "spark.ui.filters.x": "1", "spark.extra.list": []any{"a"}}},
		"a glob's prefix itself":     {M{"config": M{"spark.history.ui.acls.": "1"}}, `upstream.c.config["spark.history.ui.acls."]`, "", nil},
		"a glob on the way":          {M{"ingresses": M{"grpc": M{"tls": []any{}}}}, `upstream.c.ingresses.grpc.tls: ingresses["*"].tls is set by the platform`, "", nil},
		"beside a glob on the way":   {M{"ingresses": M{"http": M{"host": "other"}}}, "", "ingresses", M{"http": M{"tls": []any{"t"}, "host": "other"}}},
		"a non-map on the way":       {M{"ingresses": M{"http": "off"}}, `upstream.c.ingresses.http: ingresses["*"].tls`, "", nil},
		"appended dotted key":        {M{"config": M{"spark.extra.list": []any{"b"}}}, "", "config", M{"spark.ui.filters": "f", "spark.extra.list": []any{"a", "b"}}},
		"appended once per key":      {M{"envs": M{"main": []any{"B"}, "side": []any{"S"}}}, "", "envs", M{"main": []any{"A", "B"}, "side": []any{"S"}}},
		"appended a non-list":        {M{"envs": M{"side": "S"}}, "upstream.c.envs.side must be a list", "", nil},
		"appended a dotted non-list": {M{"config": M{"spark.extra.list": "b"}}, `upstream.c.config["spark.extra.list"] must be a list`, "", nil},
	} {
		t.Run(name, func(t *testing.T) {
			got := computed()
			err := applyUpstream(got, tc.user, source{chart: "c"}, protect, appendPaths)
			if tc.want != "" {
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Errorf("got %v, want an error containing %q", err, tc.want)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := computed()
			want[tc.path] = tc.v
			if !reflect.DeepEqual(got, want) {
				t.Errorf("got  %#v\nwant %#v", got, want)
			}
		})
	}
	// A key in brackets first: no dot after the chart.
	err := applyUpstream(M{}, M{"a.b": float64(1)}, source{chart: "c"}, []string{`["a.b"]`}, nil)
	if want := `upstream.c["a.b"]: ["a.b"] is set by the platform`; err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("got %v, want %q", err, want)
	}
}
