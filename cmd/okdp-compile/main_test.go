package main

import (
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/okdp/okdp-compiler/internal/outdir"
)

// files: the files under dir, path -> content.
func files(t *testing.T, dir string) map[string]string {
	t.Helper()
	got := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		rel, _ := filepath.Rel(dir, p)
		got[filepath.ToSlash(rel)] = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// write: the files of want under dir.
func write(t *testing.T, dir string, want map[string]string) {
	t.Helper()
	for name, content := range want {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// example: the example of one package copied into a temporary directory,
// its input documents (config/) and its catalog (the directory itself),
// and what it compiles to, under its instance's directory; the instance
// named after the package, in the project demo.
func example(t *testing.T, name, service string) (input string, want map[string]string) {
	t.Helper()
	input = t.TempDir()
	write(t, input, files(t, filepath.Join("..", "..", "examples", name, "input")))
	want = map[string]string{}
	for p, c := range files(t, filepath.Join("..", "..", "examples", name, "output")) {
		want["projects/demo/instances/demo-"+service+"/"+p] = c
	}
	return input, want
}

// opts: the options of a compile of the example copied in input.
func opts(input string) options {
	return options{files: []string{filepath.Join(input, "config")}, catalogs: []string{input}}
}

// TestRun: the inputs of an example compiled into a directory the command
// wrote before, holding a stale instance: it holds the example's output
// and the marker, the stale instance removed.
func TestRun(t *testing.T) {
	for _, tc := range []struct{ example, service string }{
		{"00_passthrough", "echo"},
		{"01_minimal", "hello"},
	} {
		t.Run(tc.example, func(t *testing.T) {
			input, want := example(t, tc.example, tc.service)
			out := t.TempDir()
			write(t, out, map[string]string{outdir.MarkerFile: outdir.Marker, "projects/demo/instances/demo-gone/values.yaml": "{}\n"})
			o := opts(input)
			o.output, o.costLimit = out, defaultCostLimit
			if err := run(o); err != nil {
				t.Fatal(err)
			}
			want[outdir.MarkerFile] = outdir.Marker
			if got := files(t, out); !maps.Equal(got, want) {
				t.Errorf("wrote\n%v\nwant\n%v", got, want)
			}
		})
	}
}

// TestRunWithRules: --rules adds a set to the compiler's mandatory one; a
// set that gives a rule the mandatory one gives is refused.
func TestRunWithRules(t *testing.T) {
	input, _ := example(t, "00_passthrough", "echo")
	added := t.TempDir()
	write(t, added, map[string]string{"rules.yaml": "apiVersion: okdp-compiler/rules.v1\nname: added\nversion: 1.0.0\n"})
	out := t.TempDir()
	o := opts(input)
	o.rules, o.output = added, out
	if err := run(o); err != nil {
		t.Fatal(err)
	}
	if _, ok := files(t, out)["projects/demo/instances/demo-echo/values.yaml"]; !ok {
		t.Errorf("no values.yaml: %v", files(t, out))
	}
	write(t, added, map[string]string{"rules.yaml": "apiVersion: okdp-compiler/rules.v1\nname: added\nversion: 1.0.0\nhooks:\n  namespace: [namespace]\n"})
	o = opts(input)
	o.rules, o.check = added, true
	if err := run(o); err == nil || !strings.Contains(err.Error(), "replaces none") {
		t.Errorf("a set that lists a mandatory hook document again: %v", err)
	}
}

// TestRunRefused: an instance refused, the command fails and writes
// nothing; --check reports the same.
func TestRunRefused(t *testing.T) {
	input, _ := example(t, "01_minimal", "hello")
	write(t, input, map[string]string{"config/config.yaml": "replicas: many\n"})
	out := filepath.Join(t.TempDir(), "out")
	o := opts(input)
	o.output = out
	if err := run(o); !errors.Is(err, errReported) {
		t.Errorf("run: %v", err)
	}
	if _, err := os.Stat(out); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("output written: %v", err)
	}
	o = opts(input)
	o.check = true
	if err := run(o); !errors.Is(err, errReported) {
		t.Errorf("run --check: %v", err)
	}
}

// TestCheck: --check compiles and writes nothing.
func TestCheck(t *testing.T) {
	input, _ := example(t, "01_minimal", "hello")
	before := files(t, input)
	o := opts(input)
	o.check = true
	if err := run(o); err != nil {
		t.Fatal(err)
	}
	if got := files(t, input); !maps.Equal(got, before) {
		t.Errorf("--check changed the inputs: %v", got)
	}
}

// TestFileLink: a file of -f that is a symbolic link is read where it
// points, its valuesFiles beside that file; a symbolic link under a
// directory read is refused.
func TestFileLink(t *testing.T) {
	input, _ := example(t, "01_minimal", "hello")
	link := filepath.Join(t.TempDir(), "inputs.yaml")
	if err := os.Symlink(filepath.Join(input, "config", "inputs.yaml"), link); err != nil {
		t.Fatal(err)
	}
	o := options{files: []string{link}, catalogs: []string{input}, check: true}
	if err := run(o); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(link, filepath.Join(input, "config", "linked.yaml")); err != nil {
		t.Fatal(err)
	}
	o = opts(input)
	o.check = true
	if err := run(o); err == nil || !strings.Contains(err.Error(), "linked.yaml: a symbolic link, not followed") {
		t.Errorf("a link in -f's directory: %v", err)
	}
}

// TestRefusedOptions: what the command refuses before it reads its
// inputs.
func TestRefusedOptions(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in")
	write(t, in, map[string]string{"inputs.yaml": "kind: Settings\n"})
	cat := filepath.Join(dir, "charts")
	write(t, cat, map[string]string{"catalog/.keep": ""})
	notMarked := filepath.Join(dir, "other")
	write(t, notMarked, map[string]string{"projects/keep.yaml": "{}\n"})
	f, c := []string{in}, []string{cat}
	for _, tc := range []struct {
		name string
		o    options
		want string
	}{
		{"no file", options{catalogs: c, output: dir}, "-f (--file) is required"},
		{"no catalog", options{files: f, output: dir}, "--catalog is required"},
		{"no output", options{files: f, catalogs: c}, "--output (-o) is required"},
		{"check with output", options{files: f, catalogs: c, check: true, output: dir}, "--check writes nothing"},
		{"check with force", options{files: f, catalogs: c, check: true, force: true}, "--check writes nothing"},
		{"file twice", options{files: []string{in, in}, catalogs: c, check: true}, "is given twice"},
		{"catalog a file", options{files: f, catalogs: []string{filepath.Join(in, "inputs.yaml")}, check: true}, "not a directory"},
		{"output in inputs", options{files: f, catalogs: c, output: filepath.Join(in, "out")}, "must not contain one another"},
		{"output in rules", options{files: f, catalogs: c, rules: dir, output: filepath.Join(dir, "out")}, "must not contain one another"},
		{"output not ours", options{files: f, catalogs: c, output: notMarked}, "was not written by okdp-compile"},
		{"rules missing", options{files: f, catalogs: c, rules: filepath.Join(dir, "none"), check: true}, "--rules"},
		{"no instance", options{files: f, catalogs: c, check: true}, "no instance"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := run(tc.o)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %v, want %q", err, tc.want)
			}
		})
	}
}
