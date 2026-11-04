package compiler_test

import (
	"context"
	"encoding/json"
	"errors"
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
		"internal/okdp":    {"internal/graph", "internal/target", "internal/tree"},
		"internal/graph":   {"internal/target", "internal/tree"},
		"internal/tree":    {"internal/target", "internal/repository"},
		"internal/output":  {"internal/repository"},
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
	instances, err := compiler.LoadRepository([]compiler.Repository{{Name: "db", FS: tree, Role: compiler.RoleTree}})
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

// TestCompileTree: the tree of the claims example compiled: its graph,
// the claims bound to its provider, each with its claimer and what the
// provider granted it, and the namespaces it lets claim of it, its
// instance.yaml's claimers.
func TestCompileTree(t *testing.T) {
	instances, err := compiler.LoadRepository([]compiler.Repository{{Name: "db", FS: os.DirFS("examples/04_claims/input/db"), Role: compiler.RoleTree}})
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

// charts: a ChartSource that records what it is asked, and answers fsys,
// or err.
type charts struct {
	refs []compiler.ChartRef
	fsys fs.FS
	err  error
}

func (c *charts) Ensure(_ context.Context, refs []compiler.ChartRef) (fs.FS, error) {
	c.refs = refs
	return c.fsys, c.err
}

// TestCompileTreeCharts: the charts the compile names fetched once each,
// then the values checked against them; unchecked, and why, when one
// cannot be fetched.
func TestCompileTreeCharts(t *testing.T) {
	instances, err := compiler.LoadRepository([]compiler.Repository{{Name: "db", FS: os.DirFS("examples/04_claims/input/db"), Role: compiler.RoleTree}})
	if err != nil {
		t.Fatal(err)
	}
	away := &charts{err: errors.New("the registry is away")}
	tree := compiler.CompileTree(t.Context(), instances, away)
	if !errors.Is(tree.ChartsErr, away.err) || tree.Problems() != nil || tree.Graph == nil {
		t.Errorf("unchecked: ChartsErr %v, problems %q", tree.ChartsErr, tree.Problems())
	}
	if len(away.refs) == 0 || len(slices.Compact(slices.Clone(away.refs))) != len(away.refs) {
		t.Errorf("refs: %v", away.refs)
	}
	empty := &charts{fsys: fstest.MapFS{}}
	tree = compiler.CompileTree(t.Context(), instances, empty)
	if tree.ChartsErr != nil || tree.Problems() == nil {
		t.Errorf("checked against a cache without the charts: ChartsErr %v, no problem", tree.ChartsErr)
	}
	if !reflect.DeepEqual(empty.refs, away.refs) {
		t.Errorf("refs: %v, then %v", away.refs, empty.refs)
	}
	for _, inst := range instances {
		if inst.Inputs.Charts != nil {
			t.Fatal("the instances given were changed")
		}
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
	repos := []compiler.Repository{{Name: "db", FS: os.DirFS("examples/04_claims/input/db"), Role: compiler.RoleTree}}
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
}

// TestAccess: project.yaml's access, read from the project's file alone,
// written by the targets as the Argo CD roles of its AppProject and the
// RoleBindings of its namespace, the platform's group prefix on the
// latter; the accessors a control plane mirrors them with.
func TestAccess(t *testing.T) {
	file := func(s string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(s)} }
	platform := "global: {okdp: {access: {admins: [platform_admin], groupPrefix: 'oidc:'}}}\n"
	project := "description: the demo\naccess:\n  okdp-project-demo: admin\n  analysts: viewer\n"
	tree := fstest.MapFS{
		compiler.PlatformFile: file(platform),
		path.Join(compiler.CatalogDir, "web", "1.0.0", "Chart.yaml"):                           file("apiVersion: v2\nname: web\nversion: 1.0.0\n"),
		path.Join(compiler.ProjectsDir, "demo", compiler.ProjectFile):                          file(project),
		path.Join(compiler.ProjectsDir, "demo", compiler.InstancesDir, "web", "instance.yaml"): file("service: web\nversion: 1.0.0\n"),
		path.Join(compiler.ProjectsDir, "ops", compiler.InstancesDir, "web", "instance.yaml"):  file("service: web\nversion: 1.0.0\n"),
	}
	tr, err := compiler.Build(t.Context(), []compiler.Repository{{Name: "db", FS: tree, Role: compiler.RoleTree}}, compiler.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if problems := tr.Problems(); problems != nil {
		t.Fatal(problems)
	}
	files, _, err := compiler.TreeFiles(tr, []compiler.Target{compiler.ArgoCD{RepoURL: "https://git.example.org/o.git"}, compiler.Flux{}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	demo := string(files[path.Join(compiler.ProjectManifestsDir("demo"), "access.yaml")])
	app := string(files[path.Join(compiler.ProjectManifestsDir("demo"), "appproject.yaml")])
	ops := string(files[path.Join(compiler.ProjectManifestsDir("ops"), "access.yaml")])
	for _, want := range []string{"name: okdp-access-viewer", `name: "oidc:analysts"`, "name: okdp-access-admin", `name: "oidc:okdp-project-demo"`} {
		if !strings.Contains(demo, want) {
			t.Errorf("demo's access.yaml lacks %q:\n%s", want, demo)
		}
	}
	if !strings.Contains(app, "p, proj:project-demo:admin, applications, override, project-demo/*, allow") || !strings.Contains(app, `- "okdp-project-demo"`) || strings.Contains(app, "oidc:") {
		t.Errorf("demo's AppProject:\n%s", app)
	}
	if !strings.Contains(ops, "name: okdp-access-editor") || !strings.Contains(ops, `name: "oidc:okdp-project-ops"`) {
		t.Errorf("ops's access.yaml, the default:\n%s", ops)
	}
	for p, b := range files {
		if strings.Contains(p, "/instances/") && strings.Contains(string(b), "analysts") {
			t.Errorf("%s holds the access", p)
		}
	}
	got, err := compiler.ProjectAccess([]byte(project), "demo")
	if want := map[string]compiler.AccessRole{"okdp-project-demo": compiler.AccessAdmin, "analysts": compiler.AccessViewer}; err != nil || !maps.Equal(got, want) {
		t.Errorf("ProjectAccess: %v, %v", got, err)
	}
	values, err := compiler.ParseValues([]byte(platform))
	if err != nil {
		t.Fatal(err)
	}
	if admins, err := compiler.PlatformAdmins(values); err != nil || !slices.Equal(admins, []string{"platform_admin"}) {
		t.Errorf("PlatformAdmins: %v, %v", admins, err)
	}
	if compiler.DefaultProjectGroup("demo") != "okdp-project-demo" || !compiler.IsGroupName("platform_admin") || compiler.IsGroupName("/platform_admin") {
		t.Error("DefaultProjectGroup, IsGroupName")
	}
}

// TestPartialRederives: a project kept (an instance of it refused) keeps
// its instances as the previous output holds them, but its platform's
// part is today's (D-5, CMP-07): a privilege revoked, its Namespace
// baseline, its identity confined again, its kept HelmRelease impersonating
// it, its AppProject confined; a role taken back, its RoleBinding gone.
func TestPartialRederives(t *testing.T) {
	file := func(s string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(s)} }
	tree := fstest.MapFS{
		compiler.PlatformFile:                    file("global: {okdp: {policy: {privileged: [ops]}}}\n"),
		"catalog/admin/1.0.0/Chart.yaml":         file("apiVersion: v2\nname: admin\nversion: 1.0.0\ndependencies:\n  - {name: hello, alias: app, repository: https://charts.example.org, version: 1.0.0}\n"),
		"catalog/admin/1.0.0/okdp.yaml":          file("objects:\n  - {apiVersion: rbac.authorization.k8s.io/v1, kind: ClusterRoleBinding, metadata: {name: ops-admin}, roleRef: {apiGroup: rbac.authorization.k8s.io, kind: ClusterRole, name: cluster-admin}, subjects: [{kind: ServiceAccount, name: default, namespace: ops}]}\n"),
		"catalog/admin/1.0.0/config.schema.json": file(`{"type": "object", "properties": {"n": {"type": "integer"}}}`),
		"projects/ops/project.yaml":              file("access: {ops-team: admin}\n"),
		"projects/ops/instances/x/instance.yaml": file("service: admin\nversion: 1.0.0\n"),
	}
	targets := []compiler.Target{compiler.Flux{}, compiler.ArgoCD{RepoURL: "https://git.example.org/out.git"}}
	build := func() *compiler.Tree {
		tr, err := compiler.Build(t.Context(), []compiler.Repository{{Name: "db", FS: tree, Role: compiler.RoleTree}}, compiler.BuildOptions{})
		if err != nil {
			t.Fatal(err)
		}
		return tr
	}
	first, _, err := compiler.TreeFiles(build(), targets, nil)
	if err != nil {
		t.Fatal(err)
	}
	manifests := compiler.ProjectManifestsDir("ops")
	hr := path.Join(compiler.InstanceDir("ops", "ops-x"), "helmrelease.yaml")
	if ns := string(first[path.Join(manifests, "namespace.yaml")]); !strings.Contains(ns, "enforce: privileged") || strings.Contains(string(first[hr]), "serviceAccountName") {
		t.Fatalf("before: privileged expected:\n%s", ns)
	}
	previous := fstest.MapFS{}
	for p, b := range first {
		previous[p] = &fstest.MapFile{Data: b}
	}
	// The platform revokes the privilege, the project takes its role back;
	// an instance of it is refused, which keeps the project.
	tree[compiler.PlatformFile] = file("global: {okdp: {policy: {privileged: []}}}\n")
	tree["projects/ops/project.yaml"] = file("access: {}\n")
	tree["projects/ops/instances/broken/instance.yaml"] = file("service: admin\nversion: 1.0.0\n")
	tree["projects/ops/instances/broken/config.yaml"] = file("n: notanumber\n")
	second, kept, err := compiler.TreeFiles(build(), targets, previous)
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 1 || kept[0].Namespace != "ops" {
		t.Fatalf("kept: %+v", kept)
	}
	ns := string(second[path.Join(manifests, "namespace.yaml")])
	app := string(second[path.Join(manifests, "appproject.yaml")])
	switch {
	case !strings.Contains(ns, "enforce: baseline") || strings.Contains(ns, "privileged"):
		t.Errorf("the Namespace, the privilege revoked:\n%s", ns)
	case second[path.Join(manifests, "service-account.yaml")] == nil:
		t.Error("no confined identity")
	case !strings.Contains(string(second[hr]), `serviceAccountName: "project-ops"`):
		t.Errorf("the kept HelmRelease, not confined:\n%s", second[hr])
	case !strings.Contains(app, "clusterResourceWhitelist: []") || strings.Contains(app, `namespace: "*"`) || strings.Contains(app, "roles:"):
		t.Errorf("the AppProject, not confined:\n%s", app)
	case second[path.Join(manifests, "access.yaml")] != nil:
		t.Errorf("a role taken back kept:\n%s", second[path.Join(manifests, "access.yaml")])
	}
	// The instance's own files as they were: its objects, refused now by
	// the identity that applies them.
	objects := path.Join(compiler.InstanceDir("ops", "ops-x"), compiler.ObjectsFile)
	if string(second[objects]) != string(first[objects]) || !strings.Contains(string(second[objects]), "ClusterRoleBinding") {
		t.Errorf("the kept instance's objects changed:\n%s", second[objects])
	}
}
