package okdp

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/okdp/okdp-compiler/chart"
	ruleset "github.com/okdp/okdp-compiler/internal/rules"
)

// hookRules: testdata/hooks over the mandatory set: an objects hook that
// wants a label on every object, a finalValues hook that bounds every
// count, and a section of its own, notes, whose items add objects.
func hookRules(t *testing.T) *RuleSet {
	t.Helper()
	r, err := LoadRules(os.DirFS("testdata/hooks"))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// hookPackage: a package whose okdp.yaml writes objects and notes, and
// whose values write the count of its parameters.
func hookPackage(okdp string) fstest.MapFS {
	return fstest.MapFS{
		"Chart.yaml":         {Data: []byte("apiVersion: v2\nname: demo\nversion: 1.0.0\ndependencies: [{name: app, repository: https://charts.example.org, version: 1.0.0}]\n")},
		"config.schema.json": {Data: []byte(`{"properties": {"count": {"type": "integer", "default": 3}, "objects": {"type": "array", "default": []}, "notes": {"type": "array", "default": []}}}`)},
		"okdp.yaml":          {Data: []byte(okdp)},
		"values.cel.yaml":    {Data: []byte("app: {count: '${{ values.count }}'}\n")},
	}
}

const hookOkdp = "objects: ${{ values.objects }}\nnotes: ${{ values.notes }}\n"

// labelled: a ConfigMap named name, with the label team when team is set.
func labelled(name, team string) M {
	o := M{"apiVersion": "v1", "kind": "ConfigMap", "metadata": M{"name": name}}
	if team != "" {
		o["metadata"].(M)["labels"] = M{"team": team}
	}
	return o
}

// TestHookObjects: the objects hook on each object of an instance, items
// of a List too, after check object; its message as the document writes
// it; skipped for a privileged project.
func TestHookObjects(t *testing.T) {
	r := hookRules(t)
	d, err := LoadDefinition(hookPackage(hookOkdp), r)
	if err != nil {
		t.Fatal(err)
	}
	compileWith := func(layers ...map[string]any) (*Result, error) {
		in := inputs(layers...)
		in.Rules = r
		return Compile(context.Background(), d, in)
	}
	privileged := M{"global": M{"okdp": M{"policy": M{"privileged": []any{"demo"}}}}}
	for name, tc := range map[string]struct {
		layers []map[string]any
		want   string
	}{
		"labelled": {[]map[string]any{{"objects": []any{labelled("a", "x")}}}, ""},
		"not labelled": {[]map[string]any{{"objects": []any{labelled("a", "x"), labelled("b", "")}}},
			"demo: objects: ConfigMap/b: metadata.labels: no label team in demo"},
		"an item of a List": {[]map[string]any{{"objects": []any{M{"apiVersion": "v1", "kind": "List", "metadata": M{"name": "l", "labels": M{"team": "x"}}, "items": []any{labelled("inner", "")}}}}},
			"demo: objects: ConfigMap/inner: metadata.labels: no label team in demo"},
		"the label of the settings": {[]map[string]any{{"objects": []any{labelled("a", "x")}, "global": M{"okdp": M{"label": "owner"}}}},
			"demo: objects: ConfigMap/a: metadata.labels: no label owner in demo"},
		"check object first": {[]map[string]any{{"objects": []any{M{"apiVersion": "rbac.authorization.k8s.io/v1", "kind": "ClusterRole", "metadata": M{"name": "c"}}}}},
			"demo: objects: ClusterRole/c: a cluster-scoped kind"},
		"privileged": {[]map[string]any{{"objects": []any{labelled("b", "")}}, privileged}, ""},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := compileWith(tc.layers...)
			if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.HasPrefix(err.Error(), tc.want)) {
				t.Errorf("got %v, want %q", err, tc.want)
			}
			if tc.want != "" && !errors.Is(err, ruleset.ErrRefused) {
				t.Errorf("not a refusal: %v", err)
			}
		})
	}
	// A host's objects, through CheckObjects, with the platform's settings.
	ctx := context.Background()
	if err := CheckObjects(ctx, r, "web", "demo", nil, []M{labelled("a", "")}); err == nil || err.Error() != "web: objects: ConfigMap/a: metadata.labels: no label team in demo" {
		t.Errorf("CheckObjects: got %v", err)
	}
	if err := CheckObjects(ctx, r, "web", "demo", M{"label": "owner"}, []M{labelled("a", "x")}); err == nil || err.Error() != "web: objects: ConfigMap/a: metadata.labels: no label owner in demo" {
		t.Errorf("CheckObjects with settings: got %v", err)
	}
	if err := CheckObjects(ctx, r, "web", "demo", M{}, []M{labelled("a", "x")}); err != nil {
		t.Errorf("CheckObjects of a labelled object: %v", err)
	}
	if err := CheckObjects(ctx, Mandatory(), "web", "demo", nil, []M{labelled("a", "")}); err != nil {
		t.Errorf("CheckObjects without hooks: %v", err)
	}
}

