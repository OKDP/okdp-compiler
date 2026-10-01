package compiler

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

// TestChartRefCheck: a source names a repository URL, a chart and a pinned
// version, refused when service.yaml is loaded.
func TestChartRefCheck(t *testing.T) {
	for source, want := range map[string]string{
		"{repository: https://x, chart: app, version: 1.0.0}":            "",
		"{repository: oci://x/charts, chart: app, version: v2.1.0-rc.1}": "",
		"": "charts.app.source is required: the upstream chart (repository, chart, version)",
		"{repository: x.org, chart: app, version: 1.0.0}":     `charts.app.source.repository "x.org": the URL of a chart repository`,
		"{repository: https://x, chart: ../app, version: 1}":  `charts.app.source.chart "../app": the name of a chart`,
		"{repository: https://x, chart: a/b, version: 1.0.0}": `charts.app.source.chart "a/b": the name of a chart`,
		"{repository: https://x, version: 1.0.0}":             `charts.app.source.chart "": the name of a chart`,
		"{repository: https://x, chart: app, version: ^1.0}":  `charts.app.source.version "^1.0": a pinned chart version (1.2.3), not a range`,
		"{repository: https://x, chart: app}":                 `charts.app.source.version "": a pinned chart version`,
	} {
		yml := "name: demo\nversion: 1.0.0\ncharts:\n  app: {source: " + source + "}\n"
		if source == "" {
			yml = "name: demo\nversion: 1.0.0\ncharts:\n  app: {}\n"
		}
		_, err := LoadDefinition(fstest.MapFS{"service.yaml": {Data: []byte(yml)}, "mapping.cel.yaml": {Data: []byte("{}\n")}})
		if want == "" {
			if err != nil {
				t.Errorf("%s: %v", source, err)
			}
		} else if err == nil || !strings.Contains(err.Error(), "service.yaml: "+want) {
			t.Errorf("%s: got %v, want an error containing %q", source, err, want)
		}
	}
}

// tgz: a chart archive, as helm package writes it.
func tgz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestChartCache: an upstream chart unpacked or as its archive, its own
// defaults and schema (not its subcharts'), and the helm command of a chart
// the cache does not hold.
func TestChartCache(t *testing.T) {
	cache := fstest.MapFS{
		"app-1.0.0/values.yaml":        {Data: []byte("a: 1\n")},
		"app-1.0.0/values.schema.json": {Data: []byte("{}")},
		"app-1.0.0/templates/x.yaml":   {Data: []byte("{{ .Values.a }}")},
		"nodefaults-1.0.0/Chart.yaml":  {Data: []byte("name: nodefaults\n")},
		"packed-2.0.0.tgz": {Data: tgz(t, map[string]string{
			"packed/Chart.yaml":                    "name: packed\n",
			"packed/values.yaml":                   "b: on\n",
			"packed/charts/sub/values.yaml":        "c: 3\n",
			"packed/charts/sub/values.schema.json": "false",
		})},
		"broken-1.0.0/values.yaml": {Data: []byte("a: [\n")},
		"corrupt-1.0.0.tgz":        {Data: []byte("not gzip")},
	}
	c := &chartCache{fsys: cache, charts: map[ChartRef]*upstreamChart{}}
	ref := func(chart, version string) ChartRef {
		return ChartRef{Repository: "https://charts.example.org", Chart: chart, Version: version}
	}
	for name, tc := range map[string]struct {
		ref      ChartRef
		defaults M
		schema   string
	}{
		"unpacked":         {ref("app", "1.0.0"), M{"a": float64(1)}, "{}"},
		"archive":          {ref("packed", "2.0.0"), M{"b": true}, ""},
		"no values.yaml":   {ref("nodefaults", "1.0.0"), M{}, ""},
		"read once, again": {ref("app", "1.0.0"), M{"a": float64(1)}, "{}"},
	} {
		u, err := c.get(tc.ref)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if !reflect.DeepEqual(u.defaults, tc.defaults) || string(u.schema) != tc.schema {
			t.Errorf("%s: got %#v %q", name, u.defaults, u.schema)
		}
	}
	for name, tc := range map[string]struct {
		ref  ChartRef
		want string
	}{
		"not in the cache": {ref("nope", "1.0.0"), "chart nope-1.0.0 is not in the chart cache: helm pull nope --repo https://charts.example.org --version 1.0.0 -d <chart cache>"},
		"an OCI chart":     {ChartRef{Repository: "oci://quay.io/adaltas/", Chart: "nope", Version: "1.0.0"}, "helm pull oci://quay.io/adaltas/nope --version 1.0.0 -d <chart cache>"},
		"bad values.yaml":  {ref("broken", "1.0.0"), "broken-1.0.0/values.yaml: "},
		"bad archive":      {ref("corrupt", "1.0.0"), "corrupt-1.0.0.tgz: "},
	} {
		if _, err := c.get(tc.ref); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want an error containing %q", name, err, tc.want)
		}
	}
}
