// Package repository: the tree of a deployment (docs/01_usage.md,
// repository layout), in one or more repositories, read as the instances
// CompileAll compiles.
package repository

import (
	"bytes"
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

	"github.com/okdp/okdp-compiler/internal/chart"
	"github.com/okdp/okdp-compiler/internal/okdp"
)

// Repository: a part of the tree: its name in messages (its directory, on
// the command line), its files and its role, what it may contribute:
// required, the zero value refused (Load, LoadCatalog).
type Repository struct {
	Name string
	FS   fs.FS
	Role Role
}

// Role: what a repository contributes to the tree. Its files are trusted
// for their place in the tree (docs/04_threat_model.md): a part outside
// its role is refused, naming the repository and the path, rather than
// read, so that a catalog's maintainers add no project, privileged or
// not, nor the platform's values.
type Role string

const (
	// RoleTree: a repository of the deployment, the platform's or a
	// project's: any part of the tree, PlatformFile, projects/, catalog/
	// and contracts/.
	RoleTree Role = "tree"
	// RoleCatalog: a catalog of packages: catalog/ and contracts/ only;
	// PlatformFile or projects/ in it refused.
	RoleCatalog Role = "catalog"
)

// The names of the tree.
const (
	PlatformFile       = "platform.yaml"    // the platform's values, context level 0
	ContractsDir       = "contracts"        // <contract>.schema.json
	CatalogDir         = "catalog"          // <service>/<version>/, a package
	ProjectsDir        = "projects"         // <project>/, a namespace
	ProjectFile        = "project.yaml"     // a project's values, context level 1
	ProjectDescription = "description"      // ProjectFile's key describing the project, a string: no value of the context
	InstancesDir       = "instances"        // <project>/instances/<instance>/, the release <project>-<instance>
	InstanceFile       = "instance.yaml"    // {service, version, claimers}: the instance's package, and the namespaces that may claim of it
	ConnectionsFile    = "connections.yaml" // the instance's own connections, context level 2
	ConfigFile         = "config.yaml"      // the instance's values
)

// ReleaseName: the release of the instance of project, <project>-<instance>,
// the project's directory name and the instance's: the releases of a tree
// are unique across its projects, as the HelmReleases or Applications of a
// deployer, which share a namespace, need them (the targets refuse the
// names that hold a "-" and meet, a-b/c and a/b-c).
func ReleaseName(project, instance string) string { return project + "-" + instance }

// parts: the repositories merged, the owner of each part.
type parts struct {
	platform  *Repository            // the owner of PlatformFile, nil when none
	services  map[string]*Repository // the owner of catalog/<service>/, by service
	projects  map[string]*Repository // the owner of projects/<project>/, by project
	contracts fstest.MapFS           // every contract file
	packages  map[[2]string]*okdp.Definition
}

// Load: every instance of the tree that repos make together, by project
// then instance, in name order. Each part of the tree is in one
// repository, so that the order of repos changes nothing: PlatformFile;
// catalog/<service>/, every version of the service; projects/<project>/,
// the project's file and its instances. A contract may be in several, the
// same bytes in each. Each repository has its role (Repository.Role): a
// catalog's (RoleCatalog) are catalog/ and contracts/, its PlatformFile or
// projects/ refused; a tree's (RoleTree), every part. An instance, projects/<project>/instances/<instance>/,
// is: the package its InstanceFile names, catalog/<service>/<version>/ of
// the repository of the service, loaded once, its Chart.yaml of that name
// and version; the contracts of every repository; the context,
// PlatformFile at level 0, without claims, ProjectFile at 1, its named
// claims and its access (okdp.AccessKey, which the compile reads apart)
// among it, without its ProjectDescription, and ConnectionsFile at
// 2, connections only, and the values, ConfigFile, each when present; the
// name, the instance's directory name, the release, ReleaseName of the
// project and the name, and the namespace, the project's; the claimers
// of its InstanceFile, the namespaces that may claim of it across
// namespaces (Inputs.Claimers). CatalogsFile, in one tree's repository
// at most, read strictly, its menu of services of the catalogs
// (checkCatalogs). A project without instance has the declarations of
// its ProjectFile, its connections and its named claims, checked as an
// instance's compile checks its context's (okdp.CheckDeclarations).
// Every problem at once, each naming its repository and path; a package
// that fails once. A tree with any problem is an error and gives no
// instance: LoadProjects loads what it can.
func Load(repos []Repository) ([]okdp.Instance, error) {
	l, err := LoadProjects(repos)
	if err != nil {
		return nil, err
	}
	if len(l.Refused) > 0 {
		return nil, newError(l.errs)
	}
	return l.Instances, nil
}

