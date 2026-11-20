// Package repository reads the tree of a deployment (docs/01_usage.md,
// repository layout) from one or more repositories.
package repository

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"path"
	"slices"
	"strings"
	"testing/fstest"

	"go.yaml.in/yaml/v3"

	"github.com/okdp/okdp-compiler/chart"
	"github.com/okdp/okdp-compiler/internal/okdp"
	"github.com/okdp/okdp-compiler/internal/tree"
)

// Repository is one part of the tree. Name identifies it in messages. Role
// is required: Load and LoadCatalog refuse the zero value.
type Repository struct {
	Name string
	FS   fs.FS
	Role Role
}

// Role says what a repository contributes to the tree. A part outside the
// repository's role is refused rather than read (docs/04_threat_model.md).
// The maintainers of a catalog can therefore add neither a project,
// privileged or not, nor the platform's values.
type Role string

const (
	// RoleTree is a repository of the deployment, the platform's or a
	// project's. It may hold any part of the tree.
	RoleTree Role = "tree"
	// RoleCatalog is a catalog of packages. It may hold catalog/ and
	// contracts/ only: PlatformFile or projects/ in it is refused.
	RoleCatalog Role = "catalog"
)

// The file and directory names of the tree.
const (
	PlatformFile = "platform.yaml" // the platform's values, context level 0
	ContractsDir = "contracts"     // <contract>.schema.json
	CatalogDir   = "catalog"       // <service>/<version>/, a package
	ProjectsDir  = "projects"      // <project>/, a namespace
	ProjectFile  = "project.yaml"  // a project's values, context level 1
	// ProjectDescription is the key of ProjectFile that describes the
	// project, a string. It is not a value of the context.
	ProjectDescription = "description"
	InstancesDir       = "instances"        // <instance>/, the release <project>-<instance>
	InstanceFile       = "instance.yaml"    // {service, version, claimers}
	ConnectionsFile    = "connections.yaml" // the instance's own connections, context level 2
	ConfigFile         = "config.yaml"      // the instance's values
)

// ReleaseName returns the release of an instance, <project>-<instance>. It
// makes the releases of a tree unique across its projects, because the
// HelmReleases or Applications of a deployer share a namespace. Two names
// that hold a "-" can still meet (a-b/c and a/b-c): the targets refuse
// those.
func ReleaseName(project, instance string) string { return project + "-" + instance }

// parts is the repositories merged: it records the owner of each part.
type parts struct {
	platform  *Repository            // the owner of PlatformFile, nil when none
	services  map[string]*Repository // the owner of catalog/<service>/, by service
	projects  map[string]*Repository // the owner of projects/<project>/, by project
	contracts fstest.MapFS           // every contract file; index writes it, then it is only read
	packages  map[[2]string]*okdp.Definition
	rules     *okdp.RuleSet // the rule set the packages are read for; nil, the mandatory one
}

// Load returns every instance of the tree that repos make together, sorted
// by project, then by instance name. Each part of the tree is in one
// repository, so the order of repos changes nothing:
//
//	platform.yaml         PlatformFile
//	catalog/<service>/    every version of the service
//	projects/<project>/   the project's file and its instances
//
// A contract may be in several repositories when its bytes are the same in
// each. The context of an instance is PlatformFile at level 0, ProjectFile
// at level 1 and ConnectionsFile at level 2. An absent file gives no layer.
//
// The error lists every problem at once. A package that fails is reported
// once. A tree with any problem gives no instance: LoadProjects loads what
// it can.
//
// The packages are read for rules: okdp.yaml of a package may hold their
// sections (okdp.LoadDefinition). Nil rules stand for the mandatory
// set.
func Load(ctx context.Context, repos []Repository, rules *okdp.RuleSet) ([]okdp.Instance, error) {
	l, err := LoadProjects(ctx, repos, rules)
	if err != nil {
		return nil, err
	}
	if err := newError(refusedErrs(l.Refused)); err != nil {
		return nil, err
	}
	return l.Instances, nil
}

// Loaded is a tree as LoadProjects read it.
type Loaded struct {
	Instances []okdp.Instance // the instances of the projects that load
	Refused   []Refused       // the projects that do not load, in name order
}

// Refused is a project that does not load (tree.Refused). One problem of
// its own refuses the project as a whole: none of its instances load. It
// does not refuse the tree.
type Refused = tree.Refused

