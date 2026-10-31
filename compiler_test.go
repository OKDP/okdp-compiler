package compiler_test

import (
	"context"
	"encoding/json"
	"go/parser"
	"go/token"
	"io/fs"
	"maps"
	"os"
	"path"
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
// (examples/02_advanced/input/db/catalog/webapp/1.0.0), and the inputs of
// its instance demo-webapp: its config.yaml over platform.yaml, at level 0,
// and its database's claim, bound to demo-db, with what demo-db publishes
// for it.
func example() (*compiler.Definition, compiler.Inputs) {
	def, err := compiler.LoadDefinition(os.DirFS("examples/02_advanced/input/db/catalog/webapp/1.0.0"))
	if err != nil {
		panic(err)
	}
	in := compiler.Inputs{
		Contracts: os.DirFS("examples/02_advanced/input/db/contracts"),
		Context:   []compiler.ContextLayer{{Values: values("examples/02_advanced/input/db/platform.yaml")}},
		Values:    []map[string]any{values("examples/02_advanced/input/db/projects/demo/instances/webapp/config.yaml")},
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

// TestTreeNames: a tree written with the names the package exports, read
// by LoadRepository: its instance is the release ReleaseName of its
// project and directory, in the project's namespace, and a target writes
// it in InstanceDir, under ProjectDir.
func TestTreeNames(t *testing.T) {
	file := func(s string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(s)} }
	instance := path.Join(compiler.ProjectsDir, "demo", compiler.InstancesDir, "web")
	tree := fstest.MapFS{
		compiler.PlatformFile:                                         file("global: {okdp: {}}\n"),
		path.Join(compiler.ContractsDir, "http.schema.json"):          file(`{"properties": {"url": {"type": "string"}}}`),
		path.Join(compiler.CatalogDir, "web", "1.0.0", "Chart.yaml"):  file("apiVersion: v2\nname: web\nversion: 1.0.0\n"),
		path.Join(compiler.ProjectsDir, "demo", compiler.ProjectFile): file("connections: {api: {contract: http, url: https://api.example.org}}\n"),
		path.Join(instance, compiler.InstanceFile):                    file("service: web\nversion: 1.0.0\n"),
		path.Join(instance, compiler.ConnectionsFile):                 file("connections: {own: {contract: http, url: https://own.example.org}}\n"),
		path.Join(instance, compiler.ConfigFile):                      file("{}\n"),
	}
	instances, err := compiler.LoadRepository([]compiler.Repository{{Name: "db", FS: tree}})
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 1 || len(instances[0].Inputs.Context) != 3 || len(instances[0].Inputs.Values) != 1 {
		t.Fatalf("got %d instances, want one with its 3 context layers and its values", len(instances))
	}
	in := instances[0].Inputs
	if want := compiler.ReleaseName("demo", "web"); in.Release != want || want != "demo-web" || in.Namespace != "demo" {
		t.Errorf("release %s/%s, want demo/%s", in.Namespace, in.Release, want)
	}
	res, err := compiler.Compile(t.Context(), instances[0].Definition, in)
	if err != nil {
		t.Fatal(err)
	}
	g, err := compiler.NewGraph([]*compiler.Result{res})
	if err != nil {
		t.Fatal(err)
	}
	files, err := compiler.Helm{}.Files([]*compiler.Result{res}, g)
	if err != nil {
		t.Fatal(err)
	}
	dir := compiler.InstanceDir("demo", in.Release)
	if _, ok := files[path.Join(dir, "Chart.yaml")]; !ok || dir != path.Join(compiler.ProjectDir("demo"), "instances", "demo-web") || compiler.ProjectDir("demo") != "projects/demo" {
		t.Errorf("files %q, want the umbrella chart in %s", slices.Sorted(maps.Keys(files)), dir)
	}
}
