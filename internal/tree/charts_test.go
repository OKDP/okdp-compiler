package tree

import (
	"context"
	"errors"
	"io/fs"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/okdp/okdp-compiler/chart"
	"github.com/okdp/okdp-compiler/internal/okdp"
)

// source: a ChartSource that fetches nothing: the chart cache fsys, and
// why each chart of failed could not be fetched.
type source struct {
	fsys   fs.FS
	failed map[chart.Ref]error
}

func (s source) Ensure(context.Context, []chart.Ref) (fs.FS, map[chart.Ref]error) {
	return s.fsys, s.failed
}

// definition: a package whose Chart.yaml lists deps (name@1.0.0 of
// https://charts.example.org) and that takes upstream values of its charts.
func definition(t *testing.T, deps ...string) *okdp.Definition {
	t.Helper()
	files := fstest.MapFS{}
	chartYAML := "apiVersion: v2\nname: pkg\nversion: 1.0.0\ndependencies:\n"
	for _, d := range deps {
		chartYAML += "  - {name: " + d + ", repository: https://charts.example.org, version: 1.0.0}\n"
	}
	files["Chart.yaml"] = &fstest.MapFile{Data: []byte(chartYAML)}
	files["okdp.yaml"] = &fstest.MapFile{Data: []byte("upstream: true\n")}
	d, err := okdp.LoadDefinition(files)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// ref: the Ref of the chart name, version 1.0.0 of the same repository.
func ref(name string) chart.Ref {
	return chart.Ref{Repository: "https://charts.example.org", Chart: name, Version: "1.0.0"}
}

// instanceOf: an instance of namespace named name of the package d.
func instanceOf(namespace, name string, d *okdp.Definition) okdp.Instance {
	in := instance(namespace, name)
	in.Definition = d
	return in
}

// TestChartFailures: B-5: a chart that cannot be fetched fails per chart:
// the instances that name it are compiled, their keys of it unchecked and
// reported per instance; the keys of a chart that was fetched are checked,
// a violation refusing the instance; the failure of one chart touches no
// instance of another.
func TestChartFailures(t *testing.T) {
	broken := errors.New("the registry is away")
	cache := fstest.MapFS{
		// fetched: its schema requires a key the package does not set.
		"charts.example.org/strict-1.0.0/strict/Chart.yaml":         {Data: []byte("apiVersion: v2\nname: strict\nversion: 1.0.0\n")},
		"charts.example.org/strict-1.0.0/strict/values.schema.json": {Data: []byte(`{"required": ["needed"]}`)},
		// fetched, no schema.
		"charts.example.org/app-1.0.0/app/Chart.yaml": {Data: []byte("apiVersion: v2\nname: app\nversion: 1.0.0\n")},
	}
	src := source{cache, map[chart.Ref]error{ref("away"): broken}}
	strict := definition(t, "strict")
	away := definition(t, "away")
	both := definition(t, "app", "away")
	instances := []okdp.Instance{instanceOf("demo", "strict", strict), instanceOf("demo", "away", away), instanceOf("demo", "both", both)}
	tr := Compile(t.Context(), instances, src)

	if !reflect.DeepEqual(tr.ChartFailures, src.failed) {
		t.Errorf("Tree.ChartFailures: %v", tr.ChartFailures)
	}
	if err := tr.Instance("demo", "strict").Err(); err == nil || !strings.Contains(err.Error(), "needed") {
		t.Errorf("a chart fetched, its schema violated: %v", err)
	}
	if err := tr.Instance("demo", "away").Err(); err != nil {
		t.Errorf("a chart not fetched does not refuse the instance: %v", err)
	}
	if got, want := tr.Instance("demo", "away").ChartFailures, map[string]error{"away": broken}; !reflect.DeepEqual(got, want) {
		t.Errorf("per instance: %v", got)
	}
	if got := tr.Instance("demo", "both").ChartFailures; len(got) != 1 || got["away"] != broken {
		t.Errorf("per key: %v", got)
	}
	if err := tr.Instance("demo", "both").Err(); err != nil {
		t.Errorf("%v", err)
	}
	if tr.Instance("demo", "strict").ChartFailures != nil {
		t.Errorf("a failure of another chart: %v", tr.Instance("demo", "strict").ChartFailures)
	}
}

// TestRequireRender: an instance of a project that is not privileged is
// refused when the host requires rendering and it gives upstream values to
// a chart that could not be fetched; another without upstream values, or
// with them on a chart that was fetched, or of a privileged project, is
// compiled; and without RequireRender it is compiled as well.
func TestRequireRender(t *testing.T) {
	src := source{fstest.MapFS{"charts.example.org/app-1.0.0/app/Chart.yaml": {Data: []byte("apiVersion: v2\nname: app\nversion: 1.0.0\n")}}, map[chart.Ref]error{ref("away"): errors.New("the registry is away")}}
	both := definition(t, "app", "away")
	upstream := func(key string) []map[string]any {
		return []map[string]any{{"upstream": map[string]any{key: map[string]any{"x": 1.0}}}}
	}
	tailored := func(namespace, name string, values []map[string]any) okdp.Instance {
		in := instanceOf(namespace, name, both)
		in.Inputs.Values, in.Inputs.RequireRender = values, true
		return in
	}
	privileged := tailored("privileged", "away", upstream("away"))
	privileged.Inputs.Context = []okdp.ContextLayer{{Values: map[string]any{"global": map[string]any{"okdp": map[string]any{"policy": map[string]any{"privileged": []any{"privileged"}}}}}}}
	notRequired := tailored("demo", "lax", upstream("away"))
	notRequired.Inputs.RequireRender = false
	instances := []okdp.Instance{tailored("demo", "away", upstream("away")), tailored("demo", "app", upstream("app")), tailored("demo", "plain", nil), privileged, notRequired}
	tr := Compile(t.Context(), instances, src)

	if err := tr.Instance("demo", "away").Err(); err == nil || !strings.Contains(err.Error(), "upstream.away") || !strings.Contains(err.Error(), "could not be fetched") {
		t.Errorf("refused: %v", err)
	}
	for _, name := range []string{"app", "plain", "lax"} {
		if err := tr.Instance("demo", name).Err(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := tr.Instance("privileged", "away").Err(); err != nil {
		t.Errorf("privileged: %v", err)
	}
	if tr.Instance("demo", "lax").ChartFailures == nil {
		t.Error("unrefused, its chart failure not reported")
	}
}
