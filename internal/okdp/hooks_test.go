package okdp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/okdp/okdp-compiler/chart"
	ruleset "github.com/okdp/okdp-compiler/internal/rules"
)

// testRules: the rule set of testdata/<dir> over the mandatory set.
func testRules(tb testing.TB, dir string) *RuleSet {
	tb.Helper()
	r, err := LoadRules(os.DirFS(filepath.Join("testdata", dir)))
	if err != nil {
		tb.Fatal(err)
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

// hookCompile: an instance of hookPackage, its okdp.yaml writing the
// objects and notes of its parameters, compiled with the rule set of
// testdata/hooks: objects hooks that want a label on every object and no
// Secret's data, a finalValues hook that bounds every count, namespace and
// release hooks that refuse what the settings list, and a section of its
// own, notes, whose items add objects; f its Finalize, these values
// layers.
func hookCompile(t *testing.T, f Finalize, layers ...map[string]any) (*Result, error) {
	t.Helper()
	in := inputs(layers...)
	in.Finalize = f
	return hookCompileInputs(t, in)
}

// hookCompileInputs compiles hookPackage with in, its rule set testdata/hooks.
func hookCompileInputs(t *testing.T, in Inputs) (*Result, error) {
	t.Helper()
	r := testRules(t, "hooks")
	d, err := LoadDefinition(hookPackage("objects: ${{ values.objects }}\nnotes: ${{ values.notes }}\n"), r)
	if err != nil {
		t.Fatal(err)
	}
	in.Rules = r
	return Compile(context.Background(), d, in)
}

// trusted: a values layer whose settings list demo among the namespaces
// the documents of testdata/hooks exempt (global.okdp.trusted): the
// compiler calls every hook for every project, and each document decides
// whom it exempts.
var trusted = M{"global": M{"okdp": M{"trusted": []any{"demo"}}}}

// labelled: a ConfigMap named name, with the label team when team is set.
func labelled(name, team string) M {
	o := M{"apiVersion": "v1", "kind": "ConfigMap", "metadata": M{"name": name}}
	if team != "" {
		o["metadata"].(M)["labels"] = M{"team": team}
	}
	return o
}

// TestHookObjects: the objects hook on each object of an instance, items
// of a List too, its documents in order; its message as the document
// writes it; called for every project, the document exempting whom it
// will. The compiler refuses no kind of its own.
func TestHookObjects(t *testing.T) {
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
		"a cluster-scoped kind":  {[]map[string]any{{"objects": []any{M{"apiVersion": "rbac.authorization.k8s.io/v1", "kind": "ClusterRole", "metadata": M{"name": "c", "labels": M{"team": "x"}}}}}}, ""},
		"exempt by the document": {[]map[string]any{{"objects": []any{labelled("b", "")}}, trusted}, ""},
		"the documents in order": {[]map[string]any{{"objects": []any{M{"apiVersion": "v1", "kind": "Secret", "metadata": M{"name": "s"}, "data": M{"k": "dg=="}}}}},
			"demo: objects: Secret/s: metadata.labels: no label team in demo"},
		"the second document": {[]map[string]any{{"objects": []any{M{"apiVersion": "v1", "kind": "Secret", "metadata": M{"name": "s", "labels": M{"team": "x"}}, "data": M{"k": "dg=="}}}}},
			"demo: objects: Secret/s: data"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := hookCompile(t, nil, tc.layers...)
			if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.HasPrefix(err.Error(), tc.want)) {
				t.Errorf("got %v, want %q", err, tc.want)
			}
			if tc.want != "" && !errors.Is(err, ruleset.ErrRefused) {
				t.Errorf("not a refusal: %v", err)
			}
		})
	}
	// Inputs.AllowPlainSecrets reaches the documents.
	in := inputs(M{"objects": []any{M{"apiVersion": "v1", "kind": "Secret", "metadata": M{"name": "s", "labels": M{"team": "x"}}, "data": M{"k": "dg=="}}}})
	in.AllowPlainSecrets = true
	if _, err := hookCompileInputs(t, in); err != nil {
		t.Errorf("allowPlainSecrets: %v", err)
	}
	// A host's objects, through CheckObjects, with the platform's settings.
	ctx, r := context.Background(), testRules(t, "hooks")
	if err := CheckObjects(ctx, r, "web", "demo", nil, false, []M{labelled("a", "")}); err == nil || err.Error() != "web: objects: ConfigMap/a: metadata.labels: no label team in demo" {
		t.Errorf("CheckObjects: got %v", err)
	}
	if err := CheckObjects(ctx, r, "web", "demo", M{"label": "owner"}, false, []M{labelled("a", "x")}); err == nil || err.Error() != "web: objects: ConfigMap/a: metadata.labels: no label owner in demo" {
		t.Errorf("CheckObjects with settings: got %v", err)
	}
	if err := CheckObjects(ctx, r, "web", "demo", M{}, false, []M{labelled("a", "x")}); err != nil {
		t.Errorf("CheckObjects of a labelled object: %v", err)
	}
	secret := M{"apiVersion": "v1", "kind": "Secret", "metadata": M{"name": "s", "labels": M{"team": "x"}}, "data": M{"k": "dg=="}}
	if err := CheckObjects(ctx, r, "web", "demo", M{}, false, []M{secret}); err == nil || err.Error() != "web: objects: Secret/s: data" {
		t.Errorf("CheckObjects of a Secret: got %v", err)
	}
	if err := CheckObjects(ctx, r, "web", "demo", M{}, true, []M{secret}); err != nil {
		t.Errorf("CheckObjects of a Secret, allowPlainSecrets: %v", err)
	}
	if err := CheckObjects(ctx, Mandatory(), "web", "demo", nil, false, []M{labelled("a", ""), secret}); err != nil {
		t.Errorf("CheckObjects without hooks: %v", err)
	}
	if err := CheckObjects(ctx, nil, "web", "demo", nil, false, nil); !errors.Is(err, ruleset.ErrNotLoaded) {
		t.Errorf("CheckObjects without a rule set: got %v", err)
	}
}

