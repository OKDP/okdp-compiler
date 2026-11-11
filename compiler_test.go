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
	"github.com/okdp/okdp-compiler/internal/finaltest"
)

// example: the example package
// (examples/02_advanced/input/catalog/webapp), and the inputs of
// its instance demo-webapp: its config.yaml over platform.yaml, at level 0,
// and its database's claim, bound to demo-db, with what demo-db publishes
// for it.
func finalize(in compiler.FinalInput) (map[string]map[string]any, map[string]compiler.ChartRef, []string, error) {
	var cache *finaltest.ChartCache
	if in.Charts != nil {
		cache = finaltest.NewChartCache(in.Charts)
	}
	return finaltest.FinalValues(cache, in.Files, in.Specs, in.ValuesFile, in.Output, in.Values, nil)
}

func example() (*compiler.Definition, compiler.Inputs) {
	def, err := compiler.LoadDefinition(os.DirFS("examples/02_advanced/input/catalog/webapp"))
	if err != nil {
		panic(err)
	}
	in := compiler.Inputs{
		Finalize:  finalize,
		Contracts: os.DirFS("examples/02_advanced/input/contracts"),
		Context:   []compiler.ContextLayer{{Values: values("examples/02_advanced/input/config/platform.yaml")}},
		Values:    []map[string]any{values("examples/02_advanced/input/config/demo/webapp/config.yaml")},
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
		"internal/okdp":    {"internal/graph", "internal/target", "internal/tree"},
		"internal/graph":   {"internal/target", "internal/tree"},
		"internal/tree":    {"internal/target", "internal/repository"},
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

// TestCompileTree: the tree of the claims example compiled: its graph,
// the claims bound to its provider, each with its claimer and what the
// provider granted it, and the namespaces it lets claim of it, its
// instance.yaml's claimers.
func TestCompileTree(t *testing.T) {
	instances, err := compiler.LoadRepository([]compiler.Repository{{Name: "db", FS: exampleRepository("04_claims"), Role: compiler.RoleTree}})
	if err != nil {
		t.Fatal(err)
	}
	tree := compiler.CompileTree(t.Context(), instances, nil)
	if tree.Graph == nil || tree.Problems() != nil {
		t.Fatalf("problems: %q", tree.Problems())
	}
	pg := tree.Instance("data", "postgresql")
	if pg == nil || pg.Release() != "data-postgresql" || !slices.Contains(pg.Provisions(), "database-server") {
		t.Fatalf("the provider: %+v", pg)
	}
	if got := pg.Claimers("database-server"); !slices.Equal(got, []string{"demo"}) {
		t.Errorf("claimers: got %q, want demo", got)
	}
	var claims []string
	for _, p := range pg.Provided {
		if pg.Granted(p.ID) == nil {
			t.Errorf("%s: granted nothing", p.ID)
		}
		claims = append(claims, p.ID+" by "+p.Claimer.Namespace()+"/"+p.Claimer.Name)
	}
	if want := []string{"demo.analytics by demo/dashboards", "demo.analytics by demo/reports", "demo.app.database by demo/app", "demo.metrics.database by demo/metrics"}; !reflect.DeepEqual(claims, want) {
		t.Errorf("claims: got %q, want %q", claims, want)
	}
}

// TestIsName: a DNS-1123 label, 63 characters at most.
func TestIsName(t *testing.T) {
	for name, want := range map[string]bool{
		"demo": true, "demo-app": true, "a1": true, strings.Repeat("a", 63): true,
		"": false, "Demo": false, "-demo": false, "demo-": false, "de_mo": false, "a.b": false, "..": false, "a/b": false, strings.Repeat("a", 64): false,
	} {
		if got := compiler.IsName(name); got != want {
			t.Errorf("%q: got %v", name, got)
		}
	}
}

// TestCheckProjectName: the compile's rule of a project's name, its
// errors the compile's of an instance of the project, the namespace named
// a project's: a name, none the compiler reserves, none of the platform's
// policy.reserved, privileged or not; another, default among them, or
// any without a platform, passes.
func TestCheckProjectName(t *testing.T) {
	platform := map[string]any{"global": map[string]any{"okdp": map[string]any{"policy": map[string]any{"reserved": []any{"openbao", "vault"}, "privileged": []string{"vault", "data"}}}}}
	for name, want := range map[string]string{
		"demo": "", "default": "", "data": "", "kube": "", "okdp": "",
		"Demo":          `project "Demo": a namespace name, lowercase letters, digits and "-", starting and ending with a letter or a digit, 63 characters at most`,
		"":              `project "": a namespace name`,
		"platform":      `project "platform": the namespace is reserved: platform/ holds the platform's keys of the secret store, which no project writes`,
		"kube-foo":      `project "kube-foo": the namespace is reserved: kube-* is Kubernetes' own (kube-system, kube-public, kube-node-lease)`,
		"flux-system":   `project "flux-system": the namespace is reserved: a deployer engine's, or the namespace its target writes in (argocd, flux-system, okdp-releases): a project's releases there would drive the engine`,
		"argocd":        `project "argocd": the namespace is reserved: a deployer engine's`,
		"okdp-releases": `project "okdp-releases": the namespace is reserved: a deployer engine's`,
		"openbao":       `project "openbao": the namespace is reserved by the platform (global.okdp.policy.reserved): no project has it`,
		"vault":         `project "vault": the namespace is reserved by the platform (global.okdp.policy.reserved): no project has it, privileged or not: global.okdp.policy.privileged lists it too, and the reservation wins`,
	} {
		err := compiler.CheckProjectName(name, platform)
		switch {
		case want == "" && err != nil:
			t.Errorf("%q: %v", name, err)
		case want != "" && (err == nil || !strings.HasPrefix(err.Error(), want)):
			t.Errorf("%q: got %v, want %s", name, err, want)
		case strings.HasSuffix(want, "no project has it") && err.Error() != want:
			t.Errorf("%q: got %v, want %s", name, err, want)
		}
	}
	// Without a platform, the compiler's rule alone.
	if err := compiler.CheckProjectName("openbao", nil); err != nil {
		t.Errorf("openbao, no platform: %v", err)
	}
	if err := compiler.CheckProjectName("kube-system", nil); err == nil {
		t.Error("kube-system, no platform: no error")
	}
	// The compile refuses what CheckProjectName refuses, in the same
	// words, Inputs.Namespace for project: the example's instance, its
	// namespace demo, the example's platform reserving it.
	def, in := example()
	example := in.Context[0].Values
	example["global"].(map[string]any)["okdp"].(map[string]any)["policy"] = map[string]any{"reserved": []any{"demo"}, "privileged": []any{"demo"}}
	for _, ns := range []string{"kube-foo", "platform", "demo"} {
		in.Namespace, in.Release = ns, ns+"-webapp"
		_, cerr := compiler.Compile(context.Background(), def, in)
		err := compiler.CheckProjectName(ns, example)
		if cerr == nil || err == nil || strings.Replace(cerr.Error(), "Inputs.Namespace", "project", 1) != err.Error() {
			t.Errorf("%s: compile %v, check %v", ns, cerr, err)
		}
	}
}

// TestBuild: a tree loaded and compiled with the options on each
// instance; one that does not load, a *LoadError of every problem, the
// errors it is of unwrapped.
func TestBuild(t *testing.T) {
	repos := []compiler.Repository{{Name: "db", FS: exampleRepository("04_claims"), Role: compiler.RoleTree}}
	tr, err := compiler.Build(t.Context(), repos, compiler.BuildOptions{CostLimit: 1_000_000, AllowPlainSecrets: true})
	if err != nil {
		t.Fatal(err)
	}
	if problems := tr.Problems(); problems != nil || tr.Graph == nil {
		t.Fatalf("problems: %q", problems)
	}
	for _, ti := range tr.Instances {
		if ti.Inputs.CostLimit != 1_000_000 || !ti.Inputs.AllowPlainSecrets {
			t.Errorf("%s/%s: %+v", ti.Namespace(), ti.Name, ti.Inputs)
		}
	}
	_, err = compiler.Build(t.Context(), []compiler.Repository{{Name: "db", FS: fstest.MapFS{
		"projects/demo/instances/a/instance.yaml": {Data: []byte("service: nope\nversion: 1.0.0\n")},
		"platform.yaml": {Data: []byte("global: [\n")},
	}, Role: compiler.RoleTree}}, compiler.BuildOptions{})
	le, ok := errors.AsType[*compiler.LoadError](err)
	if !ok || len(le.Problems) != 2 || !errors.Is(err, compiler.ErrNotInCatalog) {
		t.Errorf("a tree that does not load: %#v", err)
	}
	// A project that does not load is refused, not the tree: no instance
	// of it, the others compiled, its problems LoadErrorOf's.
	tr, err = compiler.Build(t.Context(), []compiler.Repository{{Name: "db", FS: fstest.MapFS{
		"catalog/app/1.0.0/Chart.yaml":            {Data: []byte("apiVersion: v2\nname: app\nversion: 1.0.0\n")},
		"projects/good/instances/a/instance.yaml": {Data: []byte("service: app\nversion: 1.0.0\n")},
		"projects/bad/instances/a/instance.yaml":  {Data: []byte("service: nope\nversion: 1.0.0\n")},
		"projects/bad/instances/b/instance.yaml":  {Data: []byte("service: app\nversion: 1.0.0\n")},
	}, Role: compiler.RoleTree}}, compiler.BuildOptions{})
	if err != nil || len(tr.Instances) != 1 || tr.Instances[0].Namespace() != "good" || len(tr.Refused) != 1 || tr.Refused[0].Namespace != "bad" {
		t.Fatalf("a project that does not load: %v %+v", err, tr)
	}
	if le := compiler.LoadErrorOf(tr); le == nil || len(le.Problems) != 1 || !errors.Is(le, compiler.ErrNotInCatalog) {
		t.Errorf("LoadErrorOf: %v", le)
	}
	if compiler.LoadErrorOf(nil) != nil || compiler.LoadErrorOf(&compiler.Tree{}) != nil {
		t.Error("LoadErrorOf a tree that loads")
	}
}
