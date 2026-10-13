package chart

import (
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

// TestRefCacheDir: a repository's directory of the chart cache, a path
// inside it or none.
func TestRefCacheDir(t *testing.T) {
	for repository, want := range map[string]string{
		"oci://quay.io/adaltas":             "quay.io/adaltas",
		"oci://quay.io/adaltas/":            "quay.io/adaltas",
		"https://charts.example.org":        "charts.example.org",
		"http://localhost:8080/charts/":     "localhost:8080/charts",
		"https://":                          "",
		"https:///charts":                   "",
		"https://charts.example.org//x":     "",
		"https://charts.example.org/./x":    "",
		"https://charts.example.org/../../": "",
		"oci://..":                          "",
		"oci://.":                           "",
		`https://charts.example.org\x`:      "",
		"file:///charts":                    "",
		"charts.example.org":                "",
	} {
		if got := (Ref{Repository: repository}).cacheDir(); got != want {
			t.Errorf("%s: got %q, want %q", repository, got, want)
		}
	}
}

// TestChartCache: an upstream chart unpacked under its repository's
// directory, its own defaults and schema (not its subcharts'), a byte order
// mark stripped; the helm command of a chart the directory does not hold,
// also when the root of the cache does, or holds its archive or the chart
// directly under <chart>-<version>.
func TestChartCache(t *testing.T) {
	cache := fstest.MapFS{
		"charts.example.org/app-1.0.0/app/values.yaml":              {Data: []byte("a: 1\n")},
		"charts.example.org/app-1.0.0/app/values.schema.json":       {Data: []byte("{}")},
		"charts.example.org/app-1.0.0/app/templates/x.yaml":         {Data: []byte("{{ .Values.a }}")},
		"charts.example.org/app-1.0.0/app/charts/sub/values.yaml":   {Data: []byte("c: 3\n")},
		"charts.example.org/nodefaults-1.0.0/nodefaults/Chart.yaml": {Data: []byte("name: nodefaults\n")},
		"charts.example.org/bom-1.0.0/bom/values.yaml":              {Data: []byte("\uFEFFb: on\n")},
		"charts.example.org/bom-1.0.0/bom/values.schema.json":       {Data: []byte("\uFEFF{}")},
		"quay.io/adaltas/oidc-dcr-0.5.0/oidc-dcr/values.yaml":       {Data: []byte("enabled: true\n")},
		"rooted-1.0.0/rooted/values.yaml":                           {Data: []byte("a: 0\n")},
		"charts.example.org/packed-1.0.0.tgz":                       {Data: []byte("not read")},
		"charts.example.org/flat-1.0.0/values.yaml":                 {Data: []byte("a: 0\n")},
		"charts.example.org/broken-1.0.0/broken/values.yaml":        {Data: []byte("a: [\n")},
	}
	ref := func(chart, version string) Ref {
		return Ref{Repository: "https://charts.example.org", Chart: chart, Version: version}
	}
	c := &chartCache{fsys: cache, charts: map[Ref]*upstreamChart{}}
	for name, tc := range map[string]struct {
		ref      Ref
		entry    string
		defaults M
		schema   string
	}{
		"unpacked":         {ref("app", "1.0.0"), "charts.example.org/app-1.0.0/app", M{"a": float64(1)}, "{}"},
		"no values.yaml":   {ref("nodefaults", "1.0.0"), "charts.example.org/nodefaults-1.0.0/nodefaults", M{}, ""},
		"byte order mark":  {ref("bom", "1.0.0"), "charts.example.org/bom-1.0.0/bom", M{"b": true}, "{}"},
		"read once, again": {ref("app", "1.0.0"), "charts.example.org/app-1.0.0/app", M{"a": float64(1)}, "{}"},
		"a trailing /":     {Ref{Repository: "https://charts.example.org/", Chart: "app", Version: "1.0.0"}, "charts.example.org/app-1.0.0/app", M{"a": float64(1)}, "{}"},
		"an OCI chart":     {Ref{Repository: "oci://quay.io/adaltas", Chart: "oidc-dcr", Version: "0.5.0"}, "quay.io/adaltas/oidc-dcr-0.5.0/oidc-dcr", M{"enabled": true}, ""},
	} {
		u, err := c.get(tc.ref)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if u.entry != tc.entry || !reflect.DeepEqual(u.defaults, tc.defaults) || string(u.schema) != tc.schema {
			t.Errorf("%s: got %s %#v %q", name, u.entry, u.defaults, u.schema)
		}
	}
	for name, tc := range map[string]struct {
		ref  Ref
		want string
	}{
		"not in the cache":  {ref("nope", "1.0.0"), "chart nope-1.0.0 is not in the chart cache: helm pull nope --repo https://charts.example.org --version 1.0.0 --untar -d <chart cache>/charts.example.org/nope-1.0.0"},
		"not in, OCI":       {Ref{Repository: "oci://quay.io/adaltas/", Chart: "nope", Version: "1.0.0"}, "chart nope-1.0.0 is not in the chart cache: helm pull oci://quay.io/adaltas/nope --version 1.0.0 --untar -d <chart cache>/quay.io/adaltas/nope-1.0.0"},
		"at the root only":  {ref("rooted", "1.0.0"), "chart rooted-1.0.0 is not in the chart cache: "},
		"an archive":        {ref("packed", "1.0.0"), "chart packed-1.0.0 is not in the chart cache: "},
		"not under <chart>": {ref("flat", "1.0.0"), "chart flat-1.0.0 is not in the chart cache: "},
		"bad values.yaml":   {ref("broken", "1.0.0"), "charts.example.org/broken-1.0.0/broken/values.yaml: "},
	} {
		if _, err := c.get(tc.ref); err == nil || !strings.HasPrefix(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want an error starting with %q", name, err, tc.want)
		}
	}
}