// Loaded: a tree LoadProjects read: the instances of the projects that
// load, and the projects that do not.
type Loaded struct {
	// Instances are those of the projects that load, as Load gives them.
	Instances []okdp.Instance
	// Refused are the projects that do not load, in name order.
	Refused []Refused

	errs []error // every problem, in the order Load lists them
}

// Refused: a project that does not load: one problem of its own, its
// project.yaml, an instance's files or its package's version, refuses
// the project as a whole (no instance of it), not the tree.
type Refused struct {
	// Project is the project's name, its directory's.
	Project string
	// Problems are why, each naming its repository and path, as an
	// *Error lists them.
	Problems []string
	// Errs are the errors of Problems, one each.
	Errs []error
	// Inputs are the project's context, for a caller that needs what the
	// platform decides of it though it does not load (its Namespace, the
	// platform's layers and the project's, the contracts), no instance.
	Inputs okdp.Inputs
	// Own: Inputs holds the project's own layer, its ProjectFile read (or
	// none); not when it does not read: the platform's alone.
	Own bool
}

// packageError: why a package fails to load, the catalog's, not the
// instance's that names it.
type packageError struct{ err error }

func (e *packageError) Error() string { return e.err.Error() }
func (e *packageError) Unwrap() error { return e.err }

// LoadProjects: Load, per project. A problem of a part of the platform,
// the tree's repositories and their roles, PlatformFile, CatalogsFile,
// a catalog's package or its contracts, is an error (an *Error with every
// problem, the projects' too, in Load's order); a problem of a project's
// own (its ProjectFile, an instance's files, the package version it
// names, a connection or a claim it declares) refuses that project as a
// whole, listed in Loaded.Refused with its problems, and the instances of
// the others are loaded.
func LoadProjects(repos []Repository) (*Loaded, error) {
	t, err := index(repos)
	if err != nil {
		return nil, newError([]error{err})
	}
	var errs []error // every problem, in Load's order
	var platformErrs []error
	for _, r := range repos {
		if r.Role == RoleTree {
			p := t.checkCatalogs(r)
			errs = append(errs, p...)
			platformErrs = append(platformErrs, p...)
		}
	}
	var platform []okdp.ContextLayer
	if t.platform != nil {
		platform, err = readLayer(*t.platform, PlatformFile, 0)
		errs = append(errs, err)
		platformErrs = append(platformErrs, err)
		// The named claims are a project's, of its namespace: the
		// platform's connections serve every project.
		for _, l := range platform {
			if _, set := l.Values["claims"]; set {
				err := fmt.Errorf("%s: claims: a project's (%s)", where(*t.platform, PlatformFile), ProjectFile)
				errs = append(errs, err)
				platformErrs = append(platformErrs, err)
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
		// A project without instance: its declarations checked as an
		// instance's compile checks its context's, which binds them.
		if err == nil && len(names) == 0 && layer != nil {
			in := okdp.Inputs{Contracts: t.contracts, Context: slices.Clip(layers), Namespace: project}
			for _, err := range unjoin(okdp.CheckDeclarations(in)) {
				own = append(own, fmt.Errorf("%s: %w", where(repo, path.Join(dir, ProjectFile)), err))
			}
		}
		var instances []okdp.Instance
		for _, name := range names {
			inst, err := t.instance(repo, project, name, slices.Clip(layers))
			if pe, ok := errors.AsType[*packageError](err); ok {
				// The catalog's problem, not the project's: listed in its
				// place all the same.
				own = append(own, pe)
				platformErrs = append(platformErrs, pe)
				continue
			}
			if err != nil || inst.Definition == nil {
				own = append(own, err)
				continue
			}
			instances = append(instances, inst)
		}
		errs = append(errs, own...)
		own = slices.DeleteFunc(own, func(err error) bool {
			_, catalog := errors.AsType[*packageError](err)
			return catalog
		})
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
	l.errs = errs
	if err := newError(platformErrs); err != nil {
		return nil, newError(errs)
	}
	return l, nil
}

// refusal: the Refused of project for its problems errs, the nil ones left
// out and those errors.Join joined listed one by one; nil when there is
// none.
func refusal(project string, errs []error) *Refused {
	e, ok := newError(errs).(*Error)
	if !ok {
		return nil
	}
	return &Refused{Project: project, Problems: e.Problems, Errs: e.errs}
}

// instance: the instance name of project, in repo, the context layers
// below its own: its directory, of its files only (instanceFiles); its
// InstanceFile, {service, version}, the package, and
// claimers, optional, the namespaces whose instances may claim of it
// across namespaces, each a namespace name or "*", once
// (okdp.CheckClaimers): Inputs.Claimers, nil when absent, the package's
// then. Without its Definition when its package failed for an instance
// before it.
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
			// The catalog's problem: the tree's, not the project's.
			err = &packageError{err}
		}
		return okdp.Instance{}, err
	}
	connections, err := readLayer(repo, path.Join(dir, ConnectionsFile), 2)
	if err != nil {
		return okdp.Instance{}, err
	}
	// The instance's own connections only: the rest of the context, global
	// among it, is the platform's or its project's.
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

// instanceFiles: the entries of the instance's directory dir of repo,
// each one of its files, InstanceFile, ConfigFile and ConnectionsFile,
// a file: any other (a values.yaml, a directory, a hidden file) would be
// read by no compile. Every one at once, in name order.
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

// InstanceSpec: an instance's InstanceFile, as written: its package,
// catalog/<service>/<version>/, and its claimers, the namespaces whose
// instances may claim of it from another namespace (namespace names, or
// "*"): nil when absent, its package's then; empty for none (claimers:
// []).
type InstanceSpec struct {
	Service  string   `yaml:"service"`
	Version  string   `yaml:"version"`
	Claimers []string `yaml:"claimers"`
}

// ParseInstance: an InstanceFile, its first document, strictly: a key
// that is none of InstanceSpec's refused, a duplicate one, a value of
// another form than a string, or a list of strings for the claimers. What
// Load checks after it (service and version required, the claimers) is
// the compile's. Its messages name the keys and the lines, no Go type.
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

// claimersNode: whether n is a value of claimers: null, or a list of
// scalars.
func claimersNode(n *yaml.Node) bool {
	switch n.Kind {
	case yaml.ScalarNode:
		return n.Tag == "!!null"
	case yaml.SequenceNode:
		return !slices.ContainsFunc(n.Content, func(c *yaml.Node) bool { return c.Kind != yaml.ScalarNode })
	}
	return false
}

// definition: the package of version of service, catalog/<service>/<version>/
// of the repository of the service, loaded once, its Chart.yaml of that
// name and version. The error of a version the catalog lacks is the
// instance's; that of the package's load, its own, the first time only:
// nil and no error after.
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
	def, err := okdp.LoadDefinition(files)
	if err == nil && (def.Chart.Name != service || def.Chart.Version != version) {
		err = fmt.Errorf("its Chart.yaml is %s %s", def.Chart.Name, def.Chart.Version)
	}
	if err != nil {
		return nil, &packageError{fmt.Errorf("the package %s: %w", where(*repo, dir), err)}
	}
	t.packages[key] = def
	return def, nil
}

