package compiler_test

import (
	"context"
	"encoding/json"
	"errors"
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
	"testing/fstest"

	compiler "github.com/okdp/okdp-compiler"
)

// example: the example package
// (examples/02_advanced/input/catalog/webapp/1.0.0), and the inputs of
// its instance demo-webapp: its config.yaml over platform.yaml, and its
// database's claim, bound to demo-db, with what demo-db publishes
// for it; the rule set the example's platform requires.
func example() (*compiler.Definition, compiler.Inputs) {
	def, err := compiler.LoadDefinition(os.DirFS("examples/02_advanced/input/catalog/webapp/1.0.0"), nil)
	if err != nil {
		panic(err)
	}
	in := compiler.Inputs{
		Rules:     exampleRules("02_advanced"),
		Contracts: os.DirFS("examples/02_advanced/input/contracts"),
		Values:    []map[string]any{values("examples/02_advanced/input/config/platform.yaml"), values("examples/02_advanced/input/config/demo/webapp/config.yaml")},
		Release:   "demo-webapp",
		Namespace: "demo",
		Bindings: map[string]compiler.Binding{"database": {
			Namespace: "demo", Release: "demo-db", Contract: "database", ID: "demo.webapp.database",
			Output: &compiler.Output{
				Name: "demo.webapp.database", Contract: "database",
				Values:    map[string]any{"host": "demo-db-db.demo.svc", "dbName": "demo_webapp_database"},
				SecretRef: &compiler.SecretRef{Name: "demo.webapp.database-credentials"},
			},
		}},
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
// one Definition and the same values layers shared by goroutines, with the
// example's platform and with a layer after it (a proxy): each compile
// gives what it gives alone, and the inputs are not changed. Meaningful
// under go test -race.
func TestConcurrentCompile(t *testing.T) {
	def, plain := example()
	proxied := plain
	proxied.Values = append(slices.Clip(plain.Values), map[string]any{
		"global": map[string]any{"okdp": map[string]any{"proxy": map[string]any{"httpsProxy": "http://proxy.example.org:3128"}}},
	})
	inputs := []compiler.Inputs{proxied, plain}
	snapshot := func() string {
		b, err := json.Marshal([]any{def.Chart, def.Package.Require, def.Package.Defaults, def.Package.Upstream, string(def.Okdp), string(def.Values), proxied.Values, plain.Values})
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

// TestLayers: each stage in its packages, knowing only those before it:
// the compile neither the input documents nor the catalog, the catalog
// not the compile; under the compile, OKDP is not known.
func TestLayers(t *testing.T) {
	const module = "github.com/okdp/okdp-compiler/"
	imports := map[string][]string{} // a package of internal/, by directory: those of the module it imports
	for _, root := range []string{"internal", "chart", "value"} {
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
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
		"internal/okdp":    {"internal/inputs", "internal/catalog"},
		"internal/catalog": {"internal/okdp", "internal/inputs"},
		"internal/compile": {"internal/okdp", "chart"},
		"chart":            {"internal/okdp", "internal/compile"},
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

// TestCheckNamespace: the compile's rule of a namespace, its errors the
// compile's of an instance of the namespace: a name, and the mandatory
// set's namespace hook: none the compiler reserves; another, default among
// them, passes. The namespaces the settings reserve
// (global.okdp.policy.reserved) are the security set's, those of the
// deployer engines a host's set's.
func TestCheckNamespace(t *testing.T) {
	platform := map[string]any{"global": map[string]any{"okdp": map[string]any{"policy": map[string]any{"reserved": []any{"openbao"}}}}}
	for name, want := range map[string]string{
		"demo": "", "default": "", "data": "", "kube": "", "okdp": "", "openbao": "", "argocd": "", "flux-system": "", "okdp-releases": "",
		"Demo":     `namespace "Demo": a namespace name, lowercase letters, digits and "-", starting and ending with a letter or a digit, 63 characters at most`,
		"":         `namespace "": a namespace name`,
		"platform": `namespace "platform": the namespace is reserved: platform/ holds the platform's keys of the secret store, which no project writes`,
		"kube-foo": `namespace "kube-foo": the namespace is reserved: kube-* is Kubernetes' own (kube-system, kube-public, kube-node-lease)`,
	} {
		err := compiler.CheckNamespace(context.Background(), compiler.Mandatory(), name, platform)
		switch {
		case want == "" && err != nil:
			t.Errorf("%q: %v", name, err)
		case want != "" && (err == nil || !strings.HasPrefix(err.Error(), want)):
			t.Errorf("%q: got %v, want %s", name, err, want)
		}
	}
	if err := compiler.CheckNamespace(context.Background(), compiler.Mandatory(), "kube-system", nil); err == nil {
		t.Error("kube-system, no platform: no error")
	}
	// The compile refuses what CheckNamespace refuses, in the same words,
	// Inputs.Namespace for namespace.
	def, in := example()
	example := in.Values[0]
	for _, ns := range []string{"kube-foo", "platform"} {
		in.Namespace, in.Release = ns, ns+"-webapp"
		_, cerr := compiler.Compile(context.Background(), def, in)
		err := compiler.CheckNamespace(context.Background(), compiler.Mandatory(), ns, example)
		if cerr == nil || err == nil || strings.Replace(cerr.Error(), "Inputs.Namespace", "namespace", 1) != err.Error() {
			t.Errorf("%s: compile %v, check %v", ns, cerr, err)
		}
	}
}

// TestLoadInputs: a package no catalog has, and a document of no kind,
// refused together.
func TestLoadInputs(t *testing.T) {
	cat, err := compiler.LoadCatalog([]compiler.CatalogFiles{{Name: "charts", FS: os.DirFS(filepath.Join("examples", "04_claims", "input"))}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = compiler.LoadInputs(t.Context(), []compiler.InputFiles{{Name: "in", FS: fstest.MapFS{
		"a.yaml": {Data: []byte("kind: Instance\nproject: demo\nname: a\nservice: nope\nversion: 1.0.0\n---\nreplicas: 2\n")},
	}}}, cat, nil)
	if err == nil || !errors.Is(err, compiler.ErrNotInCatalog) || !strings.Contains(err.Error(), "in/a.yaml: document 2 (line 7): no kind") {
		t.Errorf("got %v", err)
	}
}
