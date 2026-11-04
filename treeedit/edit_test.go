package treeedit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	compiler "github.com/okdp/okdp-compiler"
)

// The test tree (testdata/tree) and its catalog (testdata/catalog): the
// project data provides the databases of the project demo's claims, a
// default one (demo-app), a named one (demo-reports, analytics), and
// demo-legacy references a connection of its project (ext-db).
const (
	treeFixture    = "testdata/tree"
	catalogFixture = "testdata/catalog"
)

// memTree is a tree in memory, a Writer: its files by path.
type memTree map[string][]byte

func clean(p string) string { return path.Clean(p) }

func (m memTree) ReadFile(p string) ([]byte, error) {
	data, ok := m[clean(p)]
	if !ok {
		return nil, fmt.Errorf("%s: %w", p, ErrNotFound)
	}
	return bytes.Clone(data), nil
}

func (m memTree) ReadDir(p string) ([]string, error) {
	prefix := clean(p) + "/"
	if prefix == "./" {
		prefix = ""
	}
	seen := map[string]bool{}
	for f := range m {
		if rest, ok := strings.CutPrefix(f, prefix); ok {
			name, _, _ := strings.Cut(rest, "/")
			seen[name] = true
		}
	}
	if len(seen) == 0 {
		return nil, nil
	}
	names := slices.Collect(maps.Keys(seen))
	sort.Strings(names)
	return names, nil
}

func (m memTree) Exists(p string) bool {
	if _, ok := m[clean(p)]; ok {
		return true
	}
	names, _ := m.ReadDir(p)
	return len(names) > 0
}

func (m memTree) WriteFile(p string, data []byte) error {
	m[clean(p)] = bytes.Clone(data)
	return nil
}

func (m memTree) Remove(p string) error {
	p = clean(p)
	for f := range m {
		if f == p || strings.HasPrefix(f, p+"/") {
			delete(m, f)
		}
	}
	return nil
}

// files are the files of the tree, as strings.
func (m memTree) files() map[string]string {
	out := map[string]string{}
	for p, data := range m {
		out[p] = string(data)
	}
	return out
}

// set writes files, a path each, or removes those of no content: a
// person editing the tree.
func (m memTree) set(files map[string]string) {
	for p, content := range files {
		if content == "" {
			_ = m.Remove(p)
		} else {
			_ = m.WriteFile(p, []byte(content))
		}
	}
}

// store is a tree its edits are applied to the way a store's transaction
// applies them: on a copy, kept when the edit passes its check, a commit
// each that changes something, named after the edit.
type store struct {
	tree    memTree
	commits []string
}

