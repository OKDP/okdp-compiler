package treeedit

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"reflect"
	"slices"

	compiler "github.com/okdp/okdp-compiler"
)

// Instance is an instance of the tree, projects/<project>/instances/<name>/:
// the package of its instance.yaml, and its values, config.yaml. The
// directory is its identity: the project is its namespace, its release
// compiler.ReleaseName(project, name).
type Instance struct {
	Project string `yaml:"-"`
	Name    string `yaml:"-"`
	// Service and Version are instance.yaml: the package
	// catalog/<service>/<version>/ of a catalog.
	Service string `yaml:"service"`
	Version string `yaml:"version"`
	// Claimers are instance.yaml's claimers, as written: the namespaces
	// whose instances may claim of the instance, a provider, from another
	// namespace (namespace names, or "*"), which the compile checks; nil
	// when absent (its package's then), empty for none ("claimers: []"),
	// kept apart when the file is written again.
	Claimers []string `yaml:"claimers,omitempty"`
	// Values are config.yaml: the parameters and the upstream values; {}
	// without the file.
	Values map[string]any `yaml:"-"`
}

// Release is the instance's Helm release.
func (i Instance) Release() string { return compiler.ReleaseName(i.Project, i.Name) }

// Dir is the instance's directory in the tree.
func (i Instance) Dir() string { return InstanceDir(i.Project, i.Name) }

// Validate refuses an instance whose directory cannot be written, or
// whose instance.yaml names no package. The rest is the compile's.
func (i Instance) Validate() error {
	if err := ValidateName("project", i.Project); err != nil {
		return err
	}
	if err := ValidateName("instance", i.Name); err != nil {
		return err
	}
	if i.Service == "" || i.Version == "" {
		return errors.New("service and version are required: the package of the catalogs")
	}
	return nil
}

// Spec is the instance's instance.yaml (InstanceSpec).
func (i Instance) Spec() InstanceSpec {
	return InstanceSpec{Service: i.Service, Version: i.Version, Claimers: i.Claimers}
}

// ReadInstance reads the instance.yaml and config.yaml of an instance,
// or fails with an error wrapping ErrNotFound.
func ReadInstance(r Reader, project, name string) (*Instance, error) {
	if err := checkRef("instance", project, name); err != nil {
		return nil, err
	}
	return readInstance(r, project, name)
}

