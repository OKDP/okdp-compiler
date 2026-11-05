package okdp

import (
	"context"
	"reflect"
	"testing"
)

// TestKeepSynced: an object Helm keeps, helm.sh/resource-policy keep as
// Helm reads it (trimmed, any case), gets Argo CD's Prune=false and
// Delete=false, after the sync options it has, each once, an option of
// that name it sets left as it is; its annotations copied, a map two
// objects share unchanged; an item of a List alike; another object, without keep, without
// annotations or metadata, left as it is.
func TestKeepSynced(t *testing.T) {
	shared := M{resourcePolicy: "keep"}
	objects := []map[string]any{
		{"kind": "Database", "metadata": M{"name": "a", "annotations": M{resourcePolicy: "keep"}}},
		{"kind": "Secret", "metadata": M{"name": "b", "annotations": M{resourcePolicy: " Keep ", syncOptions: "ServerSideApply=true"}}},
		{"kind": "Secret", "metadata": M{"name": "c", "annotations": M{resourcePolicy: "keep", syncOptions: "Prune=false, Replace=true"}}},
		{"kind": "Secret", "metadata": M{"name": "d", "annotations": M{resourcePolicy: "keep", syncOptions: "Prune=confirm,Delete=false"}}},
		{"kind": "Secret", "metadata": M{"name": "e", "annotations": shared}},
		{"kind": "Secret", "metadata": M{"name": "f", "annotations": shared}},
		{"kind": "Secret", "metadata": M{"name": "g", "annotations": M{resourcePolicy: "delete"}}},
		{"kind": "Secret", "metadata": M{"name": "h"}},
		{"kind": "Secret"},
		{"kind": "List", "items": []any{M{"kind": "Database", "metadata": M{"name": "i", "annotations": M{resourcePolicy: "keep"}}}}},
	}
	keepSynced(objects)
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
	if got := objects[9]["items"].([]any)[0].(M)["metadata"].(M)["annotations"].(M)[syncOptions]; got != "Prune=false,Delete=false" {
		t.Errorf("an item of a List: got %v", got)
	}
	for _, o := range objects {
		meta, _ := o["metadata"].(M)
		name, _ := meta["name"].(string)
		annotations, _ := meta["annotations"].(M)
		if got := annotations[syncOptions]; name != "" && got != want[name] {
			t.Errorf("%s: got %v, want %v", name, got, want[name])
		}
	}
	if !reflect.DeepEqual(shared, M{resourcePolicy: "keep"}) {
		t.Errorf("a shared map changed: %v", shared)
	}
	if !reflect.DeepEqual(objects[8], map[string]any{"kind": "Secret"}) {
		t.Errorf("an object without metadata: %v", objects[8])
	}
}

// TestCompileKeep: the objects of a compile that Helm keeps, the
// mapping's, carry Argo CD's sync options; the others do not.
func TestCompileKeep(t *testing.T) {
	d := *def
	d.Okdp = []byte(`
objects:
  - {apiVersion: v1, kind: ConfigMap, metadata: {name: kept, annotations: {helm.sh/resource-policy: keep, argocd.argoproj.io/sync-options: ServerSideApply=true}}}
  - {apiVersion: v1, kind: ConfigMap, metadata: {name: other}}
`)
	res, err := Compile(context.Background(), &d, inputs())
	if err != nil {
		t.Fatal(err)
	}
	var got []any
	for _, o := range res.Objects {
		got = append(got, o["metadata"].(M)["annotations"])
	}
	want := []any{M{resourcePolicy: "keep", syncOptions: "ServerSideApply=true,Prune=false,Delete=false"}, nil}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
