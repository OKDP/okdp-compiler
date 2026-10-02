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
// `--umbrella`).
var (
	input  = filepath.Join("..", "..", "example", "input")
	output = filepath.Join("..", "..", "example", "output")
)

// exampleCompile: the example package compiled with its chart cache and
// contracts, the platform values, then the given values files, under the
// CLI's default release and namespace.
func exampleCompile(t *testing.T, files ...string) (*okdp.Definition, *okdp.Result, error) {
	t.Helper()
	def, err := LoadDefinition(os.DirFS(filepath.Join(input, "package")))
	if err != nil {
		t.Fatal(err)
	}
	in := okdp.Inputs{
		Charts:    os.DirFS(filepath.Join(input, "charts")),
		Contracts: os.DirFS(filepath.Join(input, "contracts")),
		Release:   "demo-webapp",
		Namespace: "demo",
	}
	for _, f := range append([]string{filepath.Join(input, "platform.yaml")}, files...) {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		v, err := chart.ParseValues(b)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		in.Values = append(in.Values, v)
	}
	res, err := okdp.Compile(def, in)
	return def, res, err
}

// TestExample compiles the example instance (example/input/instance.yaml)
// and compares what it writes with example/output/, byte for byte: files/,
// what --emit-dir writes, and umbrella/, what --umbrella writes.
func TestExample(t *testing.T) {
	def, res, err := exampleCompile(t, filepath.Join(input, "instance.yaml"))
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

// TestExampleErrors: every testdata/<case>.yaml, applied over the platform
// values, must fail with the message of its first line (# error: ..., \n
// for a line break).
func TestExampleErrors(t *testing.T) {
	cases, _ := filepath.Glob(filepath.Join("..", "..", "testdata", "*.yaml"))
	if len(cases) == 0 {
		t.Fatal("no testdata")
	}
	for _, f := range cases {
		t.Run(strings.TrimSuffix(filepath.Base(f), ".yaml"), func(t *testing.T) {
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
			if _, _, err := exampleCompile(t, f); err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("got %v, want an error containing %q", err, want)
			}
		})
	}
}