// packageError marks a package that fails to load. This is a problem of the
// catalog, not of the instance that names the package.
type packageError struct{ err error }

func (e *packageError) Error() string { return e.err.Error() }
func (e *packageError) Unwrap() error { return e.err }

// LoadProjects is Load, project by project, the packages read for rules
// as Load reads them. A problem of the platform (the repositories and
// their roles, PlatformFile, CatalogsFile, a package or its contracts) is
// an error: an *Error with every problem, the platform's first, then
// those of the projects. A problem of a project's own refuses that
// project alone: Loaded.Refused lists it, and the instances of the other
// projects load.
func LoadProjects(ctx context.Context, repos []Repository, rules *okdp.RuleSet) (*Loaded, error) {
	t, err := index(repos)
	if err != nil {
		return nil, newError([]error{err})
	}
	t.rules = rules
	var platformErrs []error // the problems of the platform's parts and of the packages
	for _, r := range repos {
		if r.Role == RoleTree {
			platformErrs = append(platformErrs, t.checkCatalogs(r)...)
		}
	}
	var platform []okdp.ContextLayer
	if t.platform != nil {
		platform, err = readLayer(*t.platform, PlatformFile, 0)
		platformErrs = append(platformErrs, err)
		// Named claims belong to a project and its namespace. The
		// platform's connections serve every project, so PlatformFile
		// declares no claims.
		for _, l := range platform {
			if _, set := l.Values["claims"]; set {
				platformErrs = append(platformErrs, fmt.Errorf("%s: claims: a project's (%s)", where(*t.platform, PlatformFile), ProjectFile))
			}
		}
	}
	l := &Loaded{}
	for _, project := range slices.Sorted(maps.Keys(t.projects)) {
		repo := *t.projects[project]
		dir := path.Join(ProjectsDir, project)
		var own []error // the project's own problems
		layer, err := readProject(repo, path.Join(dir, ProjectFile))
		layers := append(slices.Clone(platform), layer...)
		own = append(own, err)
		readable := err == nil
		names, err := dirs(repo, path.Join(dir, InstancesDir))
		own = append(own, err)
		// A project without instances has no compile to check its
		// declarations. Check them here.
		if err == nil && len(names) == 0 && layer != nil {
			in := okdp.Inputs{Contracts: t.contracts, Context: slices.Clip(layers), Namespace: project}
			for _, err := range unjoin(okdp.CheckDeclarations(ctx, in)) {
				own = append(own, fmt.Errorf("%s: %w", where(repo, path.Join(dir, ProjectFile)), err))
			}
		}
		var instances []okdp.Instance
		for _, name := range names {
			inst, err := t.instance(repo, project, name, slices.Clip(layers))
			if pe, ok := errors.AsType[*packageError](err); ok {
				// This is the catalog's problem, not the project's.
				platformErrs = append(platformErrs, pe)
				continue
			}
			if err != nil || inst.Definition == nil {
				own = append(own, err)
				continue
			}
			instances = append(instances, inst)
		}
		if r := refusal(project, own); r != nil {
			r.Inputs = okdp.Inputs{Contracts: t.contracts, Context: slices.Clip(slices.Clone(platform)), Namespace: project}
			if readable {
				r.Inputs.Context, r.Own = slices.Clip(layers), true
			}
			l.Refused = append(l.Refused, *r)
			continue
		}
		l.Instances = append(l.Instances, instances...)
	}
	if newError(platformErrs) != nil {
		return nil, newError(slices.Concat(platformErrs, refusedErrs(l.Refused)))
	}
	return l, nil
}

// refusedErrs returns the errors of the refused projects, in their order.
func refusedErrs(refused []Refused) []error {
	var errs []error
	for _, r := range refused {
		errs = append(errs, r.Errs...)
	}
	return errs
}

// refusal builds the Refused of project, nil when errs holds no problem.
func refusal(project string, errs []error) *Refused {
	e, ok := newError(errs).(*Error)
	if !ok {
		return nil
	}
	return &Refused{Namespace: project, Problems: e.Problems, Errs: e.errs}
}