func readInstance(r Reader, project, name string) (*Instance, error) {
	dir := InstanceDir(project, name)
	raw, err := r.ReadFile(path.Join(dir, compiler.InstanceFile))
	if err != nil {
		return nil, err
	}
	spec, err := ParseInstance(raw)
	if err != nil {
		return nil, fmt.Errorf("%s/%s: %w", dir, compiler.InstanceFile, err)
	}
	// The directory is the identity.
	inst := Instance{Project: project, Name: name, Service: spec.Service, Version: spec.Version, Claimers: spec.Claimers}
	inst.Values = map[string]any{}
	if rawValues, err := r.ReadFile(path.Join(dir, compiler.ConfigFile)); err == nil {
		if inst.Values, err = DecodeValues(rawValues); err != nil {
			return nil, fmt.Errorf("%s/%s: %w", dir, compiler.ConfigFile, err)
		}
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	return &inst, nil
}

// ListInstances returns the instances of a project, by name: the
// directories of its instances that hold an instance.yaml.
func ListInstances(r Reader, project string) ([]Instance, error) {
	names, err := r.ReadDir(InstancesDir(project))
	if err != nil {
		return nil, err
	}
	var out []Instance
	for _, name := range names {
		if !r.Exists(path.Join(InstanceDir(project, name), compiler.InstanceFile)) {
			continue
		}
		inst, err := readInstance(r, project, name)
		if err != nil {
			return nil, err
		}
		out = append(out, *inst)
	}
	return out, nil
}

// writeInstance adds to c the instance.yaml and config.yaml of inst, each
// only when its content changes, so that a hand-written file keeps its
// comments through the edits that leave it alone (a version bump does
// not touch config.yaml); no config.yaml for an instance without values.
func writeInstance(r Reader, c *Change, inst *Instance) error {
	if err := inst.Validate(); err != nil {
		return err
	}
	dir := inst.Dir()
	current, err := readInstance(r, inst.Project, inst.Name)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	if current == nil || current.Service != inst.Service || current.Version != inst.Version ||
		(current.Claimers == nil) != (inst.Claimers == nil) || !slices.Equal(current.Claimers, inst.Claimers) {
		// claimers: [] (none) is not an absent claimers (the package's).
		data, err := EncodeInstance(inst.Spec())
		if err != nil {
			return err
		}
		c.write(path.Join(dir, compiler.InstanceFile), data)
	}
	values := RoundTrip(inst.Values)
	config := path.Join(dir, compiler.ConfigFile)
	if (current != nil && reflect.DeepEqual(current.Values, values)) || (len(values) == 0 && !r.Exists(config)) {
		return nil
	}
	data, err := EncodeValues(values)
	if err != nil {
		return err
	}
	c.write(config, data)
	return nil
}

// CreateInstance declares a new instance of a project, checked: it must
// compile, and its release may not be another instance's. Its change
// fails with ErrExists when the instance's directory is already there,
// ErrNotFound when the project is not declared; the check with a
// *CompileError, or an *ErrReleaseTaken (Check.Verify).
func CreateInstance(inst Instance) (Edit, error) {
	if err := inst.Validate(); err != nil {
		return Edit{}, err
	}
	target := inst.Project + "/" + inst.Name
	return Edit{Action: "deploy", Target: target, change: func(r Reader) (*Change, error) {
		if r.Exists(inst.Dir()) {
			return nil, fmt.Errorf("instance %s: %w", target, ErrExists)
		}
		if !r.Exists(ProjectFilePath(inst.Project)) {
			return nil, fmt.Errorf("project %s: %w", inst.Project, ErrNotFound)
		}
		created := inst
		c := &Change{Check: &Check{Subjects: []Instance{created}, Created: true}, Instance: &created}
		if err := writeInstance(r, c, &created); err != nil {
			return nil, err
		}
		return c, nil
	}}, nil
}

// UpdateInstance applies change to the instance as the tree holds it and
// writes it back (Change.Instance), checked: it must compile, the others
// that compiled still do. change may run more than once (Edit.Change).
// The change fails with ErrNotFound when the instance is not there.
func UpdateInstance(project, name string, change func(inst *Instance) error) (Edit, error) {
	if err := checkRef("instance", project, name); err != nil {
		return Edit{}, err
	}
	return Edit{Action: "update", Target: project + "/" + name, change: func(r Reader) (*Change, error) {
		inst, err := readInstance(r, project, name)
		if err != nil {
			return nil, err
		}
		if err := change(inst); err != nil {
			return nil, err
		}
		inst.Project, inst.Name = project, name
		c := &Change{Check: &Check{Subjects: []Instance{*inst}}, Instance: inst}
		if err := writeInstance(r, c, inst); err != nil {
			return nil, err
		}
		return c, nil
	}}, nil
}

// DeleteInstance removes an instance's directory; the next compile
// removes its compiled files, and the engine prunes what it had
// deployed. The check refuses it (*ErrInUse) while another instance uses
// it, an output it publishes, a claim bound to it: that instance would
// not compile without it. The change fails with ErrNotFound when the
// instance is not there.
func DeleteInstance(project, name string) (Edit, error) {
	if err := checkRef("instance", project, name); err != nil {
		return Edit{}, err
	}
	target := project + "/" + name
	return Edit{Action: "delete", Target: target, change: func(r Reader) (*Change, error) {
		dir := InstanceDir(project, name)
		if !r.Exists(dir) {
			return nil, fmt.Errorf("instance %s: %w", target, ErrNotFound)
		}
		c := &Change{Check: &Check{Project: project, Removed: "instance " + name}}
		c.remove(dir)
		return c, nil
	}}, nil
}

// CompiledFiles are the compiled files of an instance a control plane
// reads back: its umbrella's Chart.yaml and its values.
type CompiledFiles struct {
	// Chart is the umbrella's Chart.yaml.
	Chart []byte
	// Values are the values of each chart key, under the key.
	Values map[string]any
}

// ReadCompiled returns the compiled files of an instance in output, the
// tree a target wrote (compiler.ReadCompiled), or an error wrapping
// ErrNotFound and fs.ErrNotExist when it has none: an instance the
// compile has not written yet.
func ReadCompiled(output fs.FS, project, name string) (*CompiledFiles, error) {
	if err := checkRef("instance", project, name); err != nil {
		return nil, err
	}
	compiled, err := compiler.ReadCompiled(output, project, compiler.ReleaseName(project, name))
	if errors.Is(err, fs.ErrNotExist) && !errors.Is(err, ErrNotFound) {
		return nil, fmt.Errorf("%w: %w", ErrNotFound, err)
	}
	if err != nil {
		return nil, err
	}
	return &CompiledFiles{Chart: compiled.Chart, Values: compiled.Values}, nil
}

// Published is what a compiled instance publishes (okdp.info.yaml), and
// the package it is an instance of (the Chart.yaml beside it), as
// compiler.ReadPublished reads them back.
type Published struct {
	// Name is the instance name, Release its release.
	Name    string
	Release string
	// Service and Version are the package's (Chart.yaml name and version).
	Service string
	Version string
	// Publishes is okdp.info.yaml: the url, the usage and the
	// connections the instance provides.
	Publishes compiler.Info
}

// ListPublished returns what the instances of a project of the tree r
// holds publish in output, the tree a target wrote, their compiled
// okdp.info.yaml, by instance name: those whose compiled directory output
// has. An instance that publishes nothing is listed without URL, usage
// or connection. None for a name that is not a project's.
func ListPublished(r Reader, output fs.FS, project string) ([]Published, error) {
	if ValidateName("project", project) != nil {
		return nil, nil //nolint:nilerr // a name that is not a project's has no instance to list
	}
	names, err := r.ReadDir(InstancesDir(project))
	if err != nil {
		return nil, err
	}
	byRelease := map[string]string{}
	for _, name := range names {
		byRelease[compiler.ReleaseName(project, name)] = name
	}
	compiled, err := compiler.ReadPublished(output, project)
	if err != nil {
		return nil, err
	}
	var out []Published
	for _, c := range compiled {
		if name, ok := byRelease[c.Release]; ok {
			out = append(out, Published{Name: name, Release: c.Release, Service: c.Service, Version: c.Version, Publishes: c.Info})
		}
	}
	return out, nil
}
