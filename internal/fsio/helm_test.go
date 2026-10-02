package fsio

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/okdp/okdp-compiler/internal/chart"
	"github.com/okdp/okdp-compiler/internal/okdp"
)

// helmdata: testdata/helm, at the repository root.
var helmdata = filepath.Join("..", "..", "testdata", "helm")

// helmJSON: a JSON file written by generate.sh.
func helmJSON(t *testing.T, path string) any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run testdata/helm/generate.sh)", err)
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return v
}

// sameAsAlone: the values Helm gives a subchart (got) are the values the
// chart sees alone (want), but for what Helm adds to a subchart: a global,
// empty when nothing sets one, and the null defaults nested in the chart's
// maps that the values leave unset, which the chart alone drops.
func sameAsAlone(got, want any, top bool) bool {
	gm, gok := got.(map[string]any)
	wm, wok := want.(map[string]any)
	if !gok || !wok {
		return reflect.DeepEqual(got, want)
	}
	for k, w := range wm {
		if g, ok := gm[k]; !ok || !sameAsAlone(g, w, false) {
			return false
		}
	}
	for k, g := range gm {
		if _, ok := wm[k]; ok {
			continue
		}
		switch {
		case top && k == "global" && reflect.DeepEqual(g, map[string]any{}):
		case !top && g == nil:
		default:
			return false
		}
	}
	return true
}

// TestHelmUmbrella: the umbrella the compiler writes over the charts of the
// values cases (testdata/helm/values/<case>: its values.yaml, its final
// values), each under its case name, nulls and globals a second time under
// an alias: rendered by Helm with its values as user values
// (testdata/helm/umbrella/helm.json), every subchart sees the values the
// case's chart sees alone (helm-final.json), but for what sameAsAlone
// allows.
func TestHelmUmbrella(t *testing.T) {
	dir := filepath.Join(helmdata, "umbrella")
	keys := map[string]string{"nulls-alias": "nulls", "globals-alias": "globals"} // key -> case
	cases, _ := filepath.Glob(filepath.Join(helmdata, "values", "*"))
	for _, c := range cases {
		keys[filepath.Base(c)] = filepath.Base(c)
	}
	def := &okdp.Definition{Service: okdp.Service{Name: "umbrella", Version: "1.0.0"}}
	res := &okdp.Result{Values: map[string]map[string]any{}, Charts: map[string]chart.Ref{}}
	for key, c := range keys {
		b, err := os.ReadFile(filepath.Join(helmdata, "values", c, "final.yaml"))
		if err != nil {
			t.Fatalf("%v (run go test ./internal/chart -run TestHelmValues -update)", err)
		}
		if res.Values[key], err = chart.ParseValues(b); err != nil {
			t.Fatal(err)
		}
		res.Charts[key] = chart.Ref{Repository: "file://../" + c, Chart: c, Version: "1.0.0"}
	}
	files, err := Umbrella(def, res, DefaultIndent)
	if err != nil {
		t.Fatal(err)
	}
	for name, b := range files {
		golden := filepath.Join(dir, name)
		if *update {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(golden, b, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if want, err := os.ReadFile(golden); err != nil || !bytes.Equal(b, want) {
			t.Errorf("%s differs from the umbrella the compiler writes (%v): run go test ./internal/fsio -run TestHelmUmbrella -update, then generate.sh", golden, err)
		}
	}
	if *update {
		return
	}
	helm, ok := helmJSON(t, filepath.Join(dir, "helm.json")).(map[string]any)
	if !ok || len(helm) != len(keys) {
		t.Fatalf("helm.json: one subchart per key expected, got %v (run generate.sh)", helm)
	}
	for key, c := range keys {
		if want := helmJSON(t, filepath.Join(helmdata, "values", c, "helm-final.json")); !sameAsAlone(helm[key], want, true) {
			t.Errorf("%s: Helm renders the subchart differently from the chart alone:\n got %#v\nwant %#v", key, helm[key], want)
		}
	}
}
