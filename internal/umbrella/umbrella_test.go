package umbrella

import (
	"maps"
	"slices"
	"testing"

	"github.com/okdp/okdp-compiler/internal/chart"
	"github.com/okdp/okdp-compiler/internal/okdp"
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
		Charts: map[string]chart.Ref{"web": app, "app": app, "oidc-dcr": okdp.DcrSource, "zeta-oidc-dcr": okdp.DcrSource, "okdp": app},
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
    version: 0.5.0
  - name: oidc-dcr
    alias: zeta-oidc-dcr
    repository: oci://quay.io/adaltas
    version: 0.5.0
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
		ValuesFile: generated + "app:\n  a: null\noidc-dcr: {}\nokdp: {}\nweb:\n  a: 1\n  global:\n    g: 1\nzeta-oidc-dcr: {}\n",
	} {
		if got := string(files[name]); got != want {
			t.Errorf("%s:\n got %q\nwant %q", name, got, want)
		}
	}
	if len(res.Chart.Dependencies) != 4 {
		t.Errorf("Result.Chart changed: %#v", res.Chart.Dependencies)
	}
}

// TestWrite: the umbrella's values, marked generated, the dependencies as
// a YAML list, [] when there is none, what the instance publishes, its
// provides [] when none, and the objects as a YAML stream, marked
// generated, with the template that renders it, indented by 2 spaces;
// without objects, neither file.
func TestWrite(t *testing.T) {
	app := chart.Ref{Repository: "https://charts.example.org", Chart: "app", Version: "1.8.0"}
	res := &okdp.Result{
		Values:       map[string]map[string]any{"oidc-dcr": {"a": map[string]any{"b": []any{1}}}, "app": {"b": 2}},
		Charts:       map[string]chart.Ref{"oidc-dcr": okdp.DcrSource, "app": app},
		Objects:      []map[string]any{{"kind": "ConfigMap", "data": map[string]any{"t": "{{ .x }}"}}, {"kind": "Secret", "data": nil}},
		Dependencies: []okdp.Dependency{{Instance: "demo-db", Contract: "database", Field: "backends[0].database"}},
		Instance: okdp.Instance{URL: "https://x", Usage: "use it\n", Provides: []okdp.Output{
			{Name: "demo-app", Contract: "http", Values: map[string]any{"url": "https://x"}, SecretRef: &okdp.SecretRef{Name: "creds"}},
		}},
	}
	files, err := Write(res)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		ValuesFile:       generated + "app:\n  b: 2\noidc-dcr:\n  a:\n    b:\n      - 1\n",
		DependenciesFile: "- instance: demo-db\n  contract: database\n  field: backends[0].database\n",
		InstanceFile:     "url: https://x\nusage: |\n  use it\nprovides:\n  - name: demo-app\n    contract: http\n    values:\n      url: https://x\n    secretRef:\n      name: creds\n",
		ObjectsFile:      generated + "data:\n  t: '{{ .x }}'\nkind: ConfigMap\n---\ndata: null\nkind: Secret\n",
		ObjectsTemplate:  "{{- /* OKDP's own objects: okdp.objects.yaml, as okdp-compile wrote it, not templated. */ -}}\n{{ .Files.Get \"okdp.objects.yaml\" }}\n",
	}
	if len(files) != len(want)+1 {
		t.Errorf("got %d files, want %d and Chart.yaml", len(files), len(want))
	}
	for name, w := range want {
		if got := string(files[name]); got != w {
			t.Errorf("%s: got %q, want %q", name, got, w)
		}
	}
	// No dependency, no object, nothing published.
	res.Dependencies, res.Objects, res.Instance = nil, nil, okdp.Instance{}
	if files, err = Write(res); err != nil || string(files[DependenciesFile]) != "[]\n" {
		t.Errorf("no dependency: %v %q", err, files[DependenciesFile])
	}
	if got := string(files[InstanceFile]); got != "url: \"\"\nusage: \"\"\nprovides: []\n" {
		t.Errorf("nothing published: %q", got)
	}
	if _, ok := files[ObjectsFile]; ok || len(files) != 4 {
		t.Errorf("no object: got %v", slices.Sorted(maps.Keys(files)))
	}
}
