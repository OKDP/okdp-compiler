package helm

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/okdp/okdp-compiler/internal/chart"
	"github.com/okdp/okdp-compiler/internal/okdp"
	"github.com/okdp/okdp-compiler/internal/target"
)

// The charts the compiler adds, as a compile's result names them.
var (
	dcrSource     = chart.Ref{Repository: "oci://quay.io/adaltas", Chart: "oidc-dcr", Version: "0.6.0"}
	objectsSource = chart.Ref{Repository: "oci://quay.io/okdp/charts", Chart: okdp.ObjectsChart, Version: "1.0.0"}
)

// TestUmbrella: the package's Chart.yaml, every field kept, in Helm's
// order; its dependencies those compiled: those of Chart.yaml in its order,
// as written, okdp among them, then the compiler's own charts, the OAuth
// clients', by key, each aliased when its key is not the chart's name; the
// values under the keys.
func TestUmbrella(t *testing.T) {
	app := chart.Ref{Repository: "https://charts.example.org", Chart: "app", Version: "1.0.0"}
	res := &okdp.Result{
		Values: map[string]map[string]any{
			"web": {"a": 1, "global": map[string]any{"g": 1}}, "app": {"a": nil},
			"oidc-dcr": {}, "zeta-oidc-dcr": {}, "okdp": {},
		},
		Charts: map[string]chart.Ref{"web": app, "app": app, "oidc-dcr": dcrSource, "zeta-oidc-dcr": dcrSource, "okdp": app},
		Chart: chart.Metadata{
			APIVersion: "v2", Name: "demo", Version: "1.2.3", KubeVersion: ">=1.28.0", Description: "A demo.", Type: "application",
			Keywords: []string{"demo"}, Home: "https://example.org", Sources: []string{"https://example.org/src"},
			Dependencies: []chart.Dependency{
				{Name: "app", Alias: "web", Repository: app.Repository, Version: app.Version},
				{Name: "backup", Repository: "oci://x", Version: "2.0.0"},
				{Name: "app", Alias: "app", Repository: app.Repository, Version: app.Version},
				{Name: "app", Alias: "okdp", Repository: app.Repository, Version: app.Version},
			},
			Maintainers: []chart.Maintainer{{Name: "OKDP", Email: "okdp@example.org"}},
			Icon:        "https://example.org/icon.svg", AppVersion: "1.16", Deprecated: true, Annotations: map[string]string{"b": "2", "a": "1"},
		},
	}
	files, err := Write(res)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"Chart.yaml": `apiVersion: v2
name: demo
version: 1.2.3
kubeVersion: '>=1.28.0'
description: A demo.
type: application
keywords:
  - demo
home: https://example.org
sources:
  - https://example.org/src
dependencies:
  - name: app
    alias: web
    repository: https://charts.example.org
    version: 1.0.0
  - name: app
    alias: app
    repository: https://charts.example.org
    version: 1.0.0
  - name: app
    alias: okdp
    repository: https://charts.example.org
    version: 1.0.0
  - name: oidc-dcr
    repository: oci://quay.io/adaltas
    version: 0.6.0
  - name: oidc-dcr
    alias: zeta-oidc-dcr
    repository: oci://quay.io/adaltas
    version: 0.6.0
maintainers:
  - name: OKDP
    email: okdp@example.org
icon: https://example.org/icon.svg
appVersion: "1.16"
deprecated: true
annotations:
  a: "1"
  b: "2"
`,
		ValuesFile: target.Generated + "app:\n  a: null\noidc-dcr: {}\nokdp: {}\nweb:\n  a: 1\n  global:\n    g: 1\nzeta-oidc-dcr: {}\n",
	} {
		if got := string(files[name]); got != want {
			t.Errorf("%s:\n got %q\nwant %q", name, got, want)
		}
	}
	if len(res.Chart.Dependencies) != 4 {
		t.Errorf("Result.Chart changed: %#v", res.Chart.Dependencies)
	}
}