// instance loads the instance name of project from repo. layers are the
// context layers below its own. Without claimers in its InstanceFile,
// Inputs.Claimers is nil and the package's claimers apply. The instance
// comes without its Definition when its package already failed for an
// instance before it.
func (t *parts) instance(repo Repository, project, name string, layers []okdp.ContextLayer) (okdp.Instance, error) {
	dir := path.Join(ProjectsDir, project, InstancesDir, name)
	if err := instanceFiles(repo, dir); err != nil {
		return okdp.Instance{}, err
	}
	file := path.Join(dir, InstanceFile)
	var pkg InstanceSpec
	b, err := fs.ReadFile(repo.FS, file)
	if err == nil {
		pkg, err = ParseInstance(b)
	}
	if err == nil && (pkg.Service == "" || pkg.Version == "") {
		err = errors.New("service and version are required")
	}
	if err == nil {
		err = okdp.CheckClaimers("claimers", pkg.Claimers)
	}
	if err != nil {
		return okdp.Instance{}, fmt.Errorf("%s: %w", where(repo, file), unwrapPath(err))
	}
	def, err := t.definition(pkg.Service, pkg.Version)
	if err != nil {
		err = fmt.Errorf("%s: %w", where(repo, file), err)
		if _, ok := errors.AsType[*packageError](err); ok {
			// A package that fails to load is the catalog's problem, so the
			// tree's, not the project's.
			err = &packageError{err}
		}
		return okdp.Instance{}, err
	}
	connections, err := readLayer(repo, path.Join(dir, ConnectionsFile), 2)
	if err != nil {
		return okdp.Instance{}, err
	}
	for _, l := range connections {
		for _, k := range slices.Sorted(maps.Keys(l.Values)) {
			if k != "connections" {
				return okdp.Instance{}, fmt.Errorf("%s: %s: the instance's own connections only (connections): the rest of the context is the platform's or the project's", where(repo, path.Join(dir, ConnectionsFile)), k)
			}
		}
	}
	values, err := readLayer(repo, path.Join(dir, ConfigFile), 0)
	if err != nil {
		return okdp.Instance{}, err
	}
	in := okdp.Inputs{Contracts: t.contracts, Context: append(layers, connections...), Release: ReleaseName(project, name), Namespace: project, Claimers: pkg.Claimers}
	for _, l := range values {
		in.Values = append(in.Values, l.Values)
	}
	return okdp.Instance{Definition: def, Inputs: in, Name: name}, nil
}

// instanceFiles checks that the instance's directory holds InstanceFile,
// ConfigFile and ConnectionsFile only, each a file. No compile would read
// any other entry (a values.yaml, a hidden file), so it is refused.
func instanceFiles(repo Repository, dir string) error {
	entries, err := fs.ReadDir(repo.FS, dir)
	if err != nil {
		return fmt.Errorf("%s: %w", where(repo, dir), unwrapPath(err))
	}
	var errs []error
	for _, e := range entries {
		switch e.Name() {
		case InstanceFile, ConfigFile, ConnectionsFile:
			if !e.IsDir() {
				continue
			}
		}
		errs = append(errs, fmt.Errorf("%s: not a file of an instance, which holds %s, %s and %s only: no compile reads it", where(repo, path.Join(dir, e.Name())), InstanceFile, ConfigFile, ConnectionsFile))
	}
	return errors.Join(errs...)
}

// InstanceSpec is an instance's InstanceFile, as written. Claimers lists
// the namespaces whose instances may claim from it from another namespace
// (namespace names, or "*"). Nil means absent: the package's claimers
// apply. Empty means none (claimers: []).
type InstanceSpec struct {
	Service  string   `yaml:"service"`
	Version  string   `yaml:"version"`
	Claimers []string `yaml:"claimers"`
}

