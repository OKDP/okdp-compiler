package umbrella

import (
	"strings"
	"testing"

	"github.com/okdp/okdp-compiler/internal/chart"
	"github.com/okdp/okdp-compiler/internal/okdp"
)

// TestUmbrella: one dependency per compiled key, aliased when the key is
// not the chart's name (one chart under two keys), the values under the
// keys; a version Helm refuses.
func TestUmbrella(t *testing.T) {
	app := chart.Ref{Repository: "https://charts.example.org", Chart: "app", Version: "1.0.0"}
	res := &okdp.Result{
		Values:  map[string]map[string]any{"app": {"a": 1, "global": map[string]any{"g": 1}}, "canary": {"a": nil}},
		Charts:  map[string]chart.Ref{"app": app, "canary": app},
		Service: "demo", Version: "1.2.3",
	}
	files, err := Write(res)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"Chart.yaml": `apiVersion: v2
name: demo
version: 1.2.3
dependencies:
  - name: app
    repository: https://charts.example.org
    version: 1.0.0
  - name: app
    alias: canary
    repository: https://charts.example.org
    version: 1.0.0
`,
		ValuesFile: generated + "app:\n  a: 1\n  global:\n    g: 1\ncanary:\n  a: null\n",
	} {
		if got := string(files[name]); got != want {
			t.Errorf("%s:\n got %q\nwant %q", name, got, want)
		}
	}
	res.Version = "1.x"
	if _, err := Write(res); err == nil || !strings.HasPrefix(err.Error(), `service.yaml: version "1.x": the version of the umbrella chart`) {
		t.Errorf("version: got %v", err)
	}
}

// TestWrite: the umbrella's values, marked generated, and the dependencies
// as a YAML list, [] when there is none, indented by 2 spaces.
func TestWrite(t *testing.T) {
	app := chart.Ref{Repository: "https://charts.example.org", Chart: "app", Version: "1.8.0"}
	res := &okdp.Result{
		Values:       map[string]map[string]any{"oidc-dcr": {"a": map[string]any{"b": []any{1}}}, "app": {"b": 2}},
		Charts:       map[string]chart.Ref{"oidc-dcr": okdp.DcrSource, "app": app},
		Dependencies: []okdp.Dependency{{Instance: "demo-db", Contract: "database", Field: "backends[0].database"}},
		Service:      "demo", Version: "1.0.0",
	}
	files, err := Write(res)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		ValuesFile:       generated + "app:\n  b: 2\noidc-dcr:\n  a:\n    b:\n      - 1\n",
		DependenciesFile: "- instance: demo-db\n  contract: database\n  field: backends[0].database\n",
	}
	if len(files) != len(want)+1 {
		t.Errorf("got %d files, want %d and Chart.yaml", len(files), len(want))
	}
	for name, w := range want {
		if got := string(files[name]); got != w {
			t.Errorf("%s: got %q, want %q", name, got, w)
		}
	}
	// No dependency.
	res.Dependencies = nil
	if files, err = Write(res); err != nil || string(files[DependenciesFile]) != "[]\n" {
		t.Errorf("no dependency: %v %q", err, files[DependenciesFile])
	}
}
