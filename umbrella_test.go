package compiler

import (
	"strings"
	"testing"

	"github.com/okdp/okdp-compiler/internal/chart"
)

// TestUmbrella: one dependency per compiled key, aliased when the key is
// not the chart's name (one chart under two keys), the values under the
// keys; a version Helm refuses.
func TestUmbrella(t *testing.T) {
	app := chart.Ref{Repository: "https://charts.example.org", Chart: "app", Version: "1.0.0"}
	res := &Result{
		Values:  map[string]map[string]any{"app": {"a": 1, "global": map[string]any{"g": 1}}, "canary": {"a": nil}},
		Charts:  map[string]chart.Ref{"app": app, "canary": app},
		Service: "demo", Version: "1.2.3",
	}
	files, err := Write(res, Output{})
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
		UmbrellaValues: generated + "app:\n  a: 1\n  global:\n    g: 1\ncanary:\n  a: null\n",
	} {
		if got := string(files[name]); got != want {
			t.Errorf("%s:\n got %q\nwant %q", name, got, want)
		}
	}
	res.Version = "1.x"
	if _, err := Write(res, Output{}); err == nil || !strings.HasPrefix(err.Error(), `service.yaml: version "1.x": the version of the umbrella chart`) {
		t.Errorf("version: got %v", err)
	}
}
