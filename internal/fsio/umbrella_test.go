package fsio

import (
	"strings"
	"testing"

	"github.com/okdp/okdp-compiler/internal/chart"
	"github.com/okdp/okdp-compiler/internal/okdp"
)

// TestUmbrella: one dependency per compiled key, aliased when the key is
// not the chart's name (one chart under two keys), the values under the
// keys; a version Helm refuses, a key it refuses as an alias.
func TestUmbrella(t *testing.T) {
	app := chart.Ref{Repository: "https://charts.example.org", Chart: "app", Version: "1.0.0"}
	def := &okdp.Definition{Service: okdp.Service{Name: "demo", Version: "1.2.3"}}
	res := &okdp.Result{
		Values: map[string]map[string]any{"app": {"a": 1, "global": map[string]any{"g": 1}}, "canary": {"a": nil}},
		Charts: map[string]chart.Ref{"app": app, "canary": app},
	}
	files, err := Umbrella(def, res, DefaultIndent)
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
		UmbrellaValues: "app:\n  a: 1\n  global:\n    g: 1\ncanary:\n  a: null\n",
	} {
		if got := string(files[name]); got != want {
			t.Errorf("%s:\n got %q\nwant %q", name, got, want)
		}
	}
	for name, tc := range map[string]struct {
		version, key, want string
	}{
		"version": {"1.x", "app", `service.yaml: version "1.x": the version of the umbrella chart`},
		"alias":   {"1.2.3", "my.app", `chart "my.app": a key of the result names a file (<key>.values.yaml) and an umbrella dependency alias`},
	} {
		d := *def
		d.Service.Version = tc.version
		r := &okdp.Result{Values: map[string]map[string]any{tc.key: {}}, Charts: map[string]chart.Ref{tc.key: app}}
		if _, err := Umbrella(&d, r, DefaultIndent); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want an error containing %q", name, err, tc.want)
		}
	}
}