// newStore holds the test tree.
func newStore(t *testing.T) *store {
	t.Helper()
	tree := memTree{}
	err := filepath.WalkDir(treeFixture, func(p string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return err
		}
		data, err := os.ReadFile(p)
		rel, _ := filepath.Rel(treeFixture, p)
		tree[filepath.ToSlash(rel)] = data
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return &store{tree: tree}
}

// compile compiles the tree r holds, its platform.yaml and projects/,
// with the test catalog.
func compile(r Reader) (*compiler.Tree, error) {
	tree := fstest.MapFS{}
	for p, data := range r.(memTree) {
		if p == compiler.PlatformFile || strings.HasPrefix(p, compiler.ProjectsDir+"/") {
			tree[p] = &fstest.MapFile{Data: data}
		}
	}
	return compiler.Build(context.Background(), []compiler.Repository{
		{Name: ".", FS: tree, Role: compiler.RoleTree},
		{Name: "test-charts", FS: os.DirFS(catalogFixture), Role: compiler.RoleCatalog},
	}, compiler.BuildOptions{})
}

// apply applies e, checked.
func (s *store) apply(e Edit, err error) (*Change, error) {
	if err != nil {
		return nil, err
	}
	working := maps.Clone(s.tree)
	change, _, err := Apply(working, e, compile)
	if err != nil {
		return nil, err
	}
	if !maps.EqualFunc(working, s.tree, bytes.Equal) {
		s.tree = working
		s.commits = append(s.commits, e.Action+" "+e.Target)
	}
	return change, nil
}

// last is the last commit.
func (s *store) last() string { return s.commits[len(s.commits)-1] }

// An instance's life: its files, written only when they change, each
// edit checked by a compile of the tree.
func TestInstanceLifecycle(t *testing.T) {
	s := newStore(t)
	web := Instance{Project: "demo", Name: "web", Service: "app", Version: "1.0.0", Values: map[string]any{"database": "analytics"}}
	if _, err := s.apply(CreateInstance(web)); err != nil {
		t.Fatal(err)
	}
	if got := s.tree.files()["projects/demo/instances/web/instance.yaml"]; got != "service: app\nversion: 1.0.0\n" {
		t.Errorf("instance.yaml = %q", got)
	}
	if got := s.tree.files()["projects/demo/instances/web/config.yaml"]; got != "database: analytics\n" {
		t.Errorf("config.yaml = %q", got)
	}
	if s.last() != "deploy demo/web" {
		t.Errorf("commit = %q", s.last())
	}
	if _, err := s.apply(CreateInstance(web)); !errors.Is(err, ErrExists) {
		t.Fatalf("second create: %v", err)
	}

	got, err := ReadInstance(s.tree, "demo", "web")
	if err != nil || got.Service != "app" || got.Values["database"] != "analytics" || got.Release() != "demo-web" {
		t.Fatalf("get: %+v %v", got, err)
	}
	list, err := ListInstances(s.tree, "demo")
	if err != nil || len(list) != 4 || list[3].Name != "web" {
		t.Fatalf("list: %+v %v", list, err)
	}

	// A hand-written comment survives an edit that leaves the file alone.
	s.tree.set(map[string]string{"projects/demo/instances/web/config.yaml": "# the shared database\ndatabase: analytics\n"})
	commits := len(s.commits)
	change, err := s.apply(UpdateInstance("demo", "web", func(*Instance) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.commits) != commits || len(change.Files) != 0 || change.Instance.Name != "web" {
		t.Errorf("a no-op update changed %+v", change)
	}
	change, err = s.apply(UpdateInstance("demo", "web", func(i *Instance) error {
		i.Values = map[string]any{"database": map[string]any{"class": "large"}}
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got := s.tree.files()["projects/demo/instances/web/config.yaml"]; got != "database:\n  class: large\n" {
		t.Errorf("config.yaml = %q", got)
	}
	if s.last() != "update demo/web" || change.Instance.Project != "demo" || change.Instance.Values["database"] == nil {
		t.Errorf("commit %q, instance %+v", s.last(), change.Instance)
	}

	if _, err := s.apply(DeleteInstance("demo", "web")); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.tree.files()["projects/demo/instances/web/instance.yaml"]; ok {
		t.Error("the instance survived its delete")
	}
	if s.last() != "delete demo/web" {
		t.Errorf("commit = %q", s.last())
	}
	if _, err := s.apply(DeleteInstance("demo", "web")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
}

// An edit after which an instance does not compile is refused: the
// instance written, or another that compiled before.
func TestAnEditThatDoesNotCompileIsRefused(t *testing.T) {
	s := newStore(t)
	before := s.tree.files()

	_, err := s.apply(CreateInstance(Instance{Project: "demo", Name: "web", Service: "app", Version: "1.0.0", Values: map[string]any{"database": "nowhere"}}))
	var ce *CompileError
	if !errors.As(err, &ce) || ce.Instance != "web" || !strings.Contains(err.Error(), "nowhere") {
		t.Fatalf("an unknown connection: %v", err)
	}
	_, err = s.apply(CreateInstance(Instance{Project: "demo", Name: "web", Service: "app", Version: "9.9.9"}))
	var layout *compiler.LoadError
	if !errors.As(err, &ce) || !errors.As(err, &layout) || !strings.Contains(err.Error(), `version "9.9.9" of service "app"`) {
		t.Fatalf("an unknown version: %v", err)
	}
	// No class huge: analytics does not bind, and every instance of its
	// project is refused.
	_, err = s.apply(PutClaim("demo", compiler.ClaimSpec{Name: "analytics", Contract: "database-server", Class: "huge"}, false))
	if !errors.As(err, &ce) || ce.Project != "demo" || !strings.Contains(err.Error(), `class "huge"`) {
		t.Fatalf("a claim its instances cannot bind: %v", err)
	}
	if _, err := s.apply(CreateInstance(Instance{Project: "nowhere", Name: "web", Service: "app", Version: "1.0.0"})); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an undeclared project: %v", err)
	}
	if _, err := CreateInstance(Instance{Project: "demo", Name: "web"}); err == nil {
		t.Fatal("an instance of no package")
	}
	if !maps.Equal(before, s.tree.files()) || len(s.commits) != 0 {
		t.Error("a refused edit changed the tree")
	}
}

// A tree that does not load refuses an edit for the problems it adds,
// and an edit of an instance whatever the problems.
func TestAnEditOfATreeThatDoesNotLoad(t *testing.T) {
	s := newStore(t)
	s.tree.set(map[string]string{"projects/demo/instances/broken/instance.yaml": "service: app\nversion: 9.9.9\n"})
	lake := compiler.Connection{Name: "lake", Contract: "database-server", Values: map[string]any{"host": "lake", "dbName": "lake"}, SecretRef: "lake"}
	if _, err := s.apply(PutConnection("demo", lake, true)); err != nil {
		t.Fatalf("an edit that adds no problem: %v", err)
	}
	var ce *CompileError
	_, err := s.apply(UpdateInstance("demo", "app", func(*Instance) error { return nil }))
	if !errors.As(err, &ce) || ce.Instance != "app" || !strings.Contains(err.Error(), `version "9.9.9"`) {
		t.Fatalf("an instance written: %v", err)
	}
	s.tree.set(map[string]string{"platform.yaml": "global: [\n"})
	if _, err := s.apply(PutConnection("demo", compiler.Connection{Name: "lake2", Contract: "database-server"}, true)); err != nil {
		t.Fatalf("a tree that did not load: %v", err)
	}
}

// What another instance uses cannot go: a provider, a connection, a
// named claim, a project. The provider of a named claim of a project is
// every instance's of the project: the claim binds for each.
func TestARemovalRefusesWhatIsInUse(t *testing.T) {
	s := newStore(t)
	var inUse *ErrInUse
	if _, err := s.apply(DeleteInstance("data", "postgresql")); !errors.As(err, &inUse) || strings.Join(inUse.Users, ",") != "demo/app,demo/legacy,demo/reports" {
		t.Fatalf("the provider of claims: %v", err)
	}
	if _, err := s.apply(DeleteConnection("demo", "ext-db")); !errors.As(err, &inUse) || strings.Join(inUse.Users, ",") != "legacy" {
		t.Fatalf("a connection: %v", err)
	}
	if _, err := s.apply(DeleteClaim("demo", "analytics")); !errors.As(err, &inUse) || strings.Join(inUse.Users, ",") != "reports" {
		t.Fatalf("a named claim: %v", err)
	}
	_, err := s.apply(DeleteProject("data", false))
	if !errors.As(err, &inUse) || strings.Join(inUse.Users, ",") != "demo/app,demo/legacy,demo/reports" ||
		err.Error() != "project data is still used by demo/app, demo/legacy, demo/reports" {
		t.Fatalf("a project: %v", err)
	}
	if _, err := s.apply(DeleteInstance("demo", "legacy")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.apply(DeleteConnection("demo", "ext-db")); err != nil {
		t.Fatal(err)
	}
	if s.last() != "delete connection demo/ext-db" {
		t.Errorf("commit = %q", s.last())
	}
}

// The connections are entries of connections: of project.yaml, of
// platform.yaml for the platform's; the rest of the file is kept.
func TestConnectionsAreEntriesOfTheirFile(t *testing.T) {
	s := newStore(t)
	ext, err := ReadConnection(s.tree, "demo", "ext-db")
	if err != nil || ext.Contract != "database-server" || ext.Description != "A database outside the platform" || ext.SecretRef != "ext-db-credentials" || ext.Values["host"] != "db.example.org" {
		t.Fatalf("ext-db: %+v %v", ext, err)
	}
	lake := compiler.Connection{Name: "lake", Contract: "database-server", Values: map[string]any{"host": "lake.example.org", "dbName": "lake"}, RemoteKey: "demo/lake"}
	if _, err := s.apply(PutConnection("demo", lake, true)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.apply(PutConnection("demo", lake, true)); !errors.Is(err, ErrExists) {
		t.Fatalf("second create: %v", err)
	}
	project := s.tree.files()["projects/demo/project.yaml"]
	if !strings.HasPrefix(project, "description: The demo project\nconnections:\n") ||
		!strings.Contains(project, "  lake:\n    contract: database-server\n    secretRef:\n      remoteKey: demo/lake\n    dbName: lake\n    host: lake.example.org\n") ||
		!strings.Contains(project, "claims:\n  analytics:\n") {
		t.Errorf("project.yaml:\n%s", project)
	}
	list, err := ListConnections(s.tree, "demo")
	if err != nil || len(list) != 2 || list[1].Name != "lake" || list[1].RemoteKey != "demo/lake" {
		t.Fatalf("list: %+v %v", list, err)
	}
	if _, err := s.apply(PutConnection("demo", compiler.Connection{Name: "nowhere", Contract: "database-server"}, false)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an update of nothing: %v", err)
	}
	if _, err := s.apply(PutConnection("nowhere", lake, true)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a connection of no project: %v", err)
	}
	if _, err := s.apply(DeleteConnection("demo", "lake")); err != nil {
		t.Fatal(err)
	}

	// The platform's, in platform.yaml, its head kept.
	shared := compiler.Connection{Name: "shared", Contract: "database-server", Description: "A  shared\ndatabase", Values: map[string]any{"host": "shared.example.org", "dbName": "shared"}}
	if _, err := s.apply(PutConnection("", shared, true)); err != nil {
		t.Fatal(err)
	}
	platform := s.tree.files()["platform.yaml"]
	if !strings.HasPrefix(platform, "# The platform values:") || !strings.Contains(platform, "connections:\n  shared:\n    contract: database-server\n    description: A shared database\n") {
		t.Errorf("platform.yaml:\n%s", platform)
	}
	if got, err := ReadConnection(s.tree, "", "shared"); err != nil || got.Values["dbName"] != "shared" {
		t.Fatalf("the platform's: %+v %v", got, err)
	}
	if s.last() != "create connection platform/shared" {
		t.Errorf("commit = %q", s.last())
	}
	if _, err := s.apply(PutConnection("", shared, false)); err != nil || s.last() != "create connection platform/shared" {
		t.Errorf("an update of nothing committed %q: %v", s.last(), err)
	}
}

// The named claims are entries of claims of project.yaml.
func TestNamedClaimsAreEntriesOfProjectYAML(t *testing.T) {
	s := newStore(t)
	claims, err := ListClaims(s.tree, "demo")
	if err != nil || len(claims) != 1 || claims[0].Name != "analytics" || claims[0].Class != "large" {
		t.Fatalf("claims: %+v %v", claims, err)
	}
	vectors := compiler.ClaimSpec{Name: "vectors", Contract: "database-server", Request: map[string]any{"extensions": []any{"pgvector"}}}
	if _, err := s.apply(PutClaim("demo", vectors, true)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.tree.files()["projects/demo/project.yaml"], "  vectors:\n    contract: database-server\n    request:\n      extensions:\n        - pgvector\n") {
		t.Errorf("project.yaml:\n%s", s.tree.files()["projects/demo/project.yaml"])
	}
	if s.last() != "create claim demo/vectors" {
		t.Errorf("commit = %q", s.last())
	}
	if _, err := s.apply(PutClaim("demo", vectors, true)); !errors.Is(err, ErrExists) {
		t.Fatalf("second create: %v", err)
	}
	if _, err := s.apply(DeleteClaim("demo", "vectors")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.apply(DeleteClaim("demo", "vectors")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
	if _, err := ListClaims(s.tree, ".."); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the claims of no project: %v", err)
	}
}

// An instance whose release another instance of another project has is
// refused: project demo instance app-x and project demo-app instance x
// are both release demo-app-x.
func TestCreateRefusesACollidingReleaseName(t *testing.T) {
	s := newStore(t)
	if _, err := s.apply(CreateProject(Project{Name: "demo-app"}, false)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.apply(CreateInstance(Instance{Project: "demo", Name: "app-x", Service: "app", Version: "1.0.0"})); err != nil {
		t.Fatal(err)
	}
	commits := len(s.commits)
	var taken *ErrReleaseTaken
	_, err := s.apply(CreateInstance(Instance{Project: "demo-app", Name: "x", Service: "app", Version: "1.0.0"}))
	if !errors.As(err, &taken) || taken.Release != "demo-app-x" || taken.Project != "demo" || taken.Instance != "app-x" || errors.Is(err, ErrExists) ||
		err.Error() != "release name 'demo-app-x' is already used by instance 'app-x' of project 'demo'" {
		t.Fatalf("err = %v", err)
	}
	if len(s.commits) != commits {
		t.Error("the colliding instance was written")
	}
}

// A project is its project.yaml, its description the key description.
func TestProjectsAreTheirProjectFiles(t *testing.T) {
	s := newStore(t)
	projects, err := ListProjects(s.tree, false)
	if err != nil || len(projects) != 2 || projects[0].Description != "The platform's databases" || projects[1].Description != "The demo project" {
		t.Fatalf("projects: %+v %v", projects, err)
	}
	if _, err := s.apply(CreateProject(Project{Name: "lab", Description: "A  lab\nof data"}, false)); err != nil {
		t.Fatal(err)
	}
	if got := s.tree.files()["projects/lab/project.yaml"]; got != "description: A lab of data\n" {
		t.Errorf("project.yaml = %q", got)
	}
	if s.last() != "create project lab" {
		t.Errorf("commit = %q", s.last())
	}
	if _, err := s.apply(CreateProject(Project{Name: "lab"}, false)); !errors.Is(err, ErrExists) {
		t.Fatalf("second create: %v", err)
	}
	if _, err := s.apply(UpdateProject(Project{Name: "demo", Description: "Demos"})); err != nil {
		t.Fatal(err)
	}
	demo := s.tree.files()["projects/demo/project.yaml"]
	if !strings.HasPrefix(demo, "description: Demos\nconnections:\n") || !strings.Contains(demo, "claims:\n") {
		t.Errorf("project.yaml:\n%s", demo)
	}
	if _, err := s.apply(UpdateProject(Project{Name: "lab"})); err != nil {
		t.Fatal(err)
	}
	if got := s.tree.files()["projects/lab/project.yaml"]; got != "{}\n" {
		t.Errorf("project.yaml = %q", got)
	}
	if _, err := s.apply(UpdateProject(Project{Name: "nowhere"})); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an update of nothing: %v", err)
	}
	if p, err := ReadProject(s.tree, "nowhere"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get: %+v %v", p, err)
	}
	if _, err := s.apply(DeleteProject("lab", false)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.apply(DeleteProject("lab", false)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
}

// The projects of a tree whose projects each have a repository of their
// own are those of projects.yaml, which a project joins when it is
// created and leaves when it is removed, its directory left to its
// repository.
func TestTheProjectsOfASplitTreeAreListed(t *testing.T) {
	s := newStore(t)
	s.tree.set(map[string]string{ProjectsFile: "projects:\n  - name: demo\n  - name: data\n"})
	if _, err := s.apply(CreateProject(Project{Name: "lab"}, true)); err != nil {
		t.Fatal(err)
	}
	if got := s.tree.files()[ProjectsFile]; got != "projects:\n  - name: data\n  - name: demo\n  - name: lab\n" {
		t.Errorf("%s:\n%s", ProjectsFile, got)
	}
	if err := CheckNewProject(s.tree, "lab"); !errors.Is(err, ErrExists) {
		t.Fatalf("a project listed: %v", err)
	}
	if err := CheckNewProject(s.tree, "other"); err != nil {
		t.Fatal(err)
	}
	change, err := s.apply(DeleteProject("lab", true))
	if err != nil {
		t.Fatal(err)
	}
	if len(change.Files) != 1 || change.Files[0].Path != ProjectsFile || change.Files[0].Remove {
		t.Errorf("change: %+v", change.Files)
	}
	if got := s.tree.files()[ProjectsFile]; got != "projects:\n  - name: data\n  - name: demo\n" {
		t.Errorf("%s:\n%s", ProjectsFile, got)
	}

	// A project whose project.yaml cannot be read is listed all the same.
	s.tree.set(map[string]string{"projects/data/project.yaml": ""})
	projects, err := ListProjects(s.tree, true)
	if err != nil || len(projects) != 3 || projects[0].Name != "data" || projects[0].ReadErr == nil || projects[1].ReadErr != nil {
		t.Fatalf("projects: %+v %v", projects, err)
	}
	if projects, err := ListProjects(s.tree, false); err != nil || len(projects) != 2 || projects[0].Name != "demo" {
		t.Fatalf("an unsplit tree: %+v %v", projects, err)
	}

	for _, raw := range []string{"projects:\n  - name: ..\n", "projects:\n  - name: a\n  - name: a\n", "projects: x\n"} {
		if _, err := DecodeProjects([]byte(raw)); err == nil {
			t.Errorf("%q: decoded", raw)
		}
	}
}

// A project's name the compile refuses, okdp-compiler's reserved ones
// and those of platform.yaml's global.okdp.policy.reserved, is a
// *CompileError, the compiler's message.
func TestAProjectOfAReservedNameIsRefused(t *testing.T) {
	s := newStore(t)
	s.tree["platform.yaml"] = append(s.tree["platform.yaml"], "    policy:\n      reserved: [openbao]\n"...)
	for name, want := range map[string]string{
		"kube-foo": `project "kube-foo": the namespace is reserved: kube-* is Kubernetes' own (kube-system, kube-public, kube-node-lease)`,
		"platform": `project "platform": the namespace is reserved: platform/ holds the platform's keys of the secret store, which no project writes`,
		"argocd":   `project "argocd": the namespace is reserved: a deployer engine's, or the namespace its target writes in (argocd, flux-system, okdp-releases): a project's releases there would drive the engine`,
		"openbao":  `project "openbao": the namespace is reserved by the platform (global.okdp.policy.reserved): no project has it`,
	} {
		err := CheckProjectName(s.tree, name)
		var compileErr *CompileError
		if !errors.As(err, &compileErr) || compileErr.Project != name || err.Error() != want {
			t.Errorf("%s: got %v, want %s", name, err, want)
		}
	}
	if err := CheckProjectName(s.tree, "lab"); err != nil {
		t.Fatal(err)
	}
	if err := CheckProjectName(memTree{}, "lab"); err != nil {
		t.Fatalf("a tree without platform.yaml: %v", err)
	}
}

// The catalog menu is a key of catalogs.yaml, the rest of which is
// kept; its repositories are the catalogs of a compile.
func TestUpdateCatalogKeepsWhatItDoesNotTouch(t *testing.T) {
	s := newStore(t)
	if _, err := s.apply(UpdateCatalog("app", func(doc map[string]any) error {
		doc["categories"] = []any{}
		return nil
	}), nil); err != nil {
		t.Fatal(err)
	}
	got := s.tree.files()[CatalogsFile]
	if !strings.HasPrefix(got, "# The catalog repositories") || !strings.Contains(got, "repositories:\n  - {name: test-charts") || !strings.Contains(got, "categories: []\n") {
		t.Errorf("%s:\n%s", CatalogsFile, got)
	}
	if s.last() != "update catalog app" {
		t.Errorf("commit = %q", s.last())
	}
	doc, err := ReadCatalog(s.tree)
	if err != nil || doc["categories"] == nil {
		t.Fatalf("catalog: %+v %v", doc, err)
	}
	repos, err := ReadCatalogRepositories(s.tree)
	if err != nil || !slices.Equal(repos, []CatalogRepository{{Name: "test-charts", URL: "https://git.example.org/okdp/test-charts.git", Ref: "main"}}) {
		t.Fatalf("repositories: %+v %v", repos, err)
	}
	if repos, err := ReadCatalogRepositories(memTree{}); err != nil || repos != nil {
		t.Fatalf("no %s: %+v %v", CatalogsFile, repos, err)
	}
	if _, err := ReadCatalog(memTree{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("no %s: %v", CatalogsFile, err)
	}
	if _, err := ReadCatalogRepositories(memTree{CatalogsFile: []byte("repositories: x\n")}); err == nil || !strings.HasPrefix(err.Error(), CatalogsFile+": ") {
		t.Fatalf("a list that is not one: %v", err)
	}
	if raw, err := ReadPlatformValues(s.tree); err != nil || !bytes.HasPrefix(raw, []byte("# The platform values:")) {
		t.Fatalf("platform.yaml: %q %v", raw, err)
	}
}

func TestCatalogRepositories(t *testing.T) {
	repos, err := CatalogRepositories(map[string]any{"repositories": []any{
		map[string]any{"name": "platform-charts", "url": "https://git/platform-charts.git", "ref": "v1.0.0"},
		map[string]any{"name": "sandbox-dependencies", "url": "https://git/sandbox-dependencies.git"},
	}})
	want := []CatalogRepository{
		{Name: "platform-charts", URL: "https://git/platform-charts.git", Ref: "v1.0.0"},
		{Name: "sandbox-dependencies", URL: "https://git/sandbox-dependencies.git", Ref: "main"},
	}
	if err != nil || !slices.Equal(repos, want) {
		t.Fatalf("%+v %v", repos, err)
	}
	if none, err := CatalogRepositories(map[string]any{"categories": []any{}}); err != nil || len(none) != 0 {
		t.Fatalf("none: %+v %v", none, err)
	}
	for name, doc := range map[string]map[string]any{
		"not a list":   {"repositories": "x"},
		"no name":      {"repositories": []any{map[string]any{"url": "u"}}},
		"no url":       {"repositories": []any{map[string]any{"name": "a"}}},
		"a name twice": {"repositories": []any{map[string]any{"name": "a", "url": "u"}, map[string]any{"name": "a", "url": "v"}}},
	} {
		if _, err := CatalogRepositories(doc); err == nil {
			t.Errorf("%s: read", name)
		}
	}
}

// A name that is a dot segment, which joined into a path would address
// another directory, is not found, and changes nothing.
func TestDotSegmentNamesAreNotFound(t *testing.T) {
	s := newStore(t)
	before := s.tree.files()
	if _, err := ReadInstance(s.tree, "demo", ".."); !errors.Is(err, ErrNotFound) {
		t.Errorf("get: %v", err)
	}
	if _, err := s.apply(DeleteInstance("demo", "..")); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete: %v", err)
	}
	if _, err := s.apply(UpdateInstance("..", "x", func(*Instance) error { return nil })); !errors.Is(err, ErrNotFound) {
		t.Errorf("update: %v", err)
	}
	if _, err := s.apply(DeleteConnection("..", "x")); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete connection: %v", err)
	}
	if _, err := ReadConnection(s.tree, "", ".."); !errors.Is(err, ErrNotFound) {
		t.Errorf("get connection: %v", err)
	}
	if _, err := s.apply(DeleteClaim("demo", ".")); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete claim: %v", err)
	}
	if _, err := s.apply(DeleteProject(".", false)); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete project: %v", err)
	}
	if !maps.Equal(before, s.tree.files()) {
		t.Error("a dot segment changed the tree")
	}
}

// The claimers of an instance.yaml are read, and written back as they
// are when the file is written again: absent (the package's), none
// (claimers: []), or listed.
func TestTheClaimersOfAnInstanceAreKeptAsWritten(t *testing.T) {
	s := newStore(t)
	file := "projects/data/instances/postgresql/instance.yaml"
	for _, c := range []struct {
		written  string
		claimers []string
	}{
		{"service: postgresql\nversion: 1.0.0\n", nil},
		{"service: postgresql\nversion: 1.0.0\nclaimers: []\n", []string{}},
		{"service: postgresql\nversion: 1.0.0\nclaimers:\n  - demo\n  - lab\n", []string{"demo", "lab"}},
	} {
		s.tree.set(map[string]string{file: c.written})
		same := func(got []string) bool { return (got == nil) == (c.claimers == nil) && slices.Equal(got, c.claimers) }
		got, err := ReadInstance(s.tree, "data", "postgresql")
		if err != nil || !same(got.Claimers) {
			t.Fatalf("%q: %#v %v", c.written, got, err)
		}
		list, err := ListInstances(s.tree, "data")
		if err != nil || len(list) != 1 || !same(list[0].Claimers) {
			t.Fatalf("%q: list %#v %v", c.written, list, err)
		}
		// Another version, unchecked: the claimers written back as read.
		e, err := UpdateInstance("data", "postgresql", func(i *Instance) error {
			i.Version = "2.0.0"
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := Apply(s.tree, e, nil); err != nil {
			t.Fatal(err)
		}
		if want := strings.Replace(c.written, "1.0.0", "2.0.0", 1); s.tree.files()[file] != want {
			t.Errorf("written again:\n%s\nwant:\n%s", s.tree.files()[file], want)
		}
	}

	// Changed by the control plane: written.
	s.tree.set(map[string]string{file: "service: postgresql\nversion: 1.0.0\n"})
	if _, err := s.apply(UpdateInstance("data", "postgresql", func(i *Instance) error {
		i.Claimers = []string{"demo"}
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	if got := s.tree.files()[file]; got != "service: postgresql\nversion: 1.0.0\nclaimers:\n  - demo\n" {
		t.Errorf("instance.yaml:\n%s", got)
	}
}

// The output a target wrote is read back: an instance's compiled files,
// what the instances of a project publish.
func TestTheOutputIsReadBack(t *testing.T) {
	s := newStore(t)
	if _, err := ReadCompiled(fstest.MapFS{}, "demo", "app"); !errors.Is(err, ErrNotFound) || !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("compiled before the compile: %v", err)
	}
	tree, err := compile(s.tree)
	if err != nil {
		t.Fatal(err)
	}
	files, _, err := compiler.TreeFiles(tree, []compiler.Target{compiler.Flux{}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	output := fstest.MapFS{}
	for p, data := range files {
		output[p] = &fstest.MapFile{Data: data}
	}
	compiled, err := ReadCompiled(output, "demo", "app")
	if err != nil || !strings.Contains(string(compiled.Chart), "name: app") || compiled.Values["app"] == nil {
		t.Fatalf("compiled: %+v %v", compiled, err)
	}
	if _, err := ReadCompiled(output, "demo", ".."); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a dot segment: %v", err)
	}
	published, err := ListPublished(s.tree, output, "data")
	if err != nil || len(published) != 1 {
		t.Fatalf("published: %+v %v", published, err)
	}
	if pg := published[0]; pg.Name != "postgresql" || pg.Release != "data-postgresql" || pg.Service != "postgresql" || pg.Version != "1.0.0" || len(pg.Publishes.Provides) != 2 {
		t.Errorf("data-postgresql: %+v", pg)
	}
	// An instance of the output the tree has not: not listed.
	_ = s.tree.Remove("projects/data/instances/postgresql")
	if published, err := ListPublished(s.tree, output, "data"); err != nil || len(published) != 0 {
		t.Fatalf("published: %+v %v", published, err)
	}
	if published, err := ListPublished(s.tree, output, ".."); err != nil || published != nil {
		t.Fatalf("no project: %+v %v", published, err)
	}
}
