package compiler

import (
	"bytes"
	"context"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/okdp/okdp-compiler/internal/chart"
)

var update = flag.Bool("update", false, "rewrite the examples' outputs and testdata/helm/umbrella/ from the current compiler")

// The examples, each what a compile reads (<example>/input) and writes;
// input, that of the example the failure cases run on (examples/02_advanced:
// output-no-dcr/ as `okdp-compile -o <dir>`, output-dcr/
// likewise in dcr mode).
var (
	examples = "examples"
	input    = filepath.Join(examples, "02_advanced", "input")
)

// exampleCompile: the advanced example's package compiled with its chart cache
// and contracts, the platform values (platform.yaml, then
// these platform files), these project values files and these instance
// values files, under the example's release and namespace.
func exampleCompile(t *testing.T, platform, project, instance []string) (*Result, error) {
	t.Helper()
	def, err := LoadDefinition(os.DirFS(filepath.Join(input, "service")))
	if err != nil {
		t.Fatal(err)
	}
	in := Inputs{
		Charts:    os.DirFS(filepath.Join(input, "charts")),
		Contracts: os.DirFS(filepath.Join(input, "contracts")),
		Platform:  readLayers(t, append([]string{filepath.Join(input, "platform.yaml")}, platform...)),
		Project:   readLayers(t, project),
		Values:    readLayers(t, instance),
		Release:   "demo-webapp",
		Namespace: "demo",
	}
	return Compile(context.Background(), def, in)
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

// TestExample compiles the instance of each example
// (input/config.yaml) and compares what it writes with the
// example's outputs, byte for byte. 01_minimal: output/, what -o <dir>
// writes; nothing references a contract. 02_advanced: output-no-dcr/,
// likewise; output-dcr/, what it writes with platform-dcr.yaml after
// platform.yaml. 03_full: output/, what -o <dir> --mkdag writes, with the
// project's demo.yaml (--project) between platform.yaml and config.yaml.
func TestExample(t *testing.T) {
	t.Run("01_minimal", func(t *testing.T) {
		in := filepath.Join(examples, "01_minimal", "input")
		def, err := LoadDefinition(os.DirFS(filepath.Join(in, "service")))
		if err != nil {
			t.Fatal(err)
		}
		res, err := Compile(context.Background(), def, Inputs{
			Charts:    os.DirFS(filepath.Join(in, "charts")),
			Contracts: fstest.MapFS{},
			Platform:  readLayers(t, []string{filepath.Join(in, "platform.yaml")}),
			Values:    readLayers(t, []string{filepath.Join(in, "config.yaml")}),
			Release:   "demo-hello",
			Namespace: "demo",
		})
		if err != nil {
			t.Fatal(err)
		}
		checkDir(t, "01_minimal/output", output(t, res, Output{}))
	})
	t.Run("02_advanced", func(t *testing.T) {
		instance := []string{filepath.Join(input, "config.yaml")}
		res, err := exampleCompile(t, nil, nil, instance)
		if err != nil {
			t.Fatal(err)
		}
		checkDir(t, "02_advanced/output-no-dcr", output(t, res, Output{}))
		res, err = exampleCompile(t, []string{filepath.Join(input, "platform-dcr.yaml")}, nil, instance)
		if err != nil {
			t.Fatal(err)
		}
		checkDir(t, "02_advanced/output-dcr", output(t, res, Output{}))
	})
	t.Run("03_full", func(t *testing.T) {
		in := filepath.Join(examples, "03_full", "input")
		def, err := LoadDefinition(os.DirFS(filepath.Join(in, "service")))
		if err != nil {
			t.Fatal(err)
		}
		res, err := Compile(context.Background(), def, Inputs{
			Charts:    os.DirFS(filepath.Join(in, "charts")),
			Contracts: os.DirFS(filepath.Join(in, "contracts")),
			Platform:  readLayers(t, []string{filepath.Join(in, "platform.yaml")}),
			Project:   readLayers(t, []string{filepath.Join(in, "demo.yaml")}),
			Values:    readLayers(t, []string{filepath.Join(in, "config.yaml")}),
			Release:   "demo-workspace",
			Namespace: "demo",
		})
		if err != nil {
			t.Fatal(err)
		}
		checkDir(t, "03_full/output", output(t, res, Output{Dependencies: true}))
	})
}

// output: the files Write gives for res with o, each one IsOutputFile
// names: a directory okdp-compile wrote takes the files of the next
// compile.
func output(t *testing.T, res *Result, o Output) map[string][]byte {
	t.Helper()
	files, err := Write(res, o)
	if err != nil {
		t.Fatal(err)
	}
	for name := range files {
		if !IsOutputFile(name) {
			t.Errorf("%s: not an output file (IsOutputFile)", name)
		}
	}
	return files
}

// checkDir: examples/<dir> holds these files, and only them, byte for
// byte; -update rewrites it.
func checkDir(t *testing.T, dir string, files map[string][]byte) {
	t.Helper()
	path := filepath.Join(examples, dir)
	if *update {
		if err := os.RemoveAll(path); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		for name, b := range files {
			if err := os.WriteFile(filepath.Join(path, name), b, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatalf("%v (run go test . -update)", err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	for name := range files {
		if !slices.Contains(names, name) {
			t.Errorf("examples/%s/%s: missing, run go test . -update and review the diff", dir, name)
		}
	}
	for _, name := range names {
		b, err := os.ReadFile(filepath.Join(path, name))
		if err != nil {
			t.Fatal(err)
		}
		if want, ok := files[name]; !ok || !bytes.Equal(b, want) {
			t.Errorf("examples/%s/%s differs from the compile, run go test . -update and review the diff", dir, name)
		}
	}
}

// TestExampleErrors: every testdata/<case>.yaml, an instance file over the
// platform values, every testdata/platform/<case>.yaml, a platform file
// after platform.yaml, and every testdata/project/<case>.yaml, a project
// file after it, must fail with the message of its first line (# error:
// ..., \n for a line break).
func TestExampleErrors(t *testing.T) {
	testdata := "testdata"
	instance, _ := filepath.Glob(filepath.Join(testdata, "*.yaml"))
	platform, _ := filepath.Glob(filepath.Join(testdata, "platform", "*.yaml"))
	project, _ := filepath.Glob(filepath.Join(testdata, "project", "*.yaml"))
	if len(instance) == 0 || len(platform) == 0 || len(project) == 0 {
		t.Fatal("no testdata")
	}
	for _, f := range slices.Concat(instance, platform, project) {
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
			var files [3][]string // platform, project, instance
			switch {
			case slices.Contains(platform, f):
				files[0] = []string{f}
			case slices.Contains(project, f):
				files[1] = []string{f}
			default:
				files[2] = []string{f}
			}
			if _, err := exampleCompile(t, files[0], files[1], files[2]); err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("got %v, want an error containing %q", err, want)
			}
		})
	}
}
