package compiler

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// render: a helm template output with these objects.
func render(t *testing.T, objects ...M) []byte {
	t.Helper()
	var buf bytes.Buffer
	for _, o := range objects {
		b, err := yaml.Marshal(o)
		if err != nil {
			t.Fatal(err)
		}
		buf.WriteString("---\n")
		buf.Write(b)
	}
	return buf.Bytes()
}

// valuesCM: a values ConfigMap of the wrapper, as okdp.vendor.render writes it.
func valuesCM(t *testing.T, name string, values M) M {
	t.Helper()
	b, err := yaml.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	return M{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata":   M{"name": name, "labels": M{"okdp.io/vendor-values": "app"}},
		"data":       M{"values.yaml": string(b)},
	}
}

// TestCompare: a render that matches the compile, then one difference of each kind.
func TestCompare(t *testing.T) {
	res, err := Compile(def, inputs())
	if err != nil {
		t.Fatal(err)
	}
	desc := res.Objects[0]
	app := res.Values["app"]
	changed := asMap(deepCopy(app))
	changed["replicaCount"] = float64(5)
	for name, tc := range map[string]struct {
		rendered []M
		want     []string // in the report
		failed   bool
	}{
		"identical": {
			[]M{desc, valuesCM(t, "demo-app-app-values", app)},
			[]string{"identical to ConfigMap/demo-app-okdp", "identical to demo-app-app-values"}, false,
		},
		"changed value": {
			[]M{desc, valuesCM(t, "demo-app-app-values", changed)},
			[]string{"DIFF vs demo-app-app-values", "changed .replicaCount: got 1, want 5"}, true,
		},
		"no values ConfigMap": {
			[]M{desc},
			[]string{"the wrapper renders no demo-app-app-values"}, true,
		},
		"values not compiled": {
			[]M{desc, valuesCM(t, "demo-app-app-values", app), valuesCM(t, "demo-app-db-values", M{})},
			[]string{"the wrapper renders demo-app-db-values, not compiled"}, true,
		},
		"no descriptor": {
			[]M{valuesCM(t, "demo-app-app-values", app)},
			[]string{"compiled ConfigMap/demo-app-okdp, the wrapper renders none"}, true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			var report bytes.Buffer
			err := Compare(def, inputs(), res, render(t, tc.rendered...), &report)
			if (err != nil) != tc.failed {
				t.Errorf("error: %v", err)
			}
			for _, w := range tc.want {
				if !strings.Contains(report.String(), w) {
					t.Errorf("report lacks %q:\n%s", w, report.String())
				}
			}
		})
	}
}

// TestCompareNamedValues: the ConfigMap name of a chart, from service.yaml.
func TestCompareNamedValues(t *testing.T) {
	res, err := Compile(def, inputs())
	if err != nil {
		t.Fatal(err)
	}
	d := *def
	d.Service.Charts = map[string]ChartSpec{"app": {Values: "{release}-custom"}}
	var report bytes.Buffer
	rendered := render(t, res.Objects[0], valuesCM(t, "demo-app-custom", res.Values["app"]))
	if err := Compare(&d, inputs(), res, rendered, &report); err != nil {
		t.Errorf("%v\n%s", err, report.String())
	}
}

// TestDiffPaths: one line per differing path.
func TestDiffPaths(t *testing.T) {
	got := diffPaths(
		M{"a": float64(1), "extra": "x", "l": []any{float64(1), float64(2)}, "n": M{"s": "a"}, "short": []any{float64(1)}},
		M{"a": float64(2), "missing": "y", "l": []any{float64(1), float64(3)}, "n": M{"s": "a"}, "short": []any{float64(1), float64(2)}},
		"",
	)
	want := []string{
		"changed .a: got 1, want 2",
		`extra   .extra = "x"`,
		"changed .l[1]: got 2, want 3",
		`missing .missing (want "y")`,
		"changed .short: got []interface {}{1}, want []interface {}{1, 2}",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %#v\nwant %#v", got, want)
	}
	if long := short(strings.Repeat("x", 200)); len(long) != 163 || !strings.HasSuffix(long, "...") {
		t.Errorf("short: %q", long)
	}
}
