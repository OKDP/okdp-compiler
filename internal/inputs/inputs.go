// Package inputs reads the input documents of a compile from files laid
// out in any way (docs/specs/01_inputs_outputs.md, input documents). Each
// YAML document says what it is by its kind:
//
//	kind: Settings   the values every instance receives first
//	kind: Project    a namespace, and the values its instances receive
//	kind: Instance   an instance: its project, name, package and values
//
// A document gives values inline (values) or in values files
// (valuesFiles, relative to the document's file), read as Helm reads
// them. A file named by valuesFiles is a values file, not a document file.
package inputs

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/okdp/okdp-compiler/chart"
	"github.com/okdp/okdp-compiler/internal/catalog"
	"github.com/okdp/okdp-compiler/internal/okdp"
	"github.com/okdp/okdp-compiler/value"
)

// The kinds of the input documents.
const (
	KindSettings = "Settings"
	KindProject  = "Project"
	KindInstance = "Instance"
)

// Files is a directory of input files, or one file of it. Name
// identifies it in messages. Path is the file, or "" for every .yaml and
// .yml file under FS.
type Files struct {
	Name string
	FS   fs.FS
	Path string
}

// file is a YAML file read from a Files.
type file struct {
	src  *Files
	path string
	docs []*yaml.Node // its documents
}

func (f *file) where() string { return path.Join(f.src.Name, f.path) }

// document is one document of a file, of a known kind.
type document struct {
	file *file
	n    int // its position in the file, from 1
	node *yaml.Node
	kind string
	m    map[string]*yaml.Node // its keys
}

func (d *document) where() string {
	return fmt.Sprintf("%s: document %d (line %d, %s)", d.file.where(), d.n, root(d.node).Line, d.kind)
}

// project is a Project document read.
type project struct {
	doc    *document
	values []map[string]any
}

// instance is an Instance document read.
type instance struct {
	doc              *document
	project, name    string
	service, version string
	claimers         []string
	connections      map[string]any
	values           []map[string]any
}

// Load reads the input documents of sources and returns the instances
// they describe, sorted by project, then by name, as CompileAll compiles
// them. Their packages are cat's, read for rules (nil: the mandatory
// set), and their contracts cat's contracts.
//
// The instance name of project p is the release p-name of the namespace
// p. Its values layers are the Settings document's, its project's, its
// own connections and its own values, in this order. The error lists
// every problem at once.
func Load(ctx context.Context, sources []Files, cat *catalog.Catalog, rules *okdp.RuleSet) ([]okdp.Instance, error) {
	var errs []error
	var files []*file
	for i := range sources {
		fs, err := read(&sources[i])
		errs = append(errs, err)
		files = append(files, fs...)
	}
	docs, err := documents(files)
	errs = append(errs, err)
	var settings []map[string]any
	var settingsAt string
	projects := map[string]*project{}
	var instances []*instance
	for _, d := range docs {
		switch d.kind {
		case KindSettings:
			if settingsAt != "" {
				errs = append(errs, fmt.Errorf("%s: a second Settings document, after %s: give the values of every instance once", d.where(), settingsAt))
				continue
			}
			settingsAt = d.where()
			v, err := d.values("kind")
			errs = append(errs, err)
			settings = v
		case KindProject:
			p, err := readProject(d)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			name := d.str("name")
			if prev, ok := projects[name]; ok {
				errs = append(errs, fmt.Errorf("%s: project %q is given twice, first in %s", d.where(), name, prev.doc.where()))
				continue
			}
			projects[name] = p
		case KindInstance:
			in, err := readInstance(d)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			instances = append(instances, in)
		}
	}
	slices.SortStableFunc(instances, func(a, b *instance) int {
		return cmp.Or(strings.Compare(a.project, b.project), strings.Compare(a.name, b.name))
	})
	var out []okdp.Instance
	defs := map[[2]string]*okdp.Definition{}
	failed := map[[2]string]bool{}
	for i, in := range instances {
		if i > 0 && instances[i-1].project == in.project && instances[i-1].name == in.name {
			errs = append(errs, fmt.Errorf("%s: instance %s/%s is given twice, first in %s", in.doc.where(), in.project, in.name, instances[i-1].doc.where()))
			continue
		}
		key := [2]string{in.service, in.version}
		def, ok := defs[key]
		if !ok && !failed[key] {
			var err error
			def, err = definition(cat, in.service, in.version, rules)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", in.doc.where(), err))
				failed[key] = true
				continue
			}
			defs[key] = def
		}
		if def == nil {
			continue
		}
		layers := slices.Clone(settings)
		if p, ok := projects[in.project]; ok {
			layers = append(layers, p.values...)
		}
		if in.connections != nil {
			layers = append(layers, map[string]any{"connections": in.connections})
		}
		out = append(out, okdp.Instance{Definition: def, Name: in.name, Inputs: okdp.Inputs{
			Contracts: cat.Contracts(),
			Values:    append(layers, in.values...),
			Release:   in.project + "-" + in.name,
			Namespace: in.project,
			Claimers:  in.claimers,
		}})
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return out, nil
}