// ParseInstance reads the first document of an InstanceFile strictly: it
// refuses an unknown key, a duplicate key and a value of the wrong form. It
// does not require service and version, nor check the claimers: Load does.
// Its messages name the keys and the lines, never a Go type.
func ParseInstance(b []byte) (InstanceSpec, error) {
	var doc yaml.Node
	if err := yaml.NewDecoder(bytes.NewReader(b)).Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return InstanceSpec{}, nil
		}
		return InstanceSpec{}, err
	}
	root := &doc
	if root.Kind == yaml.DocumentNode && len(root.Content) == 1 {
		root = root.Content[0]
	}
	if root.Kind == yaml.ScalarNode && root.Tag == "!!null" {
		return InstanceSpec{}, nil
	}
	if root.Kind != yaml.MappingNode {
		return InstanceSpec{}, fmt.Errorf("yaml: unmarshal errors:\n  line %d: a mapping of service, version and claimers expected", root.Line)
	}
	var problems []string
	first := map[string]int{}
	for i := 0; i+1 < len(root.Content); i += 2 {
		k, v := root.Content[i], root.Content[i+1]
		if at, dup := first[k.Value]; dup {
			problems = append(problems, fmt.Sprintf("line %d: mapping key %q already defined at line %d", k.Line, k.Value, at))
			continue
		}
		first[k.Value] = k.Line
		switch k.Value {
		case "service", "version":
			if v.Kind != yaml.ScalarNode {
				problems = append(problems, fmt.Sprintf("line %d: %s: a string expected", v.Line, k.Value))
			}
		case "claimers":
			if !claimersNode(v) {
				problems = append(problems, fmt.Sprintf("line %d: claimers: a list of namespace names expected", v.Line))
			}
		default:
			problems = append(problems, fmt.Sprintf("line %d: field %s not found: service, version and claimers only", k.Line, k.Value))
		}
	}
	if problems != nil {
		return InstanceSpec{}, fmt.Errorf("yaml: unmarshal errors:\n  %s", strings.Join(problems, "\n  "))
	}
	var spec InstanceSpec
	if err := root.Decode(&spec); err != nil {
		return InstanceSpec{}, err
	}
	return spec, nil
}

// claimersNode reports whether n is null or a list of scalars.
func claimersNode(n *yaml.Node) bool {
	switch n.Kind {
	case yaml.ScalarNode:
		return n.Tag == "!!null"
	case yaml.SequenceNode:
		return !slices.ContainsFunc(n.Content, func(c *yaml.Node) bool { return c.Kind != yaml.ScalarNode })
	}
	return false
}

// definition loads the package catalog/<service>/<version>/, once. Its
// Chart.yaml must have that name and version. A version the catalog lacks
// is the instance's error. A package that fails to load is a *packageError
// the first time only: later calls return nil and no error.
func (t *parts) definition(service, version string) (*okdp.Definition, error) {
	repo, dir, err := t.packageDir(service, version)
	if err != nil {
		return nil, err
	}
	key := [2]string{service, version}
	if def, loaded := t.packages[key]; loaded {
		return def, nil
	}
	t.packages[key] = nil
	files, err := fs.Sub(repo.FS, dir)
	if err != nil {
		return nil, &packageError{err}
	}
	def, err := okdp.LoadDefinition(files, t.rules)
	if err == nil && (def.Chart.Name != service || def.Chart.Version != version) {
		err = fmt.Errorf("its Chart.yaml is %s %s", def.Chart.Name, def.Chart.Version)
	}
	if err != nil {
		return nil, &packageError{fmt.Errorf("the package %s: %w", where(*repo, dir), err)}
	}
	t.packages[key] = def
	return def, nil
}