// TestHookFinalValues: the finalValues hook on the final values of each
// key, made by Helm's merge or by a Finalize, once, where the Finalize
// calls it or after it; skipped for a privileged project.
func TestHookFinalValues(t *testing.T) {
	r := hookRules(t)
	d, err := LoadDefinition(hookPackage(hookOkdp), r)
	if err != nil {
		t.Fatal(err)
	}
	compileWith := func(f Finalize, layers ...map[string]any) error {
		in := inputs(layers...)
		in.Rules, in.Finalize = r, f
		_, err := Compile(context.Background(), d, in)
		return err
	}
	privileged := M{"global": M{"okdp": M{"policy": M{"privileged": []any{"demo"}}}}}
	refused := "app: count: above the most (demo in demo)"
	// Helm's merge.
	if err := compileWith(nil); err != nil {
		t.Errorf("Helm's merge, a count within bounds: %v", err)
	}
	if err := compileWith(nil, M{"count": 9}); err == nil || err.Error() != refused {
		t.Errorf("Helm's merge: got %v, want %q", err, refused)
	}
	if err := compileWith(nil, M{"count": 9, "global": M{"okdp": M{"maxCount": 10}}}); err != nil {
		t.Errorf("Helm's merge, the most of the settings: %v", err)
	}
	if err := compileWith(nil, M{"count": 9}, privileged); err != nil {
		t.Errorf("Helm's merge, a privileged project: %v", err)
	}
	// A Finalize: the hook runs on what it returns.
	nested := map[string]map[string]any{"app": {"nested": []any{M{"count": 7}}}}
	returns := func(values map[string]map[string]any, err error) Finalize {
		return func(context.Context, FinalInput) (map[string]map[string]any, map[string]chart.Ref, []string, error) {
			return values, map[string]chart.Ref{"app": appSource}, nil, err
		}
	}
	if err := compileWith(returns(nested, nil)); err == nil || err.Error() != "app: nested.0.count: above the most (demo in demo)" {
		t.Errorf("after Finalize: got %v", err)
	}
	if err := compileWith(returns(nested, errors.New("refused by the host"))); err == nil || err.Error() != "refused by the host" {
		t.Errorf("after a Finalize that fails: got %v", err)
	}
	if err := compileWith(returns(nested, nil), privileged); err != nil {
		t.Errorf("after Finalize, a privileged project: %v", err)
	}
	// Outside a compile: keys in order, the first message refusing.
	ctx := context.Background()
	values := map[string]map[string]any{"b": {"count": 9}, "a": {"x": M{"count": 8}}, "c": nil}
	if err := CheckFinalValues(ctx, r, "web", "demo", nil, values); err == nil || err.Error() != "a: x.count: above the most (web in demo)" {
		t.Errorf("CheckFinalValues: got %v", err)
	}
	if err := CheckFinalValues(ctx, r, "web", "demo", M{"maxCount": 9}, values); err != nil {
		t.Errorf("CheckFinalValues with settings: %v", err)
	}
	if err := CheckFinalValues(ctx, Mandatory(), "web", "demo", nil, values); err != nil {
		t.Errorf("CheckFinalValues without hooks: %v", err)
	}
	if err := CheckFinalValues(ctx, nil, "web", "demo", nil, values); !errors.Is(err, ruleset.ErrNotLoaded) {
		t.Errorf("CheckFinalValues without a rule set: got %v", err)
	}
}