// read returns the YAML files of src, sorted by path.
func read(src *Files) ([]*file, error) {
	var paths []string
	if src.Path != "" && src.Path != "." {
		paths = []string{src.Path}
	} else {
		err := fs.WalkDir(src.FS, ".", func(p string, e fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !e.IsDir() && (strings.HasSuffix(p, ".yaml") || strings.HasSuffix(p, ".yml")) {
				paths = append(paths, p)
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("%s: %w", src.Name, err)
		}
	}
	var out []*file
	var errs []error
	for _, p := range paths {
		b, err := fs.ReadFile(src.FS, p)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", path.Join(src.Name, p), value.UnwrapPath(err)))
			continue
		}
		f := &file{src: src, path: p}
		dec := yaml.NewDecoder(bytes.NewReader(b))
		for {
			n := &yaml.Node{}
			err := dec.Decode(n)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				f.docs = nil
				errs = append(errs, fmt.Errorf("%s: %w", f.where(), err))
				break
			}
			f.docs = append(f.docs, n)
		}
		out = append(out, f)
	}
	return out, errors.Join(errs...)
}

// documents returns the documents of files, in order. A file named by a
// document's valuesFiles is a values file: its documents are not read. In
// every other file, each document must be of a known kind.
func documents(files []*file) ([]*document, error) {
	var errs []error
	valuesFiles := map[string]bool{} // by where()
	byFile := map[*file][]*document{}
	for _, f := range files {
		for i, n := range f.docs {
			d := &document{file: f, n: i + 1, node: n}
			d.m, d.kind = keys(n)
			byFile[f] = append(byFile[f], d)
			if !slices.Contains([]string{KindSettings, KindProject, KindInstance}, d.kind) {
				continue
			}
			for _, vf := range d.strs("valuesFiles") {
				if p, ok := d.resolve(vf); ok {
					valuesFiles[path.Join(f.src.Name, p)] = true
				}
			}
		}
	}
	var out []*document
	for _, f := range files {
		if valuesFiles[f.where()] {
			continue
		}
		for _, d := range byFile[f] {
			switch d.kind {
			case KindSettings, KindProject, KindInstance:
				out = append(out, d)
			case "":
				if root(d.node).Kind == yaml.ScalarNode && root(d.node).Tag == "!!null" {
					continue // an empty document
				}
				errs = append(errs, fmt.Errorf("%s: document %d (line %d): no kind: %s, %s or %s, or a values file that a document names (valuesFiles)", f.where(), d.n, root(d.node).Line, KindSettings, KindProject, KindInstance))
			default:
				errs = append(errs, fmt.Errorf("%s: document %d (line %d): kind %q: %s, %s or %s", f.where(), d.n, root(d.node).Line, d.kind, KindSettings, KindProject, KindInstance))
			}
		}
	}
	return out, errors.Join(errs...)
}

func root(n *yaml.Node) *yaml.Node {
	if n.Kind == yaml.DocumentNode && len(n.Content) == 1 {
		return n.Content[0]
	}
	return n
}

