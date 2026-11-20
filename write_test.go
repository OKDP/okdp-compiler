package compiler

import (
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/okdp/okdp-compiler/chart"
)

// M: a values map.
type M = map[string]any

// keepRules: the mandatory set's.
var keepRules = KeepSync{Annotation: "helm.sh/resource-policy", Value: "keep", SyncOptions: "argocd.argoproj.io/sync-options", Options: []string{"Prune=false", "Delete=false"}}

func TestKeepSynced(t *testing.T) {
	shared := M{keepRules.Annotation: "keep"}
	objects := []map[string]any{
		{"kind": "Database", "metadata": M{"name": "a", "annotations": M{keepRules.Annotation: "keep"}}},
		{"kind": "Secret", "metadata": M{"name": "b", "annotations": M{keepRules.Annotation: " Keep ", keepRules.SyncOptions: "ServerSideApply=true"}}},
		{"kind": "Secret", "metadata": M{"name": "c", "annotations": M{keepRules.Annotation: "keep", keepRules.SyncOptions: "Prune=false, Replace=true"}}},
		{"kind": "Secret", "metadata": M{"name": "d", "annotations": M{keepRules.Annotation: "keep", keepRules.SyncOptions: "Prune=confirm,Delete=false"}}},
		{"kind": "Secret", "metadata": M{"name": "e", "annotations": shared}},
		{"kind": "Secret", "metadata": M{"name": "f", "annotations": shared}},
		{"kind": "Secret", "metadata": M{"name": "g", "annotations": M{keepRules.Annotation: "delete"}}},
		{"kind": "Secret", "metadata": M{"name": "h"}},
		{"kind": "Secret"},
		{"kind": "List", "items": []any{M{"kind": "Database", "metadata": M{"name": "i", "annotations": M{keepRules.Annotation: "keep"}}}}},
	}
	keepSynced(keepRules, objects)
	want := map[string]any{
		"a": "Prune=false,Delete=false",
		"b": "ServerSideApply=true,Prune=false,Delete=false",
		"c": "Prune=false, Replace=true,Delete=false",
		"d": "Prune=confirm,Delete=false",
		"e": "Prune=false,Delete=false",
		"f": "Prune=false,Delete=false",
		"g": nil,
		"h": nil,
	}
	if got := objects[9]["items"].([]any)[0].(M)["metadata"].(M)["annotations"].(M)[keepRules.SyncOptions]; got != "Prune=false,Delete=false" {
		t.Errorf("an item of a List: got %v", got)
	}
	for _, o := range objects {
		meta, _ := o["metadata"].(M)
		name, _ := meta["name"].(string)
		annotations, _ := meta["annotations"].(M)
		if got := annotations[keepRules.SyncOptions]; name != "" && got != want[name] {
			t.Errorf("%s: got %v, want %v", name, got, want[name])
		}
	}
	if !reflect.DeepEqual(shared, M{keepRules.Annotation: "keep"}) {
		t.Errorf("a shared map changed: %v", shared)
	}
	if !reflect.DeepEqual(objects[8], map[string]any{"kind": "Secret"}) {
		t.Errorf("an object without metadata: %v", objects[8])
	}
}

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
