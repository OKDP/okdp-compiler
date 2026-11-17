package rules

import (
	"context"
	"io/fs"
	"os"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

// standardDir: a copy of the standard rule set, which okdp-gitops holds.
const standardDir = "../../testdata/rules"

// standardFS: the files of the standard rule set, rules.yaml edited by
// edit.
func standardFS(t *testing.T, edit func(string) string) fstest.MapFS {
	t.Helper()
	sub := os.DirFS(standardDir)
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

// TestLoadRules: the standard set loads; a rule set fails closed.
func TestLoadRules(t *testing.T) {
	r, err := LoadRules(os.DirFS(standardDir))
	if err != nil {
		t.Fatal(err)
	}
	if r.Version() == "" || !r.Has(TableDeniedUpstreamKeys, "hostPath") || r.List(TableEngineNamespaces)[0] != "argocd" {
		t.Errorf("the standard set: %+v", r)
	}
	same := func(s string) string { return s }
	if _, err := LoadRules(standardFS(t, same)); err != nil {
		t.Errorf("the standard set, read again: %v", err)
	}
	for name, tc := range map[string]struct {
		edit func(string) string
		want string
	}{
		"another major": {func(s string) string { return strings.Replace(s, "rules.v1", "rules.v2", 1) }, "apiVersion"},
		"unknown key":   {func(s string) string { return s + "other: 1\n" }, "other"},
		"missing table": {func(s string) string {
			return strings.Replace(s, "  ingressValueKeys:\n    values: [ingress, ingresses]\n", "", 1)
		}, "tables: missing ingressValueKeys"},
		"unknown table": {func(s string) string { return strings.Replace(s, "  ingressValueKeys:\n", "  renamed:\n", 1) }, "tables: unknown renamed"},
		"no mapping schema": {func(s string) string {
			return strings.Replace(s, "schemas:\n  mapping: schemas/mapping.schema.json\n", "", 1)
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
			if _, err := LoadRules(standardFS(t, tc.edit)); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
	if _, err := LoadRules(fstest.MapFS{}); err == nil || !strings.Contains(err.Error(), "the rule set:") {
		t.Errorf("no file: got %v", err)
	}
	bad := standardFS(t, same)
	bad["checks/objects.cel.yaml"] = &fstest.MapFile{Data: []byte("errors: ['${{ nope( }}']\n")}
	if _, err := LoadRules(bad); err == nil || !strings.Contains(err.Error(), "objects.cel.yaml") {
		t.Errorf("a check that does not compile: got %v", err)
	}
}

// TestRulesTables: a rule set's table is what the walkers look for: a key
// the standard set does not deny, denied by another set.
func TestRulesTables(t *testing.T) {
	r, err := LoadRules(standardFS(t, func(s string) string { return strings.Replace(s, "      - hostPath\n", "      - custom\n", 1) }))
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
	fsys := standardFS(t, func(s string) string { return s })
	fsys["checks/objects.cel.yaml"] = &fstest.MapFile{Data: []byte("errors:\n  - ${{ object.missing.deep }}\n")}
	r, err := LoadRules(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Refuse(context.Background(), CheckObjectRule, M{"svc": "svc", "ns": "demo", "object": M{"kind": "ConfigMap"}}); err == nil || !strings.Contains(err.Error(), "the rule set:") {
		t.Errorf("got %v, want the rule set's error", err)
	}
}

// TestMandatory: the mandatory set alone refuses nothing and holds what a
// compile needs to write values.
func TestMandatory(t *testing.T) {
	r := Mandatory()
	ctx := context.Background()
	for _, check := range ruleChecks {
		if err := r.Refuse(ctx, check, M{"ns": "kube-system"}); err != nil {
			t.Errorf("the check %s of the mandatory set refused: %v", check, err)
		}
	}
	if _, err := r.Values(ctx, "deployerRole", M{}); err == nil || !strings.Contains(err.Error(), "no document deployerRole") {
		t.Errorf("a document it does not hold: got %v", err)
	}
	if r.Has(TableEngineNamespaces, "argocd") || len(r.List(TableEngineNamespaces)) != 0 {
		t.Error("the mandatory set holds a platform's table")
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
	if r.MappingSchema() != nil || len(r.Charts()) != 0 {
		t.Error("the mandatory set holds what a platform's set gives")
	}
	in := M{"a": 1}
	if got, err := SchemaValues(r.MappingSchema(), "h", in); err != nil || got["a"] != 1 {
		t.Errorf("values without a schema: %v, %v", got, err)
	}
}

// TestBothSets: a rule set given is added to the mandatory one, and a
// compile applies both: the functions of one, the checks and tables of the
// other, whose documents call each other.
func TestBothSets(t *testing.T) {
	ctx := context.Background()
	fsys := standardFS(t, func(s string) string {
		return strings.Replace(s, "documents:\n", "documents:\n  calling: calling.cel.yaml\n", 1)
	})
	fsys["calling.cel.yaml"] = &fstest.MapFile{Data: []byte("values:\n  store: >-\n    ${{ rule(\"secretStore\", {\"settings\": {}}) }}\n")}
	r, err := LoadRules(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Fragments()) != 4 || r.PlatformSchema() == nil || len(r.List(TableProjectRoles)) != 3 {
		t.Errorf("the mandatory set is not applied: %+v", r)
	}
	if err := r.Refuse(ctx, CheckNamespaceRule, M{"ns": "argocd"}); err == nil {
		t.Error("the check of the set given is not applied")
	}
	if !r.Has(TableEngineNamespaces, "argocd") || r.MappingSchema() == nil || r.Charts()["objects"].Chart == "" {
		t.Errorf("the set given is not applied: %+v", r)
	}
	if v, err := r.Values(ctx, "calling", M{}); err != nil || v["store"] == nil {
		t.Errorf("a document of the set given calling one of the mandatory set: %v, %v", v, err)
	}
	if _, err := r.Values(ctx, "secretStore", M{"settings": M{}}); err != nil {
		t.Errorf("a document of the mandatory set: %v", err)
	}
}

// TestSetsDisjoint: a rule the mandatory set gives is refused in the set
// given: neither replaces the other's.
func TestSetsDisjoint(t *testing.T) {
	inDocuments := func(line string) func(string) string {
		return func(s string) string { return strings.Replace(s, "documents:\n", "documents:\n"+line+"\n", 1) }
	}
	add := func(section string) func(string) string { return func(s string) string { return s + section + "\n" } }
	for name, tc := range map[string]struct {
		edit func(string) string
		want string
	}{
		"table": {func(s string) string {
			return strings.Replace(s, "tables:\n", "tables:\n  projectRoles: {values: [admin]}\n", 1)
		}, "tables: projectRoles"},
		"document": {inDocuments("  scope: objects/deployer-role.cel.yaml"), "documents: scope"},
		"schema": {func(s string) string {
			return strings.Replace(s, "schemas:\n", "schemas:\n  platform: schemas/mapping.schema.json\n", 1)
		}, "schemas: platform"},
		"function": {add("functions: {okdpJob: {document: deployerRole}}"), "functions: okdpJob"},
		"template": {add("templates: {oidcClient: objects/deployer-role.cel.yaml}"), "templates: oidcClient"},
		"section":  {add("sections: [oidcClients]"), "sections: oidcClients"},
		"names":    {add("names: {credentialsSecret: a, connectionSecret: b, claimSecret: c, credentialsLabel: d}"), "names:"},
		"labels":   {add("labels: {selector: {}, component: a, common: {}}"), "labels:"},
		"keepSync": {add("keepSync: {annotation: a, value: b, syncOptions: c, options: []}"), "keepSync:"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := LoadRules(standardFS(t, tc.edit))
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
	if _, err := loadOver(base, standardFS(t, func(s string) string { return s })); err == nil || !strings.Contains(err.Error(), `projectRoles: "owner" is no role`) {
		t.Errorf("got %v", err)
	}
}
