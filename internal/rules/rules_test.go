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
		"missing table": {func(s string) string { return strings.Replace(s, "  ingressValueKeys:\n", "  renamed:\n", 1) }, "tables: missing ingressValueKeys"},
		"unknown check": {func(s string) string {
			return strings.Replace(s, "  host: checks/host.cel.yaml\n", "  host: checks/host.cel.yaml\n  extra: checks/objects.cel.yaml\n", 1)
		}, "checks: unknown extra"},
		"missing check": {func(s string) string { return strings.Replace(s, "  release: checks/release.cel.yaml\n", "", 1) }, "checks: missing release"},
		"unknown role":  {func(s string) string { return strings.Replace(s, "[viewer, editor, admin]", "[viewer, owner]", 1) }, `projectRoles: "owner" is no role`},
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

// TestEmpty: the empty set refuses nothing, computes nothing, and keeps what
// claims need.
func TestEmpty(t *testing.T) {
	r := Empty()
	if err := r.Refuse(context.Background(), CheckNamespaceRule, M{"ns": "kube-system"}); err != nil {
		t.Errorf("a check of the empty set refused: %v", err)
	}
	if v, err := r.Values(context.Background(), "scope", M{}); err != nil || len(v) != 0 {
		t.Errorf("a document of the empty set: %v, %v", v, err)
	}
	if r.Has(TableEngineNamespaces, "argocd") || len(r.List(TableProjectRoles)) != 0 {
		t.Error("the empty set holds a platform's table")
	}
	if !r.Whole([]string{"claims", "db"}) || !slices.Contains(r.List(TablePlatformKeysAbove), "provisioning") {
		t.Error("the empty set lost the claim format's tables")
	}
	if got := r.Name("credentialsSecret", "output", "o", "contract", "c"); got != "o-c-credentials" {
		t.Errorf("a credentials Secret of the empty set: %q", got)
	}
	if got := r.Labels("svc", "rel", "web", false); len(got) != 0 {
		t.Errorf("the labels of the empty set: %v", got)
	}
	if r.Keep().Kept(M{"": ""}) {
		t.Error("the empty set keeps an object")
	}
	in := M{"a": 1}
	if got, err := SchemaValues(r.PlatformSchema(), "h", in); err != nil || got["a"] != 1 {
		t.Errorf("values without a schema: %v, %v", got, err)
	}
}
