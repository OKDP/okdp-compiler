package rules

import (
	"context"
	"io/fs"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

// mandatoryCopy: the files of the mandatory rule set, rules.yaml edited by
// edit.
func mandatoryCopy(t *testing.T, edit func(string) string) fstest.MapFS {
	t.Helper()
	sub := mandatoryFS()
	out := fstest.MapFS{}
	if err := fs.WalkDir(sub, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(sub, p)
		out[p] = &fstest.MapFile{Data: b}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	out[RulesFile] = &fstest.MapFile{Data: []byte(edit(string(out[RulesFile].Data)))}
	return out
}

// loadEdited loads the mandatory set as edit changes it, alone.
func loadEdited(t *testing.T, edit func(string) string) (*RuleSet, error) {
	t.Helper()
	return loadOver(mandatoryCopy(t, edit), nil)
}

// addedSet: a rule set to add to the mandatory one, rules.yaml its header
// then rest; the files of the mandatory set beside it, which it may name.
func addedSet(t *testing.T, rest string) fstest.MapFS {
	t.Helper()
	return mandatoryCopy(t, func(string) string { return "apiVersion: okdp-compiler/rules.v1\nname: added\nversion: 1.0.0\n" + rest })
}

// TestLoadRules: a set added to the mandatory one loads; the mandatory set
// read with a rule missing or unknown fails closed.
func TestLoadRules(t *testing.T) {
	r, err := LoadRules(addedSet(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	if r.Version() == "" || !r.Has(TableClusterScopedKinds, "/Namespace") || r.List(TableEngineNamespaces)[0] != "argocd" {
		t.Errorf("the mandatory set under the set added: %+v", r)
	}
	same := func(s string) string { return s }
	if _, err := loadEdited(t, same); err != nil {
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
		"missing table": {func(s string) string {
			return strings.Replace(s, "  platformObjectSettings:\n    extensible: true\n    values: [secrets.store, secrets.serviceAccount]\n", "", 1)
		}, "tables: missing platformObjectSettings"},
		"unknown table": {func(s string) string { return strings.Replace(s, "  deployerApiGroups:\n", "  renamed:\n", 1) }, "tables: unknown renamed"},
		"no mapping schema": {func(s string) string {
			return strings.Replace(s, "  mapping: schemas/mapping.schema.json\n", "", 1)
		}, "schemas: mapping is required"},
		"no objects chart": {func(s string) string {
			return strings.Replace(s, "charts:\n  objects: {repository: oci://quay.io/okdp/charts, chart: okdp-objects, version: 0.1.0}\n", "", 1)
		}, "charts: objects is required"},
		"unknown check": {func(s string) string {
			return strings.Replace(s, "  release: checks/release.cel.yaml\n", "  release: checks/release.cel.yaml\n  extra: checks/objects.cel.yaml\n", 1)
		}, "checks: unknown extra"},
		"missing check": {func(s string) string { return strings.Replace(s, "  release: checks/release.cel.yaml\n", "", 1) }, "checks: missing release"},
		"no such file":  {func(s string) string { return strings.Replace(s, "checks/release.cel.yaml", "checks/none.cel.yaml", 1) }, "checks: release"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := loadEdited(t, tc.edit); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
	if _, err := LoadRules(fstest.MapFS{}); err == nil || !strings.Contains(err.Error(), "the rule set:") {
		t.Errorf("no file: got %v", err)
	}
	bad := mandatoryCopy(t, same)
	bad["checks/objects.cel.yaml"] = &fstest.MapFile{Data: []byte("errors: ['${{ nope( }}']\n")}
	if _, err := loadOver(bad, nil); err == nil || !strings.Contains(err.Error(), "objects.cel.yaml") {
		t.Errorf("a check that does not compile: got %v", err)
	}
}

// TestRulesTables: a rule set's table is what the walkers look for: a
// value the table lists refused, one it no longer lists not.
func TestRulesTables(t *testing.T) {
	r, err := loadEdited(t, func(s string) string {
		return strings.Replace(s, "values: [argocd, flux-system, okdp-releases]", "values: [custom, flux-system, okdp-releases]", 1)
	})
	if err != nil {
		t.Fatal(err)
	}
	for ns, want := range map[string]string{
		"custom": "a deployer engine's",
		"argocd": "",
	} {
		err := r.Refuse(context.Background(), CheckNamespaceRule, M{"ns": ns})
		if want == "" && err != nil || want != "" && (err == nil || !strings.Contains(err.Error(), want)) {
			t.Errorf("%s: got %v, want %q", ns, err, want)
		}
	}
}

// TestCheckFailsClosed: a check that cannot be evaluated refuses, it does
// not pass.
func TestCheckFailsClosed(t *testing.T) {
	fsys := mandatoryCopy(t, func(s string) string { return s })
	fsys["checks/objects.cel.yaml"] = &fstest.MapFile{Data: []byte("errors:\n  - ${{ object.missing.deep }}\n")}
	r, err := loadOver(fsys, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Refuse(context.Background(), CheckObjectRule, M{"svc": "svc", "ns": "demo", "object": M{"kind": "ConfigMap"}}); err == nil || !strings.Contains(err.Error(), "the rule set:") {
		t.Errorf("got %v, want the rule set's error", err)
	}
}

// TestExternalIPs: check object refuses a core Service that sets
// spec.externalIPs, and nothing else of its spec.
func TestExternalIPs(t *testing.T) {
	for name, tc := range map[string]struct {
		spec any
		want string
	}{
		"set":       {M{"externalIPs": []any{"10.0.0.1"}}, "svc: objects: Service/s: spec.externalIPs claims addresses"},
		"empty":     {M{"externalIPs": []any{}}, ""},
		"null":      {M{"externalIPs": nil}, ""},
		"none":      {M{"type": "ClusterIP"}, ""},
		"not a map": {"x", ""},
	} {
		object := M{"id": "Service/s", "kind": "Service", "name": "s", "apiVersion": "v1", "group": "", "ns": nil, "raw": M{"spec": tc.spec}}
		err := Mandatory().Refuse(context.Background(), CheckObjectRule, M{"svc": "svc", "ns": "demo", "object": object})
		if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.HasPrefix(err.Error(), tc.want)) {
			t.Errorf("%s: got %v, want %q", name, err, tc.want)
		}
	}
}

// TestMandatory: the mandatory set alone holds what a compile needs to
// write values and refuses what a platform refuses: its checks apply.
func TestMandatory(t *testing.T) {
	r := Mandatory()
	ctx := context.Background()
	if err := r.Refuse(ctx, CheckNamespaceRule, M{"ns": "kube-system"}); err == nil {
		t.Error("the mandatory set accepts the namespace kube-system")
	}
	if err := r.Refuse(ctx, CheckNamespaceRule, M{"ns": "demo"}); err != nil {
		t.Errorf("the mandatory set refuses the namespace demo: %v", err)
	}
	if _, err := r.Values(ctx, "deployerRole", M{}); err != nil && strings.Contains(err.Error(), "no document") {
		t.Errorf("the document deployerRole: %v", err)
	}
	if !r.Has(TableEngineNamespaces, "argocd") || !r.Has(TableClusterScopedKinds, "/Namespace") {
		t.Error("the mandatory set lost the tables of the checks")
	}
	if !slices.Contains(r.List(TablePlatformObjectSettings), "secrets.store") {
		t.Error("the mandatory set lost the table of the settings that name objects")
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
// compile applies both: the mandatory functions, checks and tables, and the
// documents of the set given, which call the mandatory ones.
func TestBothSets(t *testing.T) {
	ctx := context.Background()
	fsys := addedSet(t, "documents:\n  calling: calling.cel.yaml\n")
	fsys["calling.cel.yaml"] = &fstest.MapFile{Data: []byte("values:\n  store: >-\n    ${{ rule(\"secretStore\", {\"settings\": {}}) }}\n")}
	r, err := LoadRules(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Fragment("okdpGeneratedSecret"); !ok || r.PlatformSchema() == nil || !r.Has(TableEngineNamespaces, "argocd") {
		t.Errorf("the mandatory set is not applied: %+v", r)
	}
	if err := r.Refuse(ctx, CheckNamespaceRule, M{"ns": "argocd"}); err == nil {
		t.Error("the checks of the mandatory set are not applied")
	}
	if v, err := r.Values(ctx, "calling", M{}); err != nil || v["store"] == nil {
		t.Errorf("a document of the set given calling one of the mandatory set: %v, %v", v, err)
	}
	if _, err := r.Values(ctx, "secretStore", M{"settings": M{}}); err != nil {
		t.Errorf("a document of the mandatory set: %v", err)
	}
}

// TestSetsDisjoint: a rule the mandatory set gives is refused in the set
// given: neither replaces the other's, a check least of all. The mandatory
// set read here also gives a function and a section, which the compiler's
// own gives none of.
func TestSetsDisjoint(t *testing.T) {
	withFunctions := func(s string) string {
		return strings.Replace(s, "functions:\n", "functions:\n  noted: {document: deployerRole}\n", 1) + "sections: [notes]\ntemplates: {own: objects/deployer-role.cel.yaml}\n"
	}
	for name, tc := range map[string]struct {
		rest string
		want string
	}{
		"table":    {"tables:\n  engineNamespaces: {values: [admin]}\n", "tables: engineNamespaces"},
		"check":    {"checks:\n  namespace: checks/release.cel.yaml\n", "checks: namespace"},
		"document": {"documents:\n  scope: objects/deployer-role.cel.yaml\n", "documents: scope"},
		"schema":   {"schemas:\n  platform: schemas/mapping.schema.json\n", "schemas: platform"},
		"chart":    {"charts:\n  objects: {repository: oci://example.com/charts, chart: okdp-objects, version: 1.0.0}\n", "charts: objects"},
		"function": {"functions: {noted: {document: deployerRole}}\n", "functions: noted"},
		"template": {"templates: {own: objects/deployer-role.cel.yaml}\n", "templates: own"},
		"section":  {"sections: [notes]\n", "sections: notes"},
		"names":    {"names: {credentialsSecret: a, connectionSecret: b, claimSecret: c, credentialsLabel: d}\n", "names:"},
		"labels":   {"labels: {selector: {}, component: a, common: {}}\n", "labels:"},
		"keepSync": {"keepSync: {annotation: a, value: b, syncOptions: c, options: []}\n", "keepSync:"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := loadOver(mandatoryCopy(t, withFunctions), addedSet(t, tc.rest))
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "replaces none") {
				t.Errorf("got %v, want an error of %q", err, tc.want)
			}
		})
	}
}

// TestClosedTable: a set adds no value to a table the sets before it do
// not mark extensible.
func TestClosedTable(t *testing.T) {
	base := mandatoryCopy(t, func(s string) string {
		return strings.Replace(s, "  engineNamespaces:\n    extensible: true\n", "  engineNamespaces:\n", 1)
	})
	_, err := loadOver(base, addedSet(t, "tables:\n  engineNamespaces:\n    add: [other]\n"))
	if want := "tables: engineNamespaces: add: the table is not extensible"; err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("got %v, want an error containing %q", err, want)
	}
}
