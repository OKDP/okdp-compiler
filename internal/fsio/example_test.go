package fsio

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/okdp/okdp-compiler/internal/chart"
	"github.com/okdp/okdp-compiler/internal/okdp"
)

var update = flag.Bool("update", false, "rewrite example/output/ and testdata/helm/umbrella/ from the current compiler")

// The example: what a compile reads (example/input) and writes
// (example/output: files/ as `okdp-compile --emit-dir`, umbrella/ as
// `--umbrella`, dcr/ as `--emit-dir` in dcr mode).
var (
	input  = filepath.Join("..", "..", "example", "input")
	output = filepath.Join("..", "..", "example", "output")
)

// exampleCompile: the example package compiled with its chart cache,
// instance files and contracts, the platform values (platform.yaml, then
// these platform files) and these instance values files, under the
// example's release and namespace.
func exampleCompile(t *testing.T, platform, instance []string) (*okdp.Definition, *okdp.Result, error) {
	t.Helper()
	def, err := LoadDefinition(os.DirFS(filepath.Join(input, "package")))
	if err != nil {
		t.Fatal(err)
	}
	in := okdp.Inputs{
		Charts:    os.DirFS(filepath.Join(input, "charts")),
		Instance:  os.DirFS(filepath.Join(input, "instance")),
		Contracts: os.DirFS(filepath.Join(input, "contracts")),
		Platform:  readLayers(t, append([]string{filepath.Join(input, "platform.yaml")}, platform...)),
		Values:    readLayers(t, instance),
		Release:   "demo-webapp",
		Namespace: "demo",
	}
	res, err := okdp.Compile(def, in)
	return def, res, err
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

// TestExample compiles the example instance (example/input/instance/: its
// values.yaml and its files) and compares what it writes with
// example/output/, byte for byte: files/, what --emit-dir writes, and
// umbrella/, what --umbrella writes; dcr/, what --emit-dir writes with
// platform-dcr.yaml after platform.yaml.
func TestExample(t *testing.T) {
	instance := []string{filepath.Join(input, "instance", "values.yaml")}
	def, res, err := exampleCompile(t, nil, instance)
	if err != nil {
		t.Fatal(err)
	}
	files, err := Files(res, DefaultIndent)
	if err != nil {
		t.Fatal(err)
	}
	umbrella, err := Umbrella(def, res, DefaultIndent)
	if err != nil {
		t.Fatal(err)
	}
	checkDir(t, "files", files)
	checkDir(t, "umbrella", umbrella)
	_, res, err = exampleCompile(t, []string{filepath.Join(input, "platform-dcr.yaml")}, instance)
	if err != nil {
		t.Fatal(err)
	}
	if files, err = Files(res, DefaultIndent); err != nil {
		t.Fatal(err)
	}
	checkDir(t, "dcr", files)
}

// checkDir: example/output/<dir> holds these files, and only them, byte for
// byte; -update rewrites it.
func checkDir(t *testing.T, dir string, files map[string][]byte) {
	t.Helper()
	path := filepath.Join(output, dir)
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
		t.Fatalf("%v (run go test ./internal/fsio -update)", err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	for name := range files {
		if !slices.Contains(names, name) {
			t.Errorf("example/output/%s/%s: missing, run go test ./internal/fsio -update and review the diff", dir, name)
		}
	}
	for _, name := range names {
		b, err := os.ReadFile(filepath.Join(path, name))
		if err != nil {
			t.Fatal(err)
		}
		if want, ok := files[name]; !ok || !bytes.Equal(b, want) {
			t.Errorf("example/output/%s/%s differs from the compile, run go test ./internal/fsio -update and review the diff", dir, name)
		}
	}
}

// TestExampleErrors: every testdata/<case>.yaml, an instance file over the
// platform values, and every testdata/platform/<case>.yaml, a platform file
// after platform.yaml, must fail with the message of its first line
// (# error: ..., \n for a line break).
func TestExampleErrors(t *testing.T) {
	testdata := filepath.Join("..", "..", "testdata")
	instance, _ := filepath.Glob(filepath.Join(testdata, "*.yaml"))
	platform, _ := filepath.Glob(filepath.Join(testdata, "platform", "*.yaml"))
	if len(instance) == 0 || len(platform) == 0 {
		t.Fatal("no testdata")
	}
	for _, f := range append(instance, platform...) {
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
			files := [2][]string{nil, {f}} // platform, instance
			if slices.Contains(platform, f) {
				files[0], files[1] = files[1], files[0]
			}
			if _, _, err := exampleCompile(t, files[0], files[1]); err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("got %v, want an error containing %q", err, want)
			}
		})
	}
}
