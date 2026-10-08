package fsio

import (
	"strings"
	"testing"

	"github.com/okdp/okdp-compiler/internal/chart"
	"github.com/okdp/okdp-compiler/internal/okdp"
)

// TestWrite: the final values of each chart, marked generated, the next
// line naming the upstream chart that renders them as Helm takes it,
// indented by 2 spaces, or as asked; the dependencies as a YAML list, []
// when there is none; an indentation yaml.v3 cannot write, a key that
// would name a file out of the directory, a key without its upstream
// chart, and a chart that would end the comment are refused.
func TestWrite(t *testing.T) {
	app := chart.Ref{Repository: "https://charts.example.org", Chart: "app", Version: "1.8.0"}
	res := &okdp.Result{
		Values:       map[string]map[string]any{"oidc-dcr": {"a": map[string]any{"b": []any{1}}}, "app": {"b": 2}},
		Charts:       map[string]chart.Ref{"oidc-dcr": okdp.DcrSource, "app": app},
		Dependencies: []okdp.Dependency{{Instance: "demo-db", Contract: "database", Field: "backends[0].database"}},
	}
	files, err := Write(res, Output{Dependencies: true})
	if err != nil {
		t.Fatal(err)
	}
	dcr := generated + "# Chart: oci://quay.io/adaltas/oidc-dcr --version 0.5.0\n"
	want := map[string]string{
		"oidc-dcr.values.yaml": dcr + "a:\n  b:\n    - 1\n",
		"app.values.yaml":      generated + "# Chart: app --repo https://charts.example.org --version 1.8.0\nb: 2\n",
		DependenciesFile:       "- instance: demo-db\n  contract: database\n  field: backends[0].database\n",
	}
	if len(files) != len(want) {
		t.Errorf("got %d files, want %d", len(files), len(want))
	}
	for name, w := range want {
		if got := string(files[name]); got != w {
			t.Errorf("%s: got %q, want %q", name, got, w)
		}
	}
	// --indent 4: every file; no dependency.
	res.Dependencies = nil
	if files, err = Write(res, Output{Dependencies: true, Indent: 4}); err != nil || string(files["oidc-dcr.values.yaml"]) != dcr+"a:\n    b:\n        - 1\n" || string(files[DependenciesFile]) != "[]\n" {
		t.Errorf("indent 4: %v %q %q", err, files["oidc-dcr.values.yaml"], files[DependenciesFile])
	}
	// A Result built by a caller.
	for name, tc := range map[string]struct {
		res    *okdp.Result
		indent int
		want   string
	}{
		"indent 1":                   {res, 1, "Output.Indent 1: 2 to 9 spaces, 0 for DefaultIndent"},
		"a key out of the directory": {&okdp.Result{Values: map[string]map[string]any{"../app": {}}}, 0, `chart "../app": a key of the result names a file`},
		"no upstream chart":          {&okdp.Result{Values: map[string]map[string]any{"app": {}}}, 0, `chart "app": not in Result.Charts`},
		"a control character": {&okdp.Result{Values: map[string]map[string]any{"app": {}}, Charts: map[string]chart.Ref{"app": {Repository: "https://x", Chart: "app\nkey: value", Version: "1.0.0"}}}, 0,
			`chart "app": its upstream chart in Result.Charts holds a control character: "app\nkey: value"`},
	} {
		if _, err := Write(tc.res, Output{Indent: tc.indent}); err == nil || !strings.HasPrefix(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want an error starting with %q", name, err, tc.want)
		}
	}
}
