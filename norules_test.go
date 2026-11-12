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
	instances, err := compiler.LoadRepository([]compiler.Repository{{Name: name, FS: exampleRepository(name), Role: compiler.RoleTree}})
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

// A compile that gives no rule set: nothing of a platform is applied, and
// claims still bind in their namespace.
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
	for _, tc := range []struct{ name, release, want string }{
		{"no section of a rule set", "demo-workspace", "oidcClients: unknown section"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := compileExample(t, "03_full", nil)[tc.release]
			if o.Err == nil || !strings.Contains(o.Err.Error(), tc.want) {
				t.Errorf("got %v, want %q", o.Err, tc.want)
			}
		})
	}
	t.Run("no secret store objects", func(t *testing.T) {
		o := compileExample(t, "04_claims", nil)["data-postgresql"]
		if o.Err == nil || !strings.Contains(o.Err.Error(), "the rule set of the compile has no") {
			t.Errorf("a claim of another namespace: %v", o.Err)
		}
	})
	t.Run("no policy", func(t *testing.T) {
		def, in := example()
		in.Rules, in.Namespace, in.Release = nil, "kube-system", "kube-system-webapp"
		_, err := compiler.Compile(context.Background(), def, in)
		if err != nil && strings.Contains(err.Error(), "reserved") {
			t.Errorf("no rule set, a namespace refused: %v", err)
		}
		in.Rules = testrules.Standard()
		if _, err := compiler.Compile(context.Background(), def, in); err == nil || !strings.Contains(err.Error(), "reserved") {
			t.Errorf("the standard set, kube-system: %v", err)
		}
	})
}
