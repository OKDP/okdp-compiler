// Package repository: the tree of a deployment (docs/01_usage.md,
// repository layout), in one or more repositories, read as the instances
// CompileAll compiles.
package repository

import (
	"bytes"
	"errors"
	"fmt"
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
// the command line) and its files.
type Repository struct {
	Name string
	FS   fs.FS
}

// The names of the tree.
const (
	PlatformFile    = "platform.yaml"    // the platform's values, context level 0
	ContractsDir    = "contracts"        // <contract>.schema.json
	CatalogDir      = "catalog"          // <service>/<version>/, a package
	ProjectsDir     = "projects"         // <project>/, a namespace
	ProjectFile     = "project.yaml"     // a project's values, context level 1
	InstancesDir    = "instances"        // <project>/instances/<instance>/, a release
	InstanceFile    = "instance.yaml"    // {service, version}: the instance's package
	ConnectionsFile = "connections.yaml" // the instance's own connections, context level 2
	ConfigFile      = "config.yaml"      // the instance's values
)

// tree: the repositories merged, the owner of each part.
type tree struct {
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
// same bytes in each. An instance, projects/<project>/instances/<instance>/,
// is: the package its InstanceFile names, catalog/<service>/<version>/ of
// the repository of the service, loaded once, its Chart.yaml of that name
// and version; the contracts of every repository; the context,
// PlatformFile at level 0, ProjectFile at 1 and ConnectionsFile at 2,
// connections only, and the values, ConfigFile, each when present; the release, the instance's
// directory name, and the namespace, the project's. Every problem at once,
// each naming its repository and path; a package that fails once.
func Load(repos []Repository) ([]okdp.Instance, error) {
	t, err := index(repos)
	if err != nil {
		return nil, err
	}
	var errs []error
	var platform []okdp.ContextLayer
	if t.platform != nil {
		platform, err = readLayer(*t.platform, PlatformFile, 0)
		errs = append(errs, err)
	}
	var instances []okdp.Instance
	for _, project := range slices.Sorted(maps.Keys(t.projects)) {
		repo := *t.projects[project]
		dir := path.Join(ProjectsDir, project)
		layer, err := readLayer(repo, path.Join(dir, ProjectFile), 1)
		layers := append(slices.Clone(platform), layer...)
		errs = append(errs, err)
		names, err := dirs(repo, path.Join(dir, InstancesDir))
		errs = append(errs, err)
		for _, name := range names {
			inst, err := t.instance(repo, project, name, slices.Clip(layers))
			if err != nil || inst.Definition == nil {
				errs = append(errs, err)
				continue
			}
			instances = append(instances, inst)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return instances, nil
}

// instance: the instance name of project, in repo, the context layers
// below its own. Without its Definition when its package failed for an
// instance before it.
func (t *tree) instance(repo Repository, project, name string, layers []okdp.ContextLayer) (okdp.Instance, error) {
	dir := path.Join(ProjectsDir, project, InstancesDir, name)
	file := path.Join(dir, InstanceFile)
	var pkg struct {
		Service string `yaml:"service"`
		Version string `yaml:"version"`
	}
	b, err := fs.ReadFile(repo.FS, file)
	if err == nil {
		dec := yaml.NewDecoder(bytes.NewReader(b))
		dec.KnownFields(true)
		err = dec.Decode(&pkg)
	}
	if err == nil && (pkg.Service == "" || pkg.Version == "") {
		err = errors.New("service and version are required")
	}
	if err != nil {
		return okdp.Instance{}, fmt.Errorf("%s: %w", where(repo, file), unwrapPath(err))
	}
	def, err := t.definition(pkg.Service, pkg.Version)
	if err != nil {
		return okdp.Instance{}, fmt.Errorf("%s: %w", where(repo, file), err)
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
	in := okdp.Inputs{Contracts: t.contracts, Context: append(layers, connections...), Release: name, Namespace: project}
	for _, l := range values {
		in.Values = append(in.Values, l.Values)
	}
	return okdp.Instance{Definition: def, Inputs: in}, nil
}

// definition: the package of version of service, catalog/<service>/<version>/
// of the repository of the service, loaded once, its Chart.yaml of that
// name and version. The error of a version the catalog lacks is the
// instance's; that of the package's load, its own, the first time only:
// nil and no error after.
func (t *tree) definition(service, version string) (*okdp.Definition, error) {
	repo, ok := t.services[service]
	if !ok {
		return nil, fmt.Errorf("service %q is in the catalog of no repository", service)
	}
	dir := path.Join(CatalogDir, service, version)
	if _, err := fs.Stat(repo.FS, dir); err != nil || !segment(version) {
		return nil, fmt.Errorf("version %q of service %q is not in %s", version, service, where(*repo, path.Join(CatalogDir, service)))
	}
	key := [2]string{service, version}
	if def, loaded := t.packages[key]; loaded {
		return def, nil
	}
	t.packages[key] = nil
	files, err := fs.Sub(repo.FS, dir)
	if err != nil {
		return nil, err
	}
	def, err := okdp.LoadDefinition(files)
	if err == nil && (def.Chart.Name != service || def.Chart.Version != version) {
		err = fmt.Errorf("its Chart.yaml is %s %s", def.Chart.Name, def.Chart.Version)
	}
	if err != nil {
		return nil, fmt.Errorf("the package %s: %w", where(*repo, dir), err)
	}
	t.packages[key] = def
	return def, nil
}

// index: the owner of each part of the tree, every conflict at once, the
// repositories in name order.
func index(repos []Repository) (*tree, error) {
	repos = slices.SortedFunc(slices.Values(repos), func(a, b Repository) int { return strings.Compare(a.Name, b.Name) })
	t := &tree{services: map[string]*Repository{}, projects: map[string]*Repository{}, contracts: fstest.MapFS{}, packages: map[[2]string]*okdp.Definition{}}
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
		if _, err := fs.Stat(repo.FS, PlatformFile); err == nil {
			own(PlatformFile, repo)
			t.platform = repo
		} else if !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, fmt.Errorf("%s: %w", where(*repo, PlatformFile), unwrapPath(err)))
		}
		services, err := dirs(*repo, CatalogDir)
		errs = append(errs, err)
		for _, s := range services {
			own(fmt.Sprintf("service %q", s), repo)
			t.services[s] = repo
		}
		projects, err := dirs(*repo, ProjectsDir)
		errs = append(errs, err)
		for _, p := range projects {
			own(fmt.Sprintf("project %q", p), repo)
			t.projects[p] = repo
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

// segment: whether s is one segment of a path, not . or ...
func segment(s string) bool { return fs.ValidPath(s) && s != "." && !strings.Contains(s, "/") }

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
