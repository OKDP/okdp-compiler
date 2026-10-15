package compiler

import (
	"bytes"
	"errors"
	"flag"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"go.yaml.in/yaml/v3"

	"github.com/okdp/okdp-compiler/internal/chart"
)

var update = flag.Bool("update", false, "rewrite the examples' outputs from the current compiler")

// examples: each example a repository tree (docs/01_usage.md, repository
// layout): its sources under input/db/, what -o <dir> writes for each
// instance in output/projects/<project>/instances/<instance>/, and, for
// --check --charts, a chart cache in .charts/.
const examples = "examples"

// loadInstance: the compile of an instance of an example's tree,
// input/db/projects/<project>/instances/<instance>/, as a caller of the tree
// gives it: the package its instance.yaml names, catalog/<service>/<version>/,
// whose Chart.yaml has that name and version; no chart cache; the
// contracts of db/contracts/, none when absent; the context, platform.yaml at
// level 0, the project's project.yaml at level 1 and the instance's
// connections.yaml at level 2, then the instance values, its config.yaml,
// each when present; the release <instance> and the namespace <project>.
func loadInstance(t *testing.T, example, project, instance string) (*Definition, Inputs) {
	t.Helper()
	root := filepath.Join(examples, example)
	db := filepath.Join(root, "input", "db")
	proj := filepath.Join(db, "projects", project)
	dir := filepath.Join(proj, "instances", instance)
	b, err := os.ReadFile(filepath.Join(dir, "instance.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var pkg struct {
		Service string `yaml:"service"`
		Version string `yaml:"version"`
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&pkg); err != nil {
		t.Fatalf("%s: %v", filepath.Join(dir, "instance.yaml"), err)
	}
	catalog := filepath.Join(db, "catalog", pkg.Service, pkg.Version)
	def, err := LoadDefinition(os.DirFS(catalog))
	if err != nil {
		t.Fatalf("%s: %v", catalog, err)
	}
	if def.Chart.Name != pkg.Service || def.Chart.Version != pkg.Version {
		t.Fatalf("%s: Chart.yaml is %s %s", catalog, def.Chart.Name, def.Chart.Version)
	}
	in := Inputs{
		Contracts: fstest.MapFS{},
		Release:   instance,
		Namespace: project,
	}
	if contracts := filepath.Join(db, "contracts"); exists(t, contracts) {
		in.Contracts = os.DirFS(contracts)
	}
	for level, f := range []string{filepath.Join(db, "platform.yaml"), filepath.Join(proj, "project.yaml"), filepath.Join(dir, "connections.yaml")} {
		if exists(t, f) {
			in.Context = append(in.Context, readContext(t, uint(level), f)...)
		}
	}
	if f := filepath.Join(dir, "config.yaml"); exists(t, f) {
		in.Values = readLayers(t, []string{f})
	}
	return def, in
}

// exists: whether path exists.
func exists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	return err == nil
}

// readContext: values files, as context layers of this level.
func readContext(t *testing.T, level uint, files ...string) []ContextLayer {
	t.Helper()
	var layers []ContextLayer
	for _, l := range readLayers(t, files) {
		layers = append(layers, ContextLayer{Level: level, Values: l})
	}
	return layers
}

// readLayers: values files, as values layers.
func readLayers(t *testing.T, files []string) []map[string]any {
	t.Helper()
	var layers []map[string]any
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		v, err := chart.ParseValues(b)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		layers = append(layers, v)
	}
	return layers
}

