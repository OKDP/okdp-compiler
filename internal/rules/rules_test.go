package rules

import (
	"context"
	"errors"
	"io/fs"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

// copyFS: the files of a rule set, rules.yaml edited by edit when it is
// not nil.
func copyFS(t *testing.T, fsys fs.FS, edit func(string) string) fstest.MapFS {
	t.Helper()
	out := fstest.MapFS{}
	if err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(fsys, p)
		out[p] = &fstest.MapFile{Data: b}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if edit != nil {
		out[RulesFile] = &fstest.MapFile{Data: []byte(edit(string(out[RulesFile].Data)))}
	}
	return out
}

// loadEdited loads the mandatory set as edit changes it, alone.
func loadEdited(t *testing.T, edit func(string) string) (*RuleSet, error) {
	t.Helper()
	return loadOver(copyFS(t, mandatoryFS(), edit))
}

// addedSet: a rule set to add to the mandatory one, rules.yaml its header
// then rest; the files of the mandatory set beside it, which it may name.
func addedSet(t *testing.T, rest string) fstest.MapFS {
	t.Helper()
	return copyFS(t, mandatoryFS(), func(string) string { return "apiVersion: okdp-compiler/rules.v1\nname: added\nversion: 1.0.0\n" + rest })
}

// TestLoadRules: a set added to the mandatory one loads; the mandatory set
// read without what the compiler reads, or with a section that is no
// more, fails closed.
func TestLoadRules(t *testing.T) {
	r, err := LoadRules(addedSet(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(r.Hooks(HookNamespace), []string{"namespace"}) || len(r.Hooks(HookRelease)) != 0 {
		t.Errorf("the mandatory set under the set added: %+v", r)
	}
	if _, err := loadEdited(t, nil); err != nil {
		t.Errorf("the mandatory set, read again: %v", err)
	}
	for name, edit := range map[string]func(string) string{
		"another major": func(s string) string { return strings.Replace(s, "rules.v1", "rules.v2", 1) },
		"unknown key":   func(s string) string { return s + "other: 1\n" },
	} {
		fsys := addedSet(t, "")
		fsys[RulesFile] = &fstest.MapFile{Data: []byte(edit(string(fsys[RulesFile].Data)))}
		if _, err := LoadRules(fsys); err == nil {
			t.Errorf("%s: the set added loads", name)
		}
	}
	for name, tc := range map[string]struct {
		edit func(string) string
		want string
	}{
		"no mapping schema": {func(s string) string {
			return strings.Replace(s, "  mapping: schemas/mapping.schema.json\n", "", 1)
		}, "schemas: mapping is required"},
		"no objects chart": {func(s string) string {
			return strings.Replace(s, "charts:\n  objects: {repository: oci://quay.io/okdp/charts, chart: okdp-objects, version: 0.1.0}\n", "", 1)
		}, "charts: objects is required"},
		"a checks section": {func(s string) string {
			return strings.Replace(s, "hooks:\n", "checks:\n  namespace: checks/namespace.cel.yaml\nhooks:\n", 1)
		}, "doesn't meet the specifications of rules.schema.json"},
		"a hook of no document": {func(s string) string { return strings.Replace(s, "  namespace: checks/namespace.cel.yaml\n", "", 1) }, "hooks: namespace: namespace is none of documents"},
		"no such file": {func(s string) string {
			return strings.Replace(s, "checks/namespace.cel.yaml", "checks/none.cel.yaml", 1)
		}, "documents: namespace"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := loadEdited(t, tc.edit); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
	if _, err := LoadRules(fstest.MapFS{}); err == nil || !strings.HasPrefix(err.Error(), "the added rule set 1: open rules.yaml") {
		t.Errorf("no file: got %v", err)
	}
	bad := copyFS(t, mandatoryFS(), nil)
	bad["checks/namespace.cel.yaml"] = &fstest.MapFile{Data: []byte("errors: ['${{ nope( }}']\n")}
	if _, err := loadOver(bad); err == nil || !strings.Contains(err.Error(), "namespace.cel.yaml") {
		t.Errorf("a document that does not compile: got %v", err)
	}
}

// TestRulesTables: a rule set's table is what its documents look for: a
// value the table lists refused, one it no longer lists not.
func TestRulesTables(t *testing.T) {
	fsys := copyFS(t, mandatoryFS(), func(s string) string {
		return strings.Replace(s, "hooks:\n", "tables:\n  held: {values: [custom, other]}\nhooks:\n", 1)
	})
	fsys["checks/namespace.cel.yaml"] = &fstest.MapFile{Data: []byte("errors:\n  - $if: ${{ ns in sets.held }}\n    $value: \"${{ ns }}: held (${{ tables.held.join(', ') }})\"\n")}
	r, err := loadOver(fsys)
	if err != nil {
		t.Fatal(err)
	}
	for ns, want := range map[string]string{
		"custom": "custom: held (custom, other)",
		"argocd": "",
	} {
		err := r.RunHook(context.Background(), HookNamespace, M{"field": "f", "ns": ns, "settings": M{}})
		if want == "" && err != nil || want != "" && (err == nil || !strings.Contains(err.Error(), want)) {
			t.Errorf("%s: got %v, want %q", ns, err, want)
		}
	}
}

// TestCheckFailsClosed: a hook's document that cannot be evaluated
// refuses, it does not pass; so does one that reads a table no set gives.
func TestCheckFailsClosed(t *testing.T) {
	for name, edit := range map[string]func(fstest.MapFS){
		"a missing key": func(fsys fstest.MapFS) {
			fsys["checks/namespace.cel.yaml"] = &fstest.MapFile{Data: []byte("errors:\n  - ${{ settings.missing.deep }}\n")}
		},
		"a missing table": func(fsys fstest.MapFS) {
			fsys["checks/namespace.cel.yaml"] = &fstest.MapFile{Data: []byte("errors:\n  - $if: ${{ ns in sets.missing }}\n    $value: missing\n")}
		},
	} {
		fsys := copyFS(t, mandatoryFS(), nil)
		edit(fsys)
		r, err := loadOver(fsys)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if err := r.RunHook(context.Background(), HookNamespace, M{"field": "f", "ns": "demo", "settings": M{}}); err == nil || errors.Is(err, ErrRefused) || !strings.Contains(err.Error(), "the rule set:") {
			t.Errorf("%s: got %v, want the rule set's error", name, err)
		}
	}
}

// TestMandatory: the mandatory set alone holds what a compile needs to
// write values, and refuses the names OKDP keeps: its hooks apply. It
// refuses no object: the objects hook is the added sets'.
func TestMandatory(t *testing.T) {
	r := Mandatory()
	ctx := context.Background()
	if err := r.RunHook(ctx, HookNamespace, M{"field": "f", "ns": "kube-system", "settings": M{}}); err == nil || err.Error() != `f "kube-system": the namespace is reserved: kube-* is Kubernetes' own (kube-system, kube-public, kube-node-lease)` {
		t.Errorf("the mandatory set and the namespace kube-system: %v", err)
	}
	if err := r.RunHook(ctx, HookNamespace, M{"field": "f", "ns": "demo", "settings": M{}}); err != nil {
		t.Errorf("the mandatory set refuses the namespace demo: %v", err)
	}
	if len(r.Hooks(HookObjects)) != 0 || len(r.Hooks(HookFinalValues)) != 0 {
		t.Errorf("the mandatory set's objects and finalValues hooks: %v %v", r.Hooks(HookObjects), r.Hooks(HookFinalValues))
	}
	if got := r.Name("credentialsSecret", "output", "o", "contract", "c"); got != "o-c-credentials" {
		t.Errorf("a credentials Secret: %q", got)
	}
	if got := r.Labels("svc", "rel", "", true); got["app.kubernetes.io/instance"] != "rel" {
		t.Errorf("the labels: %v", got)
	}
	if f, ok := r.Fragment("okdpGeneratedSecret"); !ok || f.Template.Data == nil || f.Result != "objects" || r.PlatformSchema() == nil {
		t.Errorf("the function okdpGeneratedSecret: %+v", r)
	}
	if len(r.Fragments()) != 1 || len(r.DocumentFuncs()) != 0 || len(r.OwnObjects()) != 0 || len(r.Sections()) != 0 {
		t.Errorf("the mandatory set gives functions besides okdpGeneratedSecret or sections, which only a set added to it gives: %+v", r)
	}
	if r.MappingSchema() == nil || r.Charts()["objects"].Chart != "okdp-objects" {
		t.Error("the mandatory set lost the mapping schema or the objects chart")
	}
}

// TestBothSets: a rule set given is added to the mandatory one, and a
// compile applies both: the mandatory functions, hooks and tables, and the
// documents of the set given, which call the mandatory ones.
func TestBothSets(t *testing.T) {
	ctx := context.Background()
	fsys := addedSet(t, "documents:\n  calling: calling.cel.yaml\n")
	fsys["calling.cel.yaml"] = &fstest.MapFile{Data: []byte("values:\n  store: >-\n    ${{ rule(\"secretStore\", {\"settings\": {}}) }}\n")}
	r, err := LoadRules(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.RunHook(ctx, HookNamespace, M{"field": "f", "ns": "kube-system", "settings": M{}}); err == nil {
		t.Error("the hooks of the mandatory set are not applied")
	}
	if v, err := r.Values(ctx, "calling", M{}); err != nil || v["store"] == nil {
		t.Errorf("a document of the set given calling one of the mandatory set: %v, %v", v, err)
	}
	if _, err := r.Values(ctx, "secretStore", M{"settings": M{}}); err != nil {
		t.Errorf("a document of the mandatory set: %v", err)
	}
}

// TestSetsDisjoint: a rule the mandatory set gives is refused in the set
// given: neither replaces the other's, a hook's document least of all.
// The mandatory set read here also gives a function and a section, which
// the compiler's own gives none of.
func TestSetsDisjoint(t *testing.T) {
	withFunctions := func(s string) string {
		return strings.Replace(s, "functions:\n", "functions:\n  noted: {document: secretStore}\n", 1) + "sections: [notes]\ntemplates: {own: objects/secret-store.cel.yaml}\ntables: {held: {values: [a]}}\n"
	}
	for name, tc := range map[string]struct {
		rest string
		want string
	}{
		"table":    {"tables:\n  held: {values: [admin]}\n", "tables: held"},
		"hook":     {"hooks:\n  namespace: [namespace]\n", "hooks: namespace: namespace"},
		"document": {"documents:\n  scope: objects/secret-store.cel.yaml\n", "documents: scope"},
		"schema":   {"schemas:\n  platform: schemas/mapping.schema.json\n", "schemas: platform"},
		"chart":    {"charts:\n  objects: {repository: oci://example.com/charts, chart: okdp-objects, version: 1.0.0}\n", "charts: objects"},
		"function": {"functions: {noted: {document: secretStore}}\n", "functions: noted"},
		"template": {"templates: {own: objects/secret-store.cel.yaml}\n", "templates: own"},
		"section":  {"sections: [notes]\n", "sections: notes"},
		"names":    {"names: {credentialsSecret: a, connectionSecret: b, claimSecret: c, credentialsLabel: d}\n", "names:"},
		"labels":   {"labels: {selector: {}, component: a, common: {}}\n", "labels:"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := loadOver(copyFS(t, mandatoryFS(), withFunctions), addedSet(t, tc.rest))
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "replaces none") {
				t.Errorf("got %v, want an error of %q", err, tc.want)
			}
		})
	}
}

// TestClosedTable: a set adds no value to a table the sets before it do
// not mark extensible, and gives no values to one they mark so.
func TestClosedTable(t *testing.T) {
	for name, tc := range map[string]struct{ extensible, rest, want string }{
		"closed":     {"", "tables:\n  held:\n    add: [other]\n", "tables: held: add: the table is not extensible"},
		"extensible": {"extensible: true, ", "tables:\n  held:\n    values: [other]\n", "tables: held: given by a rule set read before"},
	} {
		t.Run(name, func(t *testing.T) {
			base := copyFS(t, mandatoryFS(), func(s string) string { return s + "tables: {held: {" + tc.extensible + "values: [a]}}\n" })
			_, err := loadOver(base, addedSet(t, tc.rest))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
}