// TestHookNamespaceRelease: the namespace and release hooks of every set,
// the mandatory set's first, with the settings, in a compile as in
// SettingsOf and CheckNamespace, the same message for each; a namespace
// refused is a *NamespaceError of SettingsOf.
func TestHookNamespaceRelease(t *testing.T) {
	ctx, r := context.Background(), testRules(t, "hooks")
	layer := M{"global": M{"okdp": M{"reserved": []any{"demo", "kube-system"}}}}
	_, err := hookCompile(t, nil, layer)
	if want := `Inputs.Namespace "demo": reserved by settings.reserved`; err == nil || err.Error() != want {
		t.Errorf("Compile: got %v, want %q", err, want)
	}
	in := inputs(layer)
	in.Rules = r
	_, serr := SettingsOf(ctx, in)
	if _, ok := errors.AsType[*NamespaceError](serr); !ok || serr.Error() != err.Error() {
		t.Errorf("SettingsOf: %v", serr)
	}
	if err := CheckNamespace(ctx, r, "namespace", "demo", M{"reserved": []any{"demo"}}); err == nil || err.Error() != `namespace "demo": reserved by settings.reserved` {
		t.Errorf("CheckNamespace: %v", err)
	}
	in.Namespace = "kube-system"
	if _, err := SettingsOf(ctx, in); err == nil || err.Error() != `Inputs.Namespace "kube-system": the namespace is reserved: kube-* is Kubernetes' own (kube-system, kube-public, kube-node-lease)` {
		t.Errorf("the mandatory set's document first: %v", err)
	}
	if _, err := SettingsOf(ctx, inputs(layer)); err != nil {
		t.Errorf("the mandatory set alone: %v", err)
	}
	_, err = hookCompile(t, nil, M{"global": M{"okdp": M{"taken": []any{"demo-app"}}}})
	if want := `Inputs.Release "demo-app": taken by settings.taken`; err == nil || err.Error() != want {
		t.Errorf("the release: got %v, want %q", err, want)
	}
	if _, err := hookCompile(t, nil, M{"global": M{"okdp": M{"taken": []any{"other"}}}}); err != nil {
		t.Errorf("a release not taken: %v", err)
	}
}

// TestHookFinalValues: the finalValues hook on the final values of each
// key, made by Helm's merge or by a Finalize, once, where the Finalize
// calls it or after it; called for every project, the document exempting
// whom it will.
func TestHookFinalValues(t *testing.T) {
	compileWith := func(f Finalize, layers ...map[string]any) error {
		_, err := hookCompile(t, f, layers...)
		return err
	}
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
	if err := compileWith(nil, M{"count": 9}, trusted); err != nil {
		t.Errorf("Helm's merge, a project the document exempts: %v", err)
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
	if err := compileWith(returns(nested, nil), trusted); err != nil {
		t.Errorf("after Finalize, a project the document exempts: %v", err)
	}
	// Outside a compile: keys in order, the first message refusing.
	ctx, r := context.Background(), testRules(t, "hooks")
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
// reads the release and the settings, and its first message refuses.
func TestOwnObjectsList(t *testing.T) {
	note := func(shared bool, text string) M {
		return M{"name": "n", "text": text, "shared": shared, "labels": M{"team": "x"}}
	}
	res, err := hookCompile(t, nil, M{"notes": []any{note(false, "hello")}})
	if err != nil || len(res.Objects) != 1 || res.Objects[0]["metadata"].(M)["name"] != "demo-app-n" {
		t.Fatalf("a note: %v %v", err, res)
	}
	for name, tc := range map[string]struct {
		layers []map[string]any
		want   string
	}{
		"shared": {[]map[string]any{{"notes": []any{note(false, "a"), note(true, "b")}}},
			"notes[1]: a shared note is a trusted namespace's (global.okdp.trusted)"},
		"shared, trusted": {[]map[string]any{{"notes": []any{note(true, "b")}}, trusted}, ""},
		"a word the settings ban": {[]map[string]any{{"notes": []any{note(false, "one,two three")}, "global": M{"okdp": M{"banned": []any{"three"}}}}},
			"notes[0]: a word of global.okdp.banned"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := hookCompile(t, nil, tc.layers...)
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
	r := testRules(t, "hooks")
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
