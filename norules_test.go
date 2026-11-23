package compiler_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	compiler "github.com/okdp/okdp-compiler"
)

// A compile that gives no rule set: the compiler's mandatory rules alone,
// its host functions, templates and checks applied, the examples without
// rules of their own compiled as with Mandatory. A call of a function that
// only another rule set gives (okdpPodSpec) fails to load.
func TestCompileWithoutRules(t *testing.T) {
	t.Run("the mandatory set", func(t *testing.T) {
		for _, name := range []string{"00_passthrough", "04_claims"} {
			var got [2][]compiler.Outcome
			for i, rules := range []*compiler.RuleSet{nil, compiler.Mandatory()} {
				instances, err := exampleInstances(name, nil)
				if err != nil {
					t.Fatal(err)
				}
				for j := range instances {
					instances[j].Inputs.Rules = rules
				}
				got[i] = compiler.CompileAll(context.Background(), instances)
				for _, o := range got[i] {
					if o.Err != nil {
						t.Fatalf("%s: %v", name, o.Err)
					}
				}
			}
			if !reflect.DeepEqual(got[0], got[1]) {
				t.Errorf("%s: no rule set does not compile as the mandatory set", name)
			}
		}
	})
	t.Run("a function of a rule set not given", func(t *testing.T) {
		def, in := example()
		in.Rules = nil
		in.Values = []map[string]any{{"global": map[string]any{"okdp": map[string]any{"cluster": map[string]any{"domain": "example.org"}}}}}
		def.Okdp, def.Values = []byte("objects:\n  - $spread: \"${{ [okdpPodSpec({'containers': []})] }}\"\n"), []byte("app: {}\n")
		if _, err := compiler.Compile(context.Background(), def, in); err == nil || !strings.Contains(err.Error(), "undeclared reference to 'okdpPodSpec'") {
			t.Errorf("okdpPodSpec without the set that gives it: %v", err)
		}
	})
}

// TestCheckRendered: what a host checks of the manifests it renders, the
// walk a compile runs on its own objects, through the module root: with
// the mandatory set alone, no hook refuses an object, a cluster-scoped
// kind, another namespace's or a Secret in clear: the policy over them is
// a rule set's (the security set's). A compile without it accepts them.
func TestCheckRendered(t *testing.T) {
	rules := compiler.Mandatory()
	objects := []map[string]any{
		{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"name": "a"}},
		{"apiVersion": "rbac.authorization.k8s.io/v1", "kind": "ClusterRole", "metadata": map[string]any{"name": "a"}},
		{"apiVersion": "v1", "kind": "Secret", "metadata": map[string]any{"name": "s", "namespace": "other"}, "stringData": map[string]any{"password": "p"}},
	}
	if err := compiler.CheckObjects(context.Background(), rules, "web", "demo", nil, false, objects); err != nil {
		t.Errorf("the mandatory set alone: %v", err)
	}
	def, in := example()
	in.Rules = nil
	in.Values = []map[string]any{{"global": map[string]any{"okdp": map[string]any{"cluster": map[string]any{"domain": "example.org"}}}}}
	def.Values = []byte("app: {}\n")
	def.Okdp = []byte("objects:\n  - {apiVersion: rbac.authorization.k8s.io/v1, kind: ClusterRole, metadata: {name: a}}\n  - {apiVersion: v1, kind: Secret, metadata: {name: s, namespace: other}, stringData: {password: p}}\n")
	res, err := compiler.Compile(context.Background(), def, in)
	if err != nil || len(res.Objects) != 2 {
		t.Errorf("a compile without the security set: %v", err)
	}
}