// index records the owner of each part of the tree. It reads the
// repositories in name order and reports every conflict at once, those of
// the parts in two repositories last, sorted by part. In a
// catalog, PlatformFile or projects/ is refused as soon as it exists (a
// file, an empty directory); the rest of the catalog is still read.
func index(repos []Repository) (*parts, error) {
	repos = slices.SortedFunc(slices.Values(repos), func(a, b Repository) int { return strings.Compare(a.Name, b.Name) })
	t := &parts{services: map[string]*Repository{}, projects: map[string]*Repository{}, contracts: fstest.MapFS{}, packages: map[[2]string]*okdp.Definition{}}
	var errs []error
	owners := map[string][]string{} // a part, as messages name it: the repositories that have it
	own := func(part string, repo *Repository) { owners[part] = append(owners[part], repo.Name) }
	from := map[string]string{} // a contract file: the first repository that has it
	for i := range repos {
		repo := &repos[i]
		switch repo.Role {
		case RoleTree, RoleCatalog:
		case "":
			errs = append(errs, fmt.Errorf("repository %s: no role (Repository.Role): %s, a part of the deployment, or %s, packages and contracts only", repo.Name, RoleTree, RoleCatalog))
			continue
		default:
			errs = append(errs, fmt.Errorf("repository %s: role %q: %s, a part of the deployment, or %s, packages and contracts only", repo.Name, repo.Role, RoleTree, RoleCatalog))
			continue
		}
		if repo.Role == RoleCatalog {
			for _, p := range []string{PlatformFile, ProjectsDir} {
				if _, err := fs.Stat(repo.FS, p); err == nil {
					errs = append(errs, fmt.Errorf("%s: a catalog repository holds %s/ and %s/ only: %s is the deployment's (a repository of role %s)", where(*repo, p), CatalogDir, ContractsDir, p, RoleTree))
				} else if !errors.Is(err, fs.ErrNotExist) {
					errs = append(errs, fmt.Errorf("%s: %w", where(*repo, p), unwrapPath(err)))
				}
			}
		}
		if repo.Role == RoleTree {
			if _, err := fs.Stat(repo.FS, PlatformFile); err == nil {
				own(PlatformFile, repo)
				t.platform = repo
			} else if !errors.Is(err, fs.ErrNotExist) {
				errs = append(errs, fmt.Errorf("%s: %w", where(*repo, PlatformFile), unwrapPath(err)))
			}
			if _, err := fs.Stat(repo.FS, CatalogsFile); err == nil {
				own(CatalogsFile, repo)
			}
		}
		services, err := dirs(*repo, CatalogDir)
		errs = append(errs, err)
		for _, s := range services {
			own(fmt.Sprintf("service %q", s), repo)
			t.services[s] = repo
		}
		if repo.Role == RoleTree {
			projects, err := dirs(*repo, ProjectsDir)
			errs = append(errs, err)
			for _, p := range projects {
				own(fmt.Sprintf("project %q", p), repo)
				t.projects[p] = repo
			}
		}
		entries, err := fs.ReadDir(repo.FS, ContractsDir)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, fmt.Errorf("%s: %w", where(*repo, ContractsDir), unwrapPath(err)))
		}
		for _, e := range entries {
			name, ok := strings.CutSuffix(e.Name(), ".schema.json")
			if e.IsDir() || !ok {
				continue
			}
			file := path.Join(ContractsDir, e.Name())
			b, err := fs.ReadFile(repo.FS, file)
			switch prev, seen := t.contracts[e.Name()]; {
			case err != nil:
				errs = append(errs, fmt.Errorf("%s: %w", where(*repo, file), unwrapPath(err)))
			case !seen:
				t.contracts[e.Name()] = &fstest.MapFile{Data: b}
				from[e.Name()] = repo.Name
			case !bytes.Equal(prev.Data, b):
				errs = append(errs, fmt.Errorf("contract %s differs between the repositories %s and %s", name, from[e.Name()], repo.Name))
			}
		}
	}
	for _, part := range slices.Sorted(maps.Keys(owners)) {
		if names := owners[part]; len(names) > 1 {
			errs = append(errs, fmt.Errorf("%s is in the repositories %s and %s", part, strings.Join(names[:len(names)-1], ", "), names[len(names)-1]))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return t, nil
}

// dirs returns the directories of dir, none when dir does not exist.
func dirs(repo Repository, dir string) ([]string, error) {
	entries, err := fs.ReadDir(repo.FS, dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", where(repo, dir), unwrapPath(err))
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

// readLayer reads a values file as a context layer of the given level. It
// returns no layer when the file does not exist.
func readLayer(repo Repository, file string, level uint) ([]okdp.ContextLayer, error) {
	b, err := fs.ReadFile(repo.FS, file)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", where(repo, file), unwrapPath(err))
	}
	v, err := chart.ParseValues(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", where(repo, file), err)
	}
	return []okdp.ContextLayer{{Level: level, Values: v}}, nil
}

// readProject reads a project's file as the context layer of level 1. It
// removes ProjectDescription, which must be a string or null: no instance
// receives it.
func readProject(repo Repository, file string) ([]okdp.ContextLayer, error) {
	layers, err := readLayer(repo, file, 1)
	for _, l := range layers {
		if d, set := l.Values[ProjectDescription]; set {
			if _, isString := d.(string); !isString && d != nil {
				return nil, fmt.Errorf("%s: %s: a string, the project's description", where(repo, file), ProjectDescription)
			}
			delete(l.Values, ProjectDescription)
		}
	}
	return layers, err
}

func where(repo Repository, p string) string { return path.Join(repo.Name, p) }

// unwrapPath strips the path of an fs.PathError from err. Messages name the
// path themselves, with its repository.
func unwrapPath(err error) error {
	if pe := (*fs.PathError)(nil); errors.As(err, &pe) {
		return pe.Err
	}
	return err
}