// TestExample compiles every instance of each example's tree (loadInstance)
// and compares what it writes with the instance's outputs, byte for byte;
// an output directory of no instance fails. 00_passthrough: Chart.yaml
// and a plain values.yaml, no instance values. 01_minimal: one chart, no
// contract. 02_advanced: dcr mode, the OAuth client's chart added.
// 03_full: the project's project.yaml at level 1 and the instance's
// connections.yaml at level 2, between platform.yaml and config.yaml; its
// chart cache, .charts/, checks the compile (--check --charts), which
// gives the same files and no other warning.
func TestExample(t *testing.T) {
	instances, _ := filepath.Glob(filepath.Join(examples, "*", "input", "db", "projects", "*", "instances", "*"))
	outputs, _ := filepath.Glob(filepath.Join(examples, "*", "output", "projects", "*", "instances", "*"))
	if len(instances) == 0 {
		t.Fatal("no example")
	}
	for _, dir := range instances {
		rel, _ := filepath.Rel(examples, dir)
		parts := strings.Split(filepath.ToSlash(rel), "/") // <example>/input/db/projects/<project>/instances/<instance>
		example, project, instance := parts[0], parts[4], parts[6]
		out := filepath.Join(example, "output", "projects", project, "instances", instance)
		outputs = slices.DeleteFunc(outputs, func(o string) bool { return o == filepath.Join(examples, out) })
		t.Run(path.Join(example, project, instance), func(t *testing.T) {
			def, in := loadInstance(t, example, project, instance)
			res, err := Compile(t.Context(), def, in)
			if err != nil {
				t.Fatal(err)
			}
			files := output(t, res)
			checkDir(t, out, files)
			charts := filepath.Join(examples, example, ".charts")
			if !exists(t, charts) {
				return
			}
			in.Charts = os.DirFS(charts)
			checked, err := Compile(t.Context(), def, in)
			if err != nil {
				t.Fatalf("checked with %s: %v", charts, err)
			}
			if !maps.EqualFunc(output(t, checked), files, bytes.Equal) || !slices.Equal(checked.Warnings, res.Warnings) {
				t.Errorf("checked with %s: other files, or warnings %q", charts, checked.Warnings)
			}
		})
	}
	for _, o := range outputs {
		t.Errorf("%s: the output of no instance", o)
	}
}

// output: the files Write gives for res.
func output(t *testing.T, res *Result) map[string][]byte {
	t.Helper()
	files, err := Write(res)
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// checkDir: examples/<dir> holds these files, path -> content, and only
// them, byte for byte, its subdirectories included; -update rewrites it.
func checkDir(t *testing.T, dir string, files map[string][]byte) {
	t.Helper()
	root := filepath.Join(examples, dir)
	if *update {
		if err := os.RemoveAll(root); err != nil {
			t.Fatal(err)
		}
		for name, b := range files {
			path := filepath.Join(root, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, b, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return
	}
	var names []string
	err := filepath.WalkDir(root, func(path string, e fs.DirEntry, err error) error {
		if err == nil && !e.IsDir() {
			rel, _ := filepath.Rel(root, path)
			names = append(names, filepath.ToSlash(rel))
		}
		return err
	})
	if err != nil {
		t.Fatalf("%v (run go test . -update)", err)
	}
	for name := range files {
		if !slices.Contains(names, name) {
			t.Errorf("examples/%s/%s: missing, run go test . -update and review the diff", dir, name)
		}
	}
	for _, name := range names {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		if want, ok := files[name]; !ok || !bytes.Equal(b, want) {
			t.Errorf("examples/%s/%s differs from the compile, run go test . -update and review the diff", dir, name)
		}
	}
}

// TestExampleErrors: on the instance of 02_advanced (loadInstance), every
// testdata/<case>.yaml, an instance file in place of its config.yaml, and
// every testdata/context/<level>/<case>.yaml, a context file of that level
// after the tree's, without config.yaml, must fail with the message of its
// first line (# error: ..., \n for a line break). A context file may set
// parameters too: those of level 2, an instance's own connections,
// reference the connections they give.
func TestExampleErrors(t *testing.T) {
	testdata := "testdata"
	values, _ := filepath.Glob(filepath.Join(testdata, "*.yaml"))
	context, _ := filepath.Glob(filepath.Join(testdata, "context", "*", "*.yaml"))
	if len(values) == 0 || len(context) == 0 {
		t.Fatal("no testdata")
	}
	for _, f := range slices.Concat(values, context) {
		name, _ := filepath.Rel(testdata, f)
		t.Run(strings.TrimSuffix(name, ".yaml"), func(t *testing.T) {
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			first, _, _ := strings.Cut(string(b), "\n")
			want, ok := strings.CutPrefix(first, "# error: ")
			if !ok {
				t.Fatalf("first line must be \"# error: <message>\", got %q", first)
			}
			want = strings.ReplaceAll(want, `\n`, "\n")
			def, in := loadInstance(t, "02_advanced", "demo", "demo-webapp")
			if level, err := strconv.ParseUint(filepath.Base(filepath.Dir(f)), 10, 0); err == nil {
				in.Context = append(in.Context, readContext(t, uint(level), f)...)
				in.Values = nil
			} else {
				in.Values = readLayers(t, []string{f})
			}
			if _, err := Compile(t.Context(), def, in); err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("got %v, want an error containing %q", err, want)
			}
		})
	}
}
