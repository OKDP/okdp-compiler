package compiler_test

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"

	compiler "github.com/okdp/okdp-compiler"
)

// compileExample: the outcomes of the instances of the example, by release,
// each compiled with rules.
func compileExample(t *testing.T, name string, rules *compiler.RuleSet) map[string]compiler.Outcome {
	t.Helper()
	instances, err := compiler.LoadRepository(context.Background(), []compiler.Repository{{Name: name, FS: exampleRepository(name), Role: compiler.RoleTree}})
	if err != nil {
		t.Fatal(err)
	}
	for i := range instances {
		instances[i].Inputs.Rules = rules
	}
	out := map[string]compiler.Outcome{}
	for i, o := range compiler.CompileAll(context.Background(), instances) {
		out[instances[i].Inputs.Release] = o
	}
	return out
}

// A compile that gives no rule set: the compiler's mandatory rules alone,
// its host functions, templates and checks applied. A call of a function
// that only another rule set gives (okdpPodSpec) fails to load, and a tree
// that requires a rule set the compile was not given is refused.
func TestCompileWithoutRules(t *testing.T) {
	t.Run("values", func(t *testing.T) {
		o := compileExample(t, "00_passthrough", nil)["demo-echo"]
		if o.Err != nil {
			t.Fatal(o.Err)
		}
		files, err := compiler.Write(o.Result)
		if err != nil {
			t.Fatal(err)
		}
		want, err := os.ReadFile("examples/00_passthrough/output/" + compiler.ValuesFile)
		if err != nil {
			t.Fatal(err)
		}
		if string(files[compiler.ValuesFile]) != string(want) {
			t.Errorf("the values:\n%s\nwant:\n%s", files[compiler.ValuesFile], want)
		}
	})
	t.Run("claims bind", func(t *testing.T) {
		o := compileExample(t, "04_claims", nil)["data-postgresql"]
		if o.Err != nil {
			t.Fatal(o.Err)
		}
		if len(o.Result.Claims) == 0 {
			t.Error("no claim was bound to the provider")
		}
	})
	t.Run("the templates of the mandatory set", func(t *testing.T) {
		o := compileExample(t, "04_claims", nil)["data-postgresql"]
		if o.Err != nil {
			t.Fatal(o.Err)
		}
		if !slices.ContainsFunc(o.Result.Objects, func(m map[string]any) bool { return m["kind"] == "Password" }) {
			t.Error("no generated credentials (the template generatedSecret)")
		}
	})
	t.Run("the secret store objects of a claim of another namespace", func(t *testing.T) {
		if o := compileExample(t, "04_claims", nil)["data-postgresql"]; o.Err != nil {
			t.Error(o.Err)
		}
	})
	t.Run("a function of a rule set not given", func(t *testing.T) {
		def, in := example()
		in.Rules, in.Values = nil, nil
		in.Context = []compiler.ContextLayer{{Values: map[string]any{"global": map[string]any{"okdp": map[string]any{"cluster": map[string]any{"domain": "example.org"}}}}}}
		def.Okdp, def.Values = []byte("objects:\n  - $spread: \"${{ [okdpPodSpec({'containers': []})] }}\"\n"), []byte("app: {}\n")
		if _, err := compiler.Compile(context.Background(), def, in); err == nil || !strings.Contains(err.Error(), "undeclared reference to 'okdpPodSpec'") {
			t.Errorf("okdpPodSpec without the set that gives it: %v", err)
		}
	})
	t.Run("a tree that requires a rule set not given", func(t *testing.T) {
		for set, rules := range map[string]*compiler.RuleSet{"example": nil, "security": exampleRules("02_advanced")} {
			def, in := example()
			in.Rules = rules
			in.Context = append(slices.Clip(in.Context), compiler.ContextLayer{Values: map[string]any{
				"global": map[string]any{"okdp": map[string]any{"ruleSets": []any{map[string]any{"name": set}}}},
			}})
			want := "global.okdp.ruleSets: the platform requires the rule set " + set + ", which the compile was not given: give it with okdp-compile --rules (compiler.LoadRules)"
			if _, err := compiler.Compile(context.Background(), def, in); err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("%s: got %v, want %q", set, err, want)
			}
			if _, err := compiler.PlatformOf(context.Background(), in); err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("%s: PlatformOf: got %v, want %q", set, err, want)
			}
		}
	})
	t.Run("the checks of the mandatory set", func(t *testing.T) {
		def, in := example()
		in.Rules, in.Namespace, in.Release = nil, "kube-system", "kube-system-webapp"
		if _, err := compiler.Compile(context.Background(), def, in); err == nil || !strings.Contains(err.Error(), "reserved") {
			t.Errorf("no rule set, kube-system: %v", err)
		}
	})
}

// TestCheckRendered: what a host checks of the manifests it renders, the
// walks a compile runs on its own objects, through the module root: an
// object without a namespace is the release's; a cluster-scoped kind is
// refused, named.
func TestCheckRendered(t *testing.T) {
	rules := compiler.Mandatory()
	if err := compiler.CheckObjects(context.Background(), rules, "web", "demo", nil, []map[string]any{{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"name": "a"}}}); err != nil {
		t.Errorf("an object of the namespace: %v", err)
	}
	err := compiler.CheckObjects(context.Background(), rules, "web", "demo", nil, []map[string]any{{"apiVersion": "rbac.authorization.k8s.io/v1", "kind": "ClusterRole", "metadata": map[string]any{"name": "a"}}})
	if err == nil || !strings.Contains(err.Error(), "web: objects: ClusterRole/a") {
		t.Errorf("a cluster-scoped kind: %v", err)
	}
}
