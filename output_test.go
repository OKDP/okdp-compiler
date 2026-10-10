package compiler

import (
	"testing"

	"github.com/okdp/okdp-compiler/internal/chart"
)

// TestWrite: the umbrella's values, marked generated, and the dependencies
// as a YAML list, [] when there is none, indented by 2 spaces.
func TestWrite(t *testing.T) {
	app := chart.Ref{Repository: "https://charts.example.org", Chart: "app", Version: "1.8.0"}
	res := &Result{
		Values:       map[string]map[string]any{"oidc-dcr": {"a": map[string]any{"b": []any{1}}}, "app": {"b": 2}},
		Charts:       map[string]chart.Ref{"oidc-dcr": DcrSource, "app": app},
		Dependencies: []Dependency{{Instance: "demo-db", Contract: "database", Field: "backends[0].database"}},
		Service:      "demo", Version: "1.0.0",
	}
	files, err := Write(res, Output{Dependencies: true})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		UmbrellaValues:   generated + "app:\n  b: 2\noidc-dcr:\n  a:\n    b:\n      - 1\n",
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
	if files, err = Write(res, Output{Dependencies: true}); err != nil || string(files[DependenciesFile]) != "[]\n" {
		t.Errorf("no dependency: %v %q", err, files[DependenciesFile])
	}
}
