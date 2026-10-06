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

var update = flag.Bool("update", false, "rewrite the example's outputs and testdata/helm/umbrella/ from the current compiler")

// The example: what a compile reads (examples/02_advanced/input) and writes
// (examples/02_advanced: output-no-dcr/helm-cmd/ as `okdp-compile
// --emit-dir --mkinstall`, output-no-dcr/umbrella-chart/ as `--umbrella`,
// output-dcr/helm-cmd/ as `--emit-dir --mkinstall` in dcr mode).
var (
	example = filepath.Join("..", "..", "examples", "02_advanced")
	input   = filepath.Join(example, "input")
)

// exampleCompile: the example package compiled with its chart cache,
// instance files and contracts, the platform values (platform.yaml, then
// these platform files) and these instance values files, under the
// example's release and namespace.
func exampleCompile(t *testing.T, platform, instance []string) (*okdp.Definition, *okdp.Result, error) {
	t.Helper()
	def, err := LoadDefinition(os.DirFS(filepath.Join(input, "service")))
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

// TestExample compiles the example instance (input/instance/: its
// values.yaml and its files) and compares what it writes with the example's
// outputs, byte for byte: output-no-dcr/helm-cmd/, what --emit-dir
// --mkinstall writes, and output-no-dcr/umbrella-chart/, what --umbrella
// writes; output-dcr/helm-cmd/, what --emit-dir --mkinstall writes with
// platform-dcr.yaml after platform.yaml.
func TestExample(t *testing.T) {
	instance := []string{filepath.Join(input, "instance", "values.yaml")}
	def, res, err := exampleCompile(t, nil, instance)
	if err != nil {
		t.Fatal(err)
	}
	umbrella, err := Umbrella(def, res, DefaultIndent)
	if err != nil {
		t.Fatal(err)
	}
	checkDir(t, "output-no-dcr/helm-cmd", helmCmd(t, res))
	checkDir(t, "output-no-dcr/umbrella-chart", umbrella)
	_, res, err = exampleCompile(t, []string{filepath.Join(input, "platform-dcr.yaml")}, instance)
	if err != nil {
		t.Fatal(err)
	}
	checkDir(t, "output-dcr/helm-cmd", helmCmd(t, res))
}

// helmCmd: the files of res and its InstallScript, what --emit-dir
// --mkinstall writes.
func helmCmd(t *testing.T, res *okdp.Result) map[string][]byte {
	t.Helper()
	files, err := Files(res, DefaultIndent)
	if err != nil {
		t.Fatal(err)
	}
	if files[InstallScript], err = Install(res); err != nil {
		t.Fatal(err)
	}
	return files
}

// checkDir: <dir> of the example holds these files, and only them, byte for
// byte, InstallScript executable; -update rewrites it.
func checkDir(t *testing.T, dir string, files map[string][]byte) {
	t.Helper()
	path := filepath.Join(example, dir)
	if *update {
		if err := os.RemoveAll(path); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		for name, b := range files {
			perm := os.FileMode(0o644)
			if name == InstallScript {
				perm = 0o755
			}
			if err := os.WriteFile(filepath.Join(path, name), b, perm); err != nil {
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
		if info, err := e.Info(); err == nil && e.Name() == InstallScript && info.Mode()&0o111 == 0 {
			t.Errorf("examples/02_advanced/%s/%s: not executable", dir, e.Name())
		}
	}
	for name := range files {
		if !slices.Contains(names, name) {
			t.Errorf("examples/02_advanced/%s/%s: missing, run go test ./internal/fsio -update and review the diff", dir, name)
		}
	}
	for _, name := range names {
		b, err := os.ReadFile(filepath.Join(path, name))
		if err != nil {
			t.Fatal(err)
		}
		if want, ok := files[name]; !ok || !bytes.Equal(b, want) {
			t.Errorf("examples/02_advanced/%s/%s differs from the compile, run go test ./internal/fsio -update and review the diff", dir, name)
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
