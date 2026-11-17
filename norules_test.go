package compiler_test

import (
	"context"
	"os"
	"strings"
	"testing"

	compiler "github.com/okdp/okdp-compiler"
	"github.com/okdp/okdp-compiler/internal/testrules"
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
// its functions, templates and checks applied.
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
		o := compileExample(t, "03_full", nil)["demo-db"]
		if o.Err != nil {
			t.Fatal(o.Err)
		}
		if len(o.Result.Claims) == 0 {
			t.Error("no claim was bound to the provider")
		}
	})
	t.Run("the sections and functions of the mandatory set", func(t *testing.T) {
		if o := compileExample(t, "03_full", nil)["demo-workspace"]; o.Err != nil {
			t.Error(o.Err)
		}
	})
	t.Run("the secret store objects of a claim of another namespace", func(t *testing.T) {
		if o := compileExample(t, "04_claims", nil)["data-postgresql"]; o.Err != nil {
			t.Error(o.Err)
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
// object without a namespace is the release's; a cluster-scoped kind, an
// object of another namespace and a host the project may not serve are
// refused, named.
func TestCheckRendered(t *testing.T) {
	rules := testrules.Standard()
	settings := map[string]any{"ingress": map[string]any{"suffix": "example.org"}}
	if err := compiler.CheckObjects(context.Background(), rules, "web", "demo", []map[string]any{{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"name": "a"}}}); err != nil {
		t.Errorf("an object of the namespace: %v", err)
	}
	err := compiler.CheckObjects(context.Background(), rules, "web", "demo", []map[string]any{{"apiVersion": "rbac.authorization.k8s.io/v1", "kind": "ClusterRole", "metadata": map[string]any{"name": "a"}}})
	if err == nil || !strings.Contains(err.Error(), "web: objects: ClusterRole/a") {
		t.Errorf("a cluster-scoped kind: %v", err)
	}
	ingress := func(host string) []map[string]any {
		return []map[string]any{{"apiVersion": "networking.k8s.io/v1", "kind": "Ingress", "metadata": map[string]any{"name": "i"}, "spec": map[string]any{"rules": []any{map[string]any{"host": host}}}}}
	}
	if err := compiler.CheckIngressHosts(context.Background(), rules, "web", "demo", settings, ingress("web.demo.example.org")); err != nil {
		t.Errorf("the project's host: %v", err)
	}
	err = compiler.CheckIngressHosts(context.Background(), rules, "web", "demo", settings, ingress("sso.example.org"))
	if err == nil || !strings.Contains(err.Error(), `web: objects: Ingress/i: spec.rules[0].host: host "sso.example.org"`) {
		t.Errorf("a host: %v", err)
	}
}