// TestWrite: the umbrella's values, marked generated, what the instance
// publishes, its empty keys left out, and the objects as one YAML stream,
// indented by 2 spaces, in the values of the chart that renders them, a
// dependency; the dependencies of the instances in no file; publishing
// nothing, no instance file; without objects, no such chart.
func TestWrite(t *testing.T) {
	app := chart.Ref{Repository: "https://charts.example.org", Chart: "app", Version: "1.8.0"}
	res := &okdp.Result{
		Values:        map[string]map[string]any{"oidc-dcr": {"a": map[string]any{"b": []any{1}}}, "app": {"b": 2}},
		Charts:        map[string]chart.Ref{"oidc-dcr": dcrSource, "app": app},
		Objects:       []map[string]any{{"kind": "ConfigMap", "data": map[string]any{"t": "{{ .x }}"}}, {"kind": "Secret", "data": nil}},
		ObjectsSource: objectsSource,
		Dependencies:  []okdp.Dependency{{Instance: "demo-db", Contract: "database", Field: "backends[0].database"}},
		Info: okdp.Info{URL: "https://x", Usage: "use it\n", Provides: []okdp.Output{
			{Name: "demo-app", Contract: "http", Values: map[string]any{"url": "https://x"}, SecretRef: &okdp.SecretRef{Name: "creds"}},
		}},
	}
	files, err := Write(res)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		ValuesFile: target.Generated + "app:\n  b: 2\noidc-dcr:\n  a:\n    b:\n      - 1\nokdp-objects:\n  manifests: |\n    data:\n      t: '{{ .x }}'\n    kind: ConfigMap\n    ---\n    data: null\n    kind: Secret\n",
		InfoFile:   "url: https://x\nusage: |\n  use it\nprovides:\n  - name: demo-app\n    contract: http\n    values:\n      url: https://x\n    secretRef:\n      name: creds\n",
	}
	if len(files) != len(want)+1 {
		t.Errorf("got %d files, want %d and Chart.yaml", len(files), len(want))
	}
	for name, w := range want {
		if got := string(files[name]); got != w {
			t.Errorf("%s: got %q, want %q", name, got, w)
		}
	}
	chartDep := "- name: okdp-objects\n    repository: oci://quay.io/okdp/charts\n    version: 1.0.0\n"
	if !strings.Contains(string(files["Chart.yaml"]), chartDep) {
		t.Errorf("the chart of the objects is no dependency:\n%s", files["Chart.yaml"])
	}
	if _, ok := res.Values[okdp.ObjectsChart]; ok {
		t.Errorf("Result.Values changed: %v", res.Values)
	}
	// No object, nothing published: Chart.yaml and the values only, the
	// dependencies all the same.
	res.Objects, res.Info = nil, okdp.Info{}
	if files, err = Write(res); err != nil {
		t.Fatal(err)
	}
	if got := slices.Sorted(maps.Keys(files)); !slices.Equal(got, []string{"Chart.yaml", ValuesFile}) {
		t.Errorf("nothing but the chart: got %v", got)
	}
	if strings.Contains(string(files["Chart.yaml"]), okdp.ObjectsChart) {
		t.Errorf("a chart of no objects:\n%s", files["Chart.yaml"])
	}
	// A usage alone is published, without the empty keys, of the instance
	// and of an output.
	res.Info = okdp.Info{Usage: "use it\n"}
	if files, err = Write(res); err != nil || string(files[InfoFile]) != "usage: |\n  use it\n" {
		t.Errorf("a usage alone: %v %q", err, files[InfoFile])
	}
	res.Info = okdp.Info{Provides: []okdp.Output{{Name: "demo-app", Contract: "http", Values: map[string]any{}}}}
	if files, err = Write(res); err != nil || string(files[InfoFile]) != "provides:\n  - name: demo-app\n    contract: http\n" {
		t.Errorf("an output without values: %v %q", err, files[InfoFile])
	}
}