// keys returns the keys of a mapping document and its kind, "" when it
// is not a mapping or has no string kind.
func keys(n *yaml.Node) (map[string]*yaml.Node, string) {
	r := root(n)
	if r.Kind != yaml.MappingNode {
		return nil, ""
	}
	m := map[string]*yaml.Node{}
	for i := 0; i+1 < len(r.Content); i += 2 {
		if _, dup := m[r.Content[i].Value]; !dup {
			m[r.Content[i].Value] = r.Content[i+1]
		}
	}
	kind := ""
	if k, ok := m["kind"]; ok && k.Kind == yaml.ScalarNode {
		kind = k.Value
	}
	return m, kind
}

// check refuses the keys of d that allowed does not list and a key given
// twice.
func (d *document) check(allowed []string) error {
	r := root(d.node)
	var errs []error
	seen := map[string]int{}
	for i := 0; i+1 < len(r.Content); i += 2 {
		k := r.Content[i]
		if at, dup := seen[k.Value]; dup {
			errs = append(errs, fmt.Errorf("%s: line %d: %s is given twice, first at line %d", d.where(), k.Line, k.Value, at))
			continue
		}
		seen[k.Value] = k.Line
		if !slices.Contains(allowed, k.Value) {
			errs = append(errs, fmt.Errorf("%s: line %d: %s: not a key of the document, which holds %s", d.where(), k.Line, k.Value, strings.Join(allowed, ", ")))
		}
	}
	return errors.Join(errs...)
}

// required refuses a key of keys that d does not set.
func (d *document) required(keys ...string) error {
	var errs []error
	for _, k := range keys {
		if v, ok := d.m[k]; !ok || v.Kind == yaml.ScalarNode && v.Tag == "!!null" {
			errs = append(errs, fmt.Errorf("%s: %s is required", d.where(), k))
		}
	}
	return errors.Join(errs...)
}

// str returns the string at key k, "" when it is absent or not a string.
func (d *document) str(k string) string {
	if v, ok := d.m[k]; ok && v.Kind == yaml.ScalarNode && v.Tag != "!!null" {
		return v.Value
	}
	return ""
}

// strs returns the strings of the list at key k, nil when it is absent or
// not a list of strings.
func (d *document) strs(k string) []string {
	v, ok := d.m[k]
	if !ok || v.Kind != yaml.SequenceNode {
		return nil
	}
	out := []string{}
	for _, c := range v.Content {
		if c.Kind != yaml.ScalarNode {
			return nil
		}
		out = append(out, c.Value)
	}
	return out
}

// name checks that the string at key k names a namespace or an instance.
func (d *document) name(k string) error {
	v, ok := d.m[k]
	if !ok {
		return nil
	}
	if v.Kind != yaml.ScalarNode || !chart.IsDNSLabel(v.Value) {
		return fmt.Errorf("%s: line %d: %s: a DNS label expected (lowercase letters, digits and -, 63 characters at most)", d.where(), v.Line, k)
	}
	return nil
}

// list checks that the value at key k, when set, is a list of strings.
func (d *document) list(k string) error {
	v, ok := d.m[k]
	if ok && !(v.Kind == yaml.ScalarNode && v.Tag == "!!null") && d.strs(k) == nil {
		return fmt.Errorf("%s: line %d: %s: a list of strings expected", d.where(), v.Line, k)
	}
	return nil
}

// resolve returns the path of the values file p names, relative to the
// document's file, false when it leaves the files of the document.
func (d *document) resolve(p string) (string, bool) {
	if path.IsAbs(p) {
		return "", false
	}
	full := path.Join(path.Dir(d.file.path), p)
	return full, fs.ValidPath(full) && full != "."
}

