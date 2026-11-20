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
	if r.Version() == "" || !r.Has(TableDeniedUpstreamKeys, "hostPath") || r.List(TableEngineNamespaces)[0] != "argocd" {
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
			return strings.Replace(s, "  ingressValueKeys:\n    values: [ingress, ingresses]\n", "", 1)
		}, "tables: missing ingressValueKeys"},
		"unknown table": {func(s string) string { return strings.Replace(s, "  ingressValueKeys:\n", "  renamed:\n", 1) }, "tables: unknown renamed"},
		"no mapping schema": {func(s string) string {
			return strings.Replace(s, "  mapping: schemas/mapping.schema.json\n", "", 1)
		}, "schemas: mapping is required"},
		"no objects chart": {func(s string) string {
			return strings.Replace(s, "charts:\n  objects: {repository: oci://quay.io/okdp/charts, chart: okdp-objects, version: 0.1.0}\n", "", 1)
		}, "charts: objects is required"},
		"unknown check": {func(s string) string {
			return strings.Replace(s, "  host: checks/host.cel.yaml\n", "  host: checks/host.cel.yaml\n  extra: checks/objects.cel.yaml\n", 1)
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

// TestRulesTables: a rule set's table is what the walkers look for: a key
// the table lists denied, one it no longer lists not.
func TestRulesTables(t *testing.T) {
	r, err := loadEdited(t, func(s string) string { return strings.Replace(s, "      - hostPath\n", "      - custom\n", 1) })
	if err != nil {
		t.Fatal(err)
	}
	added := r.Added(TableDeniedUpstreamKeys, M{"policy": M{"upstream": M{"deny": []any{"added"}}}})
	for key, want := range map[string]string{
		"custom":   "(a pod's privileges",
		"added":    "(global.okdp.policy.upstream.deny)",
		"hostPath": "",
	} {
		err := r.Refuse(context.Background(), CheckUpstreamKey, M{"key": key, "value": true, "at": key, "ns": "demo", "added": added})
		if want == "" && err != nil || want != "" && (err == nil || !strings.Contains(err.Error(), want)) {
			t.Errorf("%s: got %v, want %q", key, err, want)
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
	if !r.Has(TableEngineNamespaces, "argocd") || !r.Has(TableDeniedUpstreamKeys, "hostPath") {
		t.Error("the mandatory set lost the tables of the checks")
	}
	if !r.Whole([]string{"claims", "db"}) || !slices.Contains(r.List(TablePlatformKeysAbove), "provisioning") || len(r.List(TableProjectRoles)) != 3 {
		t.Error("the mandatory set lost the tables of the claim format and of the roles")
	}
	if got := r.Name("credentialsSecret", "output", "o", "contract", "c"); got != "o-c-credentials" {
		t.Errorf("a credentials Secret: %q", got)
	}
	if got := r.Labels("svc", "rel", "", true); got["app.kubernetes.io/instance"] != "rel" {
		t.Errorf("the labels: %v", got)
	}
	if len(r.Fragments()) != 4 || len(r.DocumentFuncs()) != 1 || r.GeneratedSecret().Data == nil || len(r.OwnObjects()) != 1 || r.PlatformSchema() == nil {
		t.Errorf("the functions and templates: %+v", r)
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
	if len(r.Fragments()) != 4 || r.PlatformSchema() == nil || len(r.List(TableProjectRoles)) != 3 {
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
// given: neither replaces the other's, a check least of all.
func TestSetsDisjoint(t *testing.T) {
	for name, tc := range map[string]struct {
		rest string
		want string
	}{
		"table":    {"tables:\n  projectRoles: {values: [admin]}\n", "tables: projectRoles"},
		"check":    {"checks:\n  namespace: checks/release.cel.yaml\n", "checks: namespace"},
		"document": {"documents:\n  scope: objects/deployer-role.cel.yaml\n", "documents: scope"},
		"schema":   {"schemas:\n  platform: schemas/mapping.schema.json\n", "schemas: platform"},
		"chart":    {"charts:\n  objects: {repository: oci://example.com/charts, chart: okdp-objects, version: 1.0.0}\n", "charts: objects"},
		"function": {"functions: {okdpJob: {document: deployerRole}}\n", "functions: okdpJob"},
		"template": {"templates: {oidcClient: objects/deployer-role.cel.yaml}\n", "templates: oidcClient"},
		"section":  {"sections: [oidcClients]\n", "sections: oidcClients"},
		"names":    {"names: {credentialsSecret: a, connectionSecret: b, claimSecret: c, credentialsLabel: d}\n", "names:"},
		"labels":   {"labels: {selector: {}, component: a, common: {}}\n", "labels:"},
		"keepSync": {"keepSync: {annotation: a, value: b, syncOptions: c, options: []}\n", "keepSync:"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := LoadRules(addedSet(t, tc.rest))
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "replaces none") {
				t.Errorf("got %v, want an error of %q", err, tc.want)
			}
		})
	}
}

// TestMandatoryRolesChecked: a role of no target is refused in the set that
// gives it.
func TestMandatoryRolesChecked(t *testing.T) {
	base := fstest.MapFS{}
	sub := mandatoryFS()
	if err := fs.WalkDir(sub, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(sub, p)
		base[p] = &fstest.MapFile{Data: b}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	base[RulesFile] = &fstest.MapFile{Data: []byte(strings.Replace(string(base[RulesFile].Data), "[viewer, editor, admin]", "[viewer, owner]", 1))}
	if _, err := loadOver(base, nil); err == nil || !strings.Contains(err.Error(), `projectRoles: "owner" is no role`) {
		t.Errorf("got %v", err)
	}
}
