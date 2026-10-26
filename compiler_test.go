package compiler_test

import (
	"context"
	"encoding/json"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	compiler "github.com/okdp/okdp-compiler"
)

// example: the example package
// (examples/02_advanced/input/db/catalog/webapp/1.0.0), and the inputs of
// its instance demo-webapp: its config.yaml over platform.yaml, at level 0,
// and what demo-db, the database it references, publishes.
func example() (*compiler.Definition, compiler.Inputs) {
	def, err := compiler.LoadDefinition(os.DirFS("examples/02_advanced/input/db/catalog/webapp/1.0.0"))
	if err != nil {
		panic(err)
	}
	in := compiler.Inputs{
		Contracts: os.DirFS("examples/02_advanced/input/db/contracts"),
		Context:   []compiler.ContextLayer{{Values: values("examples/02_advanced/input/db/platform.yaml")}},
		Values:    []map[string]any{values("examples/02_advanced/input/db/projects/demo/instances/demo-webapp/config.yaml")},
		Release:   "demo-webapp",
		Namespace: "demo",
		Published: map[string]compiler.Info{"demo-db": {Provides: []compiler.Output{{
			Name: "demo-db", Contract: "database",
			Values:    map[string]any{"host": "demo-db-db.demo.svc", "dbName": "demo-db"},
			SecretRef: &compiler.SecretRef{Name: "demo-db-database-credentials"},
		}}}},
	}
	return def, in
}

// values: a values file, as a values layer.
func values(file string) map[string]any {
	b, err := os.ReadFile(file)
	if err != nil {
		panic(err)
	}
	v, err := compiler.ParseValues(b)
	if err != nil {
		panic(err)
	}
	return v
}

// TestConcurrentCompile: the concurrent use the documentation promises,
// one Definition and the same values layers shared by goroutines, in the
// dcr mode of the example's platform (the OAuth client chart added to a copy
// of the Definition's charts) and the existing mode, a layer after it: each
// compile gives what it gives alone, and the inputs are not changed.
// Meaningful under go test -race.
func TestConcurrentCompile(t *testing.T) {
	def, dcr := example()
	existing := dcr
	existing.Context = append(slices.Clip(dcr.Context), compiler.ContextLayer{Values: map[string]any{
		"global": map[string]any{"okdp": map[string]any{"oidc": map[string]any{"clientProvisioning": "existing"}}},
	}})
	inputs := []compiler.Inputs{existing, dcr}
	snapshot := func() string {
		b, err := json.Marshal([]any{def.Chart, def.Package.Require, def.Package.Defaults, def.Package.Upstream, string(def.Okdp), string(def.Values), existing.Context, dcr.Values})
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	before := snapshot()
	want := make([]*compiler.Result, len(inputs))
	for i, in := range inputs {
		res, err := compiler.Compile(context.Background(), def, in)
		if err != nil {
			t.Fatal(err)
		}
		want[i] = res
	}
	got := make([]*compiler.Result, 16)
	errs := make([]error, len(got))
	var wg sync.WaitGroup
	for i := range got {
		wg.Go(func() { got[i], errs[i] = compiler.Compile(context.Background(), def, inputs[i%len(inputs)]) })
	}
	wg.Wait()
	for i, res := range got {
		if errs[i] != nil || !reflect.DeepEqual(res, want[i%len(inputs)]) {
			t.Errorf("compile %d (inputs %d): %v, not the result of a compile alone", i, i%len(inputs), errs[i])
		}
	}
	if snapshot() != before {
		t.Error("the Definition or the values layers changed")
	}
}

// TestLayers: each stage of a tree's compile in its packages, knowing only
// those before it: the compile no graph and no target, the graph no
// target; under the compile, OKDP is not known.
func TestLayers(t *testing.T) {
	const module = "github.com/okdp/okdp-compiler/"
	imports := map[string][]string{} // a package of internal/, by directory: those of the module it imports
	err := filepath.WalkDir("internal", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return err
		}
		f, err := parser.ParseFile(token.NewFileSet(), p, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		dir := filepath.ToSlash(filepath.Dir(p))
		for _, imp := range f.Imports {
			if path := strings.Trim(imp.Path.Value, `"`); strings.HasPrefix(path, module) {
				imports[dir] = append(imports[dir], strings.TrimPrefix(path, module))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var deps func(pkg string, seen map[string]bool) map[string]bool
	deps = func(pkg string, seen map[string]bool) map[string]bool {
		for _, d := range imports[pkg] {
			if !seen[d] {
				seen[d] = true
				deps(d, seen)
			}
		}
		return seen
	}
	for pkg, unknown := range map[string][]string{
		"internal/okdp":    {"internal/graph", "internal/target"},
		"internal/graph":   {"internal/target"},
		"internal/compile": {"internal/okdp", "internal/chart"},
		"internal/chart":   {"internal/okdp", "internal/compile"},
	} {
		if imports[pkg] == nil {
			t.Fatalf("%s: no import read", pkg)
		}
		for d := range deps(pkg, map[string]bool{}) {
			for _, u := range unknown {
				if d == u || strings.HasPrefix(d, u+"/") {
					t.Errorf("%s imports %s", pkg, d)
				}
			}
		}
	}
}
