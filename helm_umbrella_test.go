package compiler

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/okdp/okdp-compiler/internal/chart"
	"github.com/okdp/okdp-compiler/internal/helmtest"
)

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

// missing: no value at a path.
type missing struct{}

// umbrellaRender: a render of the umbrella by generate.sh: the Helm
// versions known to render it, and where they give a subchart other values
// than Helm 4 gives its chart alone, beside what sameAsAlone allows: by key,
// the value at a dot-separated path of the key's values, missing{} where
// they delete the key.
type umbrellaRender struct {
	versions string
	known    map[string]map[string]any
}

// umbrellaRenders: the renders of testdata/helm/umbrella, by file; each
// version of testdata/helm/SOURCE in one.
var umbrellaRenders = map[string]umbrellaRender{
	"helm.json": {versions: "4.3.0, 4.2.4, 4.2.1, 4.2.0"},
	// Helm 3 reads the first document of a values.yaml only: the a of the
	// first document of documents/values.yaml, which the last deletes, is
	// back. A key below the top level, null in the chart's values.yaml, that
	// the values set to null: Helm 3 deletes it, Helm 4 keeps it null.
	"helm3.json": {versions: "3.19.4, 3.19.2", known: map[string]map[string]any{
		"documents":   {"a": 1.0},
		"nulls":       {"tbl.defaultNull": missing{}},
		"nulls-alias": {"tbl.defaultNull": missing{}},
	}},
}

// valueAt: the value at path in v, missing{} when there is none.
func valueAt(v any, path []string) any {
	for _, k := range path {
		m, ok := v.(map[string]any)
		if !ok {
			return missing{}
		}
		if v, ok = m[k]; !ok {
			return missing{}
		}
	}
	return v
}

// setAt: x at path in v, deleted for missing{}, when the map that holds it
// exists.
func setAt(v any, path []string, x any) {
	last := len(path) - 1
	m, ok := valueAt(v, path[:last]).(map[string]any)
	switch {
	case !ok:
	case x == missing{}:
		delete(m, path[last])
	default:
		m[path[last]] = x
	}
}

// TestHelmUmbrella: the umbrella the compiler writes over the charts of the
// values cases (testdata/helm/values/<case>: its values.yaml, its final
// values), each under its case name, nulls and globals a second time under
// an alias: rendered by each Helm version with its values as user values
// (testdata/helm/umbrella/helm*.json), every subchart sees the values the
// case's chart sees alone under Helm 4 (helm-final.json), but for what
// sameAsAlone allows and the known differences of the version, each
// checked.
func TestHelmUmbrella(t *testing.T) {
	dir := filepath.Join(helmtest.Dir, "umbrella")
	keys := map[string]string{"nulls-alias": "nulls", "globals-alias": "globals"} // key -> case
	cases, _ := filepath.Glob(filepath.Join(helmtest.Dir, "values", "*"))
	for _, c := range cases {
		keys[filepath.Base(c)] = filepath.Base(c)
	}
	res := &Result{Values: map[string]map[string]any{}, Charts: map[string]chart.Ref{}, Service: "umbrella", Version: "1.0.0"}
	for key, c := range keys {
		b, err := os.ReadFile(filepath.Join(helmtest.Dir, "values", c, "final.yaml"))
		if err != nil {
			t.Fatalf("%v (run go test ./internal/chart -run TestHelmValues -update)", err)
		}
		if res.Values[key], err = chart.ParseValues(b); err != nil {
			t.Fatal(err)
		}
		res.Charts[key] = chart.Ref{Repository: "file://../" + c, Chart: c, Version: "1.0.0"}
	}
	files, err := Write(res, Output{})
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
			t.Errorf("%s differs from the umbrella the compiler writes (%v): run go test . -run TestHelmUmbrella -update, then generate.sh", golden, err)
		}
	}
	if *update {
		return
	}
	renders, _ := filepath.Glob(filepath.Join(dir, "helm*.json"))
	for _, path := range renders {
		if _, ok := umbrellaRenders[filepath.Base(path)]; !ok {
			t.Errorf("%s: a render of no known Helm version: add it to umbrellaRenders", path)
		}
	}
	listed := map[string]string{} // version -> file
	for file, r := range umbrellaRenders {
		for v := range strings.SplitSeq(r.versions, ", ") {
			listed[v] = file
		}
	}
	for _, v := range helmtest.Versions(t) {
		if _, ok := listed[v]; !ok {
			t.Errorf("Helm %s (testdata/helm/SOURCE): add it to the versions of the file it renders in umbrellaRenders", v)
		}
		delete(listed, v)
	}
	for v, file := range listed {
		t.Errorf("Helm %s (umbrellaRenders, %s): not in testdata/helm/SOURCE", v, file)
	}
	for file, r := range umbrellaRenders {
		helm, ok := helmtest.JSON(t, filepath.Join(dir, file)).(map[string]any)
		if !ok || len(helm) != len(keys) {
			t.Fatalf("%s: one subchart per key expected, got %v (run generate.sh)", file, helm)
		}
		for key, c := range keys {
			want := helmtest.JSON(t, filepath.Join(helmtest.Dir, "values", c, "helm-final.json"))
			for p, x := range r.known[key] {
				path := strings.Split(p, ".")
				if got, alone := valueAt(helm[key], path), valueAt(want, path); !reflect.DeepEqual(got, x) || reflect.DeepEqual(alone, x) {
					t.Errorf("%s (Helm %s): %s: %s is %#v, alone %#v: not the known difference, %#v", file, r.versions, key, p, got, alone, x)
				}
				setAt(want, path, x)
			}
			if !sameAsAlone(helm[key], want, true) {
				t.Errorf("%s (Helm %s): %s: Helm renders the subchart differently from the chart alone:\n got %#v\nwant %#v", file, r.versions, key, helm[key], want)
			}
		}
	}
}