// mapping decodes the value at key k as Helm reads a values file: YAML
// 1.1, string keys, every number a float64. It returns nil when k is
// absent or null.
func (d *document) mapping(k string) (map[string]any, error) {
	v, ok := d.m[k]
	if !ok || v.Kind == yaml.ScalarNode && v.Tag == "!!null" {
		return nil, nil
	}
	if v.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%s: line %d: %s: a mapping expected", d.where(), v.Line, k)
	}
	b, err := yaml.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("%s: %s: %w", d.where(), k, err)
	}
	m, err := chart.ParseValues(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %s: %w", d.where(), k, err)
	}
	return m, nil
}

// values returns the values layers of d: each of valuesFiles, in order,
// then values. The keys of d are checked against allowed, besides
// values and valuesFiles.
func (d *document) values(allowed ...string) ([]map[string]any, error) {
	var errs []error
	if err := d.list("valuesFiles"); err != nil {
		errs = append(errs, err)
	}
	var out []map[string]any
	for _, vf := range d.strs("valuesFiles") {
		p, ok := d.resolve(vf)
		if !ok {
			errs = append(errs, fmt.Errorf("%s: valuesFiles: %q: a path relative to the document's file, inside %s", d.where(), vf, d.file.src.Name))
			continue
		}
		b, err := fs.ReadFile(d.file.src.FS, p)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: valuesFiles: %s: %w", d.where(), path.Join(d.file.src.Name, p), value.UnwrapPath(err)))
			continue
		}
		v, err := chart.ParseValues(b)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: valuesFiles: %s: %w", d.where(), path.Join(d.file.src.Name, p), err))
			continue
		}
		out = append(out, v)
	}
	inline, err := d.mapping("values")
	errs = append(errs, err)
	if inline != nil {
		out = append(out, inline)
	}
	keys := slices.Clone(allowed)
	for _, k := range []string{"values", "valuesFiles"} {
		if !slices.Contains(keys, k) {
			keys = append(keys, k)
		}
	}
	if err := d.check(keys); err != nil {
		errs = append(errs, err)
	}
	return out, errors.Join(errs...)
}

// readProject reads a Project document.
func readProject(d *document) (*project, error) {
	errs := []error{d.name("name"), d.required("name")}
	v, err := d.values("kind", "name")
	errs = append(errs, err)
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return &project{doc: d, values: v}, nil
}

// readInstance reads an Instance document.
func readInstance(d *document) (*instance, error) {
	allowed := []string{"kind", "project", "name", "service", "version", "claimers", "connections", "values", "valuesFiles"}
	errs := []error{d.name("project"), d.name("name"), d.list("claimers")}
	errs = append(errs, d.required("project", "name", "service", "version"))
	for _, k := range []string{"service", "version"} {
		if v, ok := d.m[k]; ok && v.Kind != yaml.ScalarNode {
			errs = append(errs, fmt.Errorf("%s: line %d: %s: a string expected", d.where(), v.Line, k))
		}
	}
	in := &instance{doc: d, project: d.str("project"), name: d.str("name"), service: d.str("service"), version: d.str("version")}
	if v, ok := d.m["claimers"]; ok && !(v.Kind == yaml.ScalarNode && v.Tag == "!!null") {
		in.claimers = d.strs("claimers")
		if in.claimers != nil {
			if err := okdp.CheckClaimers("claimers", in.claimers); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", d.where(), err))
			}
		}
	}
	conns, err := d.mapping("connections")
	errs = append(errs, err)
	in.connections = conns
	values, err := d.values(allowed...)
	errs = append(errs, err)
	in.values = values
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return in, nil
}

// definition loads the package service version of cat for rules. Its
// Chart.yaml must have that name and version.
func definition(cat *catalog.Catalog, service, version string, rules *okdp.RuleSet) (*okdp.Definition, error) {
	files, err := cat.Package(service, version)
	if err != nil {
		return nil, err
	}
	def, err := okdp.LoadDefinition(files, rules)
	if err == nil && (def.Chart.Name != service || def.Chart.Version != version) {
		err = fmt.Errorf("its Chart.yaml is %s %s", def.Chart.Name, def.Chart.Version)
	}
	if err != nil {
		return nil, fmt.Errorf("the package %s: %w", cat.PackagePath(service, version), err)
	}
	return def, nil
}
