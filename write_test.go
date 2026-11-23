package compiler

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/okdp/okdp-compiler/chart"
)

// M: a values map.
type M = map[string]any

// TestWrite: a plain helm install: no chart, the package's values, a
// dependency the mapping leaves out switched off, the okdp-objects chart's
// values apart.
func TestWrite(t *testing.T) {
	app := chart.Ref{Repository: "https://charts.example.org", Chart: "app", Version: "1.8.0"}
	res := &Result{
		Values:  map[string]map[string]any{"app": {"b": nil}},
		Charts:  map[string]chart.Ref{"app": app},
		Objects: []map[string]any{{"kind": "ConfigMap", "data": map[string]any{"t": "{{ .x }}"}}},
		Chart: chart.Metadata{Dependencies: []chart.Dependency{
			{Name: "app", Repository: app.Repository, Version: app.Version},
			{Name: "opa", Repository: app.Repository, Version: app.Version, Condition: "opa.enabled"},
		}},
	}
	files, err := Write(res)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		ValuesFile:        Generated + "app:\n  b: null\nopa:\n  enabled: false\n",
		ObjectsValuesFile: Generated + "manifests: |\n  data:\n    t: '{{ .x }}'\n  kind: ConfigMap\n",
	}
	if len(files) != len(want) {
		t.Errorf("files %v, want no chart", slices.Sorted(maps.Keys(files)))
	}
	for name, w := range want {
		if got := string(files[name]); got != w {
			t.Errorf("%s: got %q, want %q", name, got, w)
		}
	}
	// Left out without a condition: the chart would render it.
	res.Chart.Dependencies[1].Condition = nil
	if _, err := Write(res); err == nil || !strings.Contains(err.Error(), "condition: opa.enabled") {
		t.Errorf("no condition: got %v", err)
	}
	// Nothing to give: no file.
	if files, err := Write(&Result{}); err != nil || len(files) != 0 {
		t.Errorf("nothing: %v %v", err, files)
	}
}