// TestOwnObjectsList: the list document of a section of the rule set
// reads privileged and settings, and its first message refuses.
func TestOwnObjectsList(t *testing.T) {
	r := hookRules(t)
	d, err := LoadDefinition(hookPackage(hookOkdp), r)
	if err != nil {
		t.Fatal(err)
	}
	compileWith := func(layers ...map[string]any) (*Result, error) {
		in := inputs(layers...)
		in.Rules = r
		return Compile(context.Background(), d, in)
	}
	note := func(shared bool, text string) M {
		return M{"name": "n", "text": text, "shared": shared, "labels": M{"team": "x"}}
	}
	privileged := M{"global": M{"okdp": M{"policy": M{"privileged": []any{"demo"}}}}}
	res, err := compileWith(M{"notes": []any{note(false, "hello")}})
	if err != nil || len(res.Objects) != 1 || res.Objects[0]["metadata"].(M)["name"] != "demo-app-n" {
		t.Fatalf("a note: %v %v", err, res)
	}
	for name, tc := range map[string]struct {
		layers []map[string]any
		want   string
	}{
		"shared": {[]map[string]any{{"notes": []any{note(false, "a"), note(true, "b")}}},
			"notes[1]: a shared note is the platform's projects' (global.okdp.policy.privileged)"},
		"shared, privileged": {[]map[string]any{{"notes": []any{note(true, "b")}}, privileged}, ""},
		"a word the settings ban": {[]map[string]any{{"notes": []any{note(false, "one,two three")}, "global": M{"okdp": M{"banned": []any{"three"}}}}},
			"notes[0]: a word of global.okdp.banned"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := compileWith(tc.layers...)
			if tc.want == "" && err != nil || tc.want != "" && (err == nil || err.Error() != tc.want) {
				t.Errorf("got %v, want %q", err, tc.want)
			}
		})
	}
}

// TestOkdpSections: okdp.yaml holds the sections of the rule set it is
// read for, and no other key: the mandatory set alone refuses them, with
// the message of any unknown key.
func TestOkdpSections(t *testing.T) {
	r := hookRules(t)
	files := hookPackage("vars: {}\nnotes: []\n")
	if _, err := LoadDefinition(files, r); err != nil {
		t.Errorf("a section of the rule set: %v", err)
	}
	want := "okdp.yaml: yaml: unmarshal errors:\n  line 2: field notes not found in type okdp.Package"
	if _, err := LoadDefinition(files, nil); err == nil || err.Error() != want {
		t.Errorf("the mandatory set: got %v, want %q", err, want)
	}
	files = hookPackage("notes: []\nrequires: [a]\nvar: {}\n")
	want = "okdp.yaml: yaml: unmarshal errors:\n  line 2: field requires not found in type okdp.Package\n  line 3: field var not found in type okdp.Package"
	if _, err := LoadDefinition(files, r); err == nil || err.Error() != want {
		t.Errorf("unknown keys beside a section: got %v, want %q", err, want)
	}
	files = hookPackage("notes: []\nrequire: a\n")
	want = "okdp.yaml: yaml: unmarshal errors:\n  line 2: cannot unmarshal !!str `a` into []string"
	if _, err := LoadDefinition(files, r); err == nil || err.Error() != want {
		t.Errorf("another error beside a section: got %v, want %q", err, want)
	}
	if !slices.Contains(r.Sections(), "notes") {
		t.Errorf("the sections: %v", r.Sections())
	}
}
