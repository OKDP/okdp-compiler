package compiler

import (
	"bytes"
	"context"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/okdp/okdp-compiler/internal/chart"
)

var update = flag.Bool("update", false, "rewrite the examples' outputs from the current compiler")

// The examples, each what a compile reads (<example>/input) and writes;
// input, that of the example the failure cases run on (examples/02_advanced:
// output-no-dcr/ as `okdp-compile -o <dir>`, output-dcr/
// likewise in dcr mode).
var (
	examples = "examples"
	input    = filepath.Join(examples, "02_advanced", "input")
)

// exampleCompile: the advanced example's package compiled with its chart cache
// and contracts, the context (platform.yaml at level 0, then these layers)
// and these instance values files, under the example's release and
// namespace.
func exampleCompile(t *testing.T, context []ContextLayer, instance []string) (*Result, error) {
	t.Helper()
	def, err := LoadDefinition(os.DirFS(filepath.Join(input, "service")))
	if err != nil {
		t.Fatal(err)
	}
	in := Inputs{
		Charts:    os.DirFS(filepath.Join(input, "charts")),
		Contracts: os.DirFS(filepath.Join(input, "contracts")),
		Context:   append(readContext(t, 0, filepath.Join(input, "platform.yaml")), context...),
		Values:    readLayers(t, instance),
		Release:   "demo-webapp",
		Namespace: "demo",
	}
	return Compile(t.Context(), def, in)
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

// TestExample compiles the instance of each example
// (input/config.yaml) and compares what it writes with the
// example's outputs, byte for byte. 01_minimal: output/, what -o <dir>
// writes; nothing references a contract. 02_advanced: output-no-dcr/,
// likewise; output-dcr/, what it writes with platform-dcr.yaml after
// platform.yaml. 03_full: output/, what -o <dir> writes, with the
// project's demo.yaml at level 1 (--context 1:demo.yaml) and the
// instance's connections.yaml at level 2 (--context 2:connections.yaml)
// between platform.yaml and config.yaml.
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
			Context:   readContext(t, 0, filepath.Join(in, "platform.yaml")),
			Values:    readLayers(t, []string{filepath.Join(in, "config.yaml")}),
			Release:   "demo-hello",
			Namespace: "demo",
		})
		if err != nil {
			t.Fatal(err)
		}
		checkDir(t, "01_minimal/output", output(t, res))
	})
	t.Run("02_advanced", func(t *testing.T) {
		instance := []string{filepath.Join(input, "config.yaml")}
		res, err := exampleCompile(t, nil, instance)
		if err != nil {
			t.Fatal(err)
		}
		checkDir(t, "02_advanced/output-no-dcr", output(t, res))
		res, err = exampleCompile(t, readContext(t, 0, filepath.Join(input, "platform-dcr.yaml")), instance)
		if err != nil {
			t.Fatal(err)
		}
		checkDir(t, "02_advanced/output-dcr", output(t, res))
	})
	t.Run("03_full", func(t *testing.T) {
		in := filepath.Join(examples, "03_full", "input")
		def, err := LoadDefinition(os.DirFS(filepath.Join(in, "service")))
		if err != nil {
			t.Fatal(err)
		}
		layers := slices.Concat(
			readContext(t, 0, filepath.Join(in, "platform.yaml")),
			readContext(t, 1, filepath.Join(in, "demo.yaml")),
			readContext(t, 2, filepath.Join(in, "connections.yaml")),
		)
		res, err := Compile(context.Background(), def, Inputs{
			Charts:    os.DirFS(filepath.Join(in, "charts")),
			Contracts: os.DirFS(filepath.Join(in, "contracts")),
			Context:   layers,
			Values:    readLayers(t, []string{filepath.Join(in, "config.yaml")}),
			Release:   "demo-workspace",
			Namespace: "demo",
		})
		if err != nil {
			t.Fatal(err)
		}
		checkDir(t, "03_full/output", output(t, res))
	})
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
// context, and every testdata/context/<level>/<case>.yaml, a context file of
// that level after platform.yaml, must fail with the message of its first
// line (# error: ..., \n for a line break). A context file may set
// parameters too: those of level 2, an instance's own connections,
// reference the connections they give.
func TestExampleErrors(t *testing.T) {
	testdata := "testdata"
	instance, _ := filepath.Glob(filepath.Join(testdata, "*.yaml"))
	context, _ := filepath.Glob(filepath.Join(testdata, "context", "*", "*.yaml"))
	if len(instance) == 0 || len(context) == 0 {
		t.Fatal("no testdata")
	}
	for _, f := range slices.Concat(instance, context) {
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
			var layers []ContextLayer
			var values []string
			if level, err := strconv.ParseUint(filepath.Base(filepath.Dir(f)), 10, 0); err == nil {
				layers = readContext(t, uint(level), f)
			} else {
				values = []string{f}
			}
			if _, err := exampleCompile(t, layers, values); err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("got %v, want an error containing %q", err, want)
			}
		})
	}
}
