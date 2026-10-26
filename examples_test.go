package compiler

import (
	"bytes"
	"errors"
	"flag"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/okdp/okdp-compiler/internal/chart"
)

var update = flag.Bool("update", false, "rewrite the examples' outputs from the current compiler")

// examples: each example a repository tree (docs/01_usage.md, repository
// layout): its sources under input/db/, what the helm target writes for
// its instances in output/, and, for --check --charts, a chart cache in
// .charts/.
const examples = "examples"

// load: every instance of an example's tree, its input/db/ one
// repository (LoadRepository).
func load(t *testing.T, example string) []Instance {
	t.Helper()
	db := filepath.Join(examples, example, "input", "db")
	instances, err := LoadRepository([]Repository{{Name: db, FS: os.DirFS(db)}})
	if err != nil {
		t.Fatal(err)
	}
	return instances
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

// TestExample compiles the instances of each example's tree together
// (load, CompileAll), checks their graph (NewGraph) and compares what the
// helm target writes with its output/, byte for byte; a file of output/
// that it does not write fails. 00_passthrough: Chart.yaml and a plain
// values.yaml, no instance values. 01_minimal: one chart, no contract.
// 02_advanced: dcr mode, the OAuth client's chart added; the database it
// references. 03_full: the project's project.yaml at level 1 and the
// instance's connections.yaml at level 2, between platform.yaml and
// config.yaml; the instances it references; its chart cache, .charts/,
// checks the compiles (--check --charts), which give the same files and
// no other warning.
func TestExample(t *testing.T) {
	dbs, _ := filepath.Glob(filepath.Join(examples, "*", "input", "db"))
	if len(dbs) == 0 {
		t.Fatal("no example")
	}
	for _, db := range dbs {
		example := filepath.Base(filepath.Dir(filepath.Dir(db)))
		t.Run(example, func(t *testing.T) {
			instances := load(t, example)
			files, warnings := compileTree(t, instances)
			checkDir(t, filepath.Join(example, "output"), files)
			charts := filepath.Join(examples, example, ".charts")
			if !exists(t, charts) {
				return
			}
			for i := range instances {
				instances[i].Inputs.Charts = os.DirFS(charts)
			}
			checked, checkedWarnings := compileTree(t, instances)
			if !maps.EqualFunc(checked, files, bytes.Equal) || !slices.Equal(checkedWarnings, warnings) {
				t.Errorf("checked with %s: other files, or warnings %q", charts, checkedWarnings)
			}
		})
	}
}

// compileTree: what the helm target writes for instances compiled
// together, their graph checked, and their warnings, in their order.
func compileTree(t *testing.T, instances []Instance) (map[string][]byte, []string) {
	t.Helper()
	var results []*Result
	var warnings []string
	for i, o := range CompileAll(t.Context(), instances) {
		if o.Err != nil {
			t.Fatalf("%s: %v", instances[i].Inputs.Release, o.Err)
		}
		results = append(results, o.Result)
		warnings = append(warnings, o.Result.Warnings...)
	}
	g, err := NewGraph(results)
	if err != nil {
		t.Fatal(err)
	}
	files, err := Helm{}.Files(results, g)
	if err != nil {
		t.Fatal(err)
	}
	return files, warnings
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

// TestExampleErrors: on the instance demo-webapp of 02_advanced (load), every
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
			var webapp Instance
			for _, inst := range load(t, "02_advanced") {
				if inst.Inputs.Release == "demo-webapp" {
					webapp = inst
				}
			}
			in := webapp.Inputs
			if level, err := strconv.ParseUint(filepath.Base(filepath.Dir(f)), 10, 0); err == nil {
				for _, l := range readLayers(t, []string{f}) {
					in.Context = append(in.Context, ContextLayer{Level: uint(level), Values: l})
				}
				in.Values = nil
			} else {
				in.Values = readLayers(t, []string{f})
			}
			if _, err := Compile(t.Context(), webapp.Definition, in); err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("got %v, want an error containing %q", err, want)
			}
		})
	}
}
