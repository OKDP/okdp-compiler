package chart

import (
	"reflect"
	"strings"
	"testing"
)

// TestParsePath: plain keys, bracketed literal keys (dots, escapes) and
// globs, anywhere in the path.
func TestParsePath(t *testing.T) {
	for p, want := range map[string][]pathSeg{
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
		`a["q\"b\\c\*"]`:      {{key: "a"}, {key: `q"b\c*`}},
		`a["x\**"]`:           {{key: "a"}, {key: "x*", glob: true}},
		`env.VITE_OIDC_SCOPE`: {{key: "env"}, {key: "VITE_OIDC_SCOPE"}},
	} {
		got, err := parsePath(p)
		if err != nil {
			t.Errorf("%s: %v", p, err)
		} else if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %#v, want %#v", p, got, want)
		}
	}
	for p, want := range map[string]string{
		"":                   "empty path",
		"a..b":               "an empty key",
		".a":                 "an empty key",
		"a.":                 "an empty key",
		"a.*":                `a * outside brackets: a glob is a bracketed key`,
		"annotations.x.io/*": `a * outside brackets: a glob is a bracketed key, the whole key in the brackets, dots included: ["prefix*"]`,
		"a]":                 `']' outside brackets`,
		`a"b`:                `'"' outside brackets`,
		`a\b`:                `'\\' outside brackets`,
		"a[b]":               `a bracketed key is quoted: ["key"]`,
		"a['b']":             `a bracketed key is quoted`,
		`a.["b"]`:            "no dot before a bracketed key",
		`a["b"`:              `a bracketed key ends with "]`,
		`a["b`:               `a bracketed key ends with "]`,
		`a["b"]c`:            `'c' after a bracketed key`,
		`a[""]`:              "an empty key",
		`a["x*y"]`:           "a * is a glob at the end of a bracketed key only",
		`a["**"]`:            "a * is a glob at the end of a bracketed key only",
		`a["\n"]`:            `escapes ", \ or * only`,
		`a["b\"]`:            `a bracketed key ends with "]`,
		`a["b"]["c"]..d`:     "an empty key",
		`a["nginx.io/x*"]x`:  `'x' after a bracketed key`,
	} {
		if _, err := parsePath(p); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: got %v, want an error containing %q", p, err, want)
		}
	}
}

// TestFormatPath: the key a value was found at, as the errors show it;
// parsePath reads it back as the same literal keys.
func TestFormatPath(t *testing.T) {
	for want, keys := range map[string][]string{
		"ingress.hosts": {"ingress", "hosts"},
		`filer.annotations["nginx.ingress.kubernetes.io/auth-type"]`: {"filer", "annotations", "nginx.ingress.kubernetes.io/auth-type"},
		`["spark.ui.filters"]`: {"spark.ui.filters"},
		`a["q\"b\\c\*"].d`:     {"a", `q"b\c*`, "d"},
		`a[""]`:                {"a", ""},
	} {
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
	// In an instance file, the message names the file.
	err := applyUpstream(computed(), M{"config": M{"spark.ui.filters": "x"}}, source{file: "upstream.c.yaml"}, protect, nil)
	if want := `upstream.c.yaml: config["spark.ui.filters"]: config["spark.ui.filters"] is set by the platform`; err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("got %v, want %q", err, want)
	}
	// A key in brackets first: no dot after the chart.
	err = applyUpstream(M{}, M{"a.b": float64(1)}, source{chart: "c"}, []string{`["a.b"]`}, nil)
	if want := `upstream.c["a.b"]: ["a.b"] is set by the platform`; err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("got %v, want %q", err, want)
	}
}