// index: the owner of each part of the tree, every conflict at once, the
// repositories in name order. A repository without a role, or of another
// role than RoleTree and RoleCatalog, is refused; a catalog's PlatformFile
// or projects/, there at all (a file, an empty directory), refused, the
// rest of it read.
func index(repos []Repository) (*parts, error) {
	repos = slices.SortedFunc(slices.Values(repos), func(a, b Repository) int { return strings.Compare(a.Name, b.Name) })
	t := &parts{services: map[string]*Repository{}, projects: map[string]*Repository{}, contracts: fstest.MapFS{}, packages: map[[2]string]*okdp.Definition{}}
	var errs []error
	owners := map[string][]string{} // a part, as messages name it: the repositories that have it
	var parts []string
	own := func(part string, repo *Repository) {
		if owners[part] == nil {
			parts = append(parts, part)
		}
		owners[part] = append(owners[part], repo.Name)
	}
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
			// The platform's values and the projects are the deployment's:
			// a catalog that holds them is refused, not read without them.
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
				t.contracts[e.Name()] = &fstest.MapFile{Data: b, Mode: 0o444}
				from[e.Name()] = repo.Name
			case !bytes.Equal(prev.Data, b):
				errs = append(errs, fmt.Errorf("contract %s differs between the repositories %s and %s", name, from[e.Name()], repo.Name))
			}
		}
	}
	for _, part := range parts {
		if names := owners[part]; len(names) > 1 {
			errs = append(errs, fmt.Errorf("%s is in the repositories %s and %s", part, strings.Join(names[:len(names)-1], ", "), names[len(names)-1]))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return t, nil
}

// dirs: the directories of dir in repo, in name order; none when dir does
// not exist.
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

// readLayer: the values file of repo, a context layer of level; none when
// it does not exist.
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

// readProject: a project's file, the context layer of level 1, without
// its ProjectDescription, a string, or null: the project's own, which no
// instance receives.
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

// where: a path of repo, as messages name it.
func where(repo Repository, p string) string { return path.Join(repo.Name, p) }

// unwrapPath: err without the path of an fs.PathError: messages name the
// path, with its repository.
func unwrapPath(err error) error {
	if pe := (*fs.PathError)(nil); errors.As(err, &pe) {
		return pe.Err
	}
	return err
}
