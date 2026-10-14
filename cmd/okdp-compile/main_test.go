package main

import (
	"bytes"
	"errors"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/rogpeppe/go-internal/testscript"
)

// The example: what a compile reads (examples/02_advanced/input) and
// writes (the rest of examples/02_advanced).
var (
	exampleDir = filepath.Join("..", "..", "examples", "02_advanced")
	input      = filepath.Join(exampleDir, "input")
)

// TestMain: the test binary is also okdp-compile, the command of the
// scripts (TestScript).
func TestMain(m *testing.M) {
	testscript.Main(m, map[string]func(){"okdp-compile": main})
}

// example: the options that compile the example instance into dir, as its
// output-no-dcr/ (-o dir).
func example(dir string) options {
	return options{
		pkg:       filepath.Join(input, "service"),
		charts:    filepath.Join(input, "charts"),
		contracts: filepath.Join(input, "contracts"),
		contexts:  []string{filepath.Join(input, "platform.yaml")},
		values:    []string{filepath.Join(input, "config.yaml")},
		release:   "demo-webapp",
		namespace: "demo",
		output:    dir,
	}
}

// tree: the files under dir, path -> content.
func tree(t *testing.T, dir string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return err
		}
		b, err := os.ReadFile(path)
		rel, _ := filepath.Rel(dir, path)
		files[rel] = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// TestRun: the example compiled into directories, one per output, the
// first holding the files of a previous compile: they hold what the
// example's outputs hold, the files of the previous compile overwritten.
func TestRun(t *testing.T) {
	dir := t.TempDir()
	noDcr := filepath.Join(dir, "output-no-dcr")
	if err := os.MkdirAll(noDcr, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"instance.values.yaml", "okdp.dependencies.yaml"} {
		if err := os.WriteFile(filepath.Join(noDcr, name), []byte("stale: true\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		output string
		dcr    bool
	}{
		{"output-no-dcr", false},
		{"output-dcr", true},
	} {
		o := example(filepath.Join(dir, c.output))
		if c.dcr {
			o.contexts = append(o.contexts, filepath.Join(input, "platform-dcr.yaml"))
		}
		if err := run(o); err != nil {
			t.Fatalf("%s: %v", c.output, err)
		}
	}
	want := tree(t, exampleDir)
	maps.DeleteFunc(want, func(path string, _ string) bool { return strings.HasPrefix(path, "input"+string(filepath.Separator)) })
	if got := tree(t, dir); !maps.Equal(got, want) {
		t.Errorf("got %v, want the example's outputs: %v", slices.Sorted(maps.Keys(got)), slices.Sorted(maps.Keys(want)))
	}
}

// TestRunErrors: what run refuses, before it writes anything.
func TestRunErrors(t *testing.T) {
	// A package whose version an umbrella chart cannot take: it fails to
	// load.
	pkg := t.TempDir()
	if err := os.CopyFS(pkg, os.DirFS(filepath.Join(input, "service"))); err != nil {
		t.Fatal(err)
	}
	meta, err := os.ReadFile(filepath.Join(pkg, "Chart.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	meta = bytes.Replace(meta, []byte("version: 1.0.0"), []byte(`version: "1.0"`), 1)
	if err := os.WriteFile(filepath.Join(pkg, "Chart.yaml"), meta, 0o644); err != nil {
		t.Fatal(err)
	}
	protected := filepath.Join("..", "..", "testdata", "protected-upstream.yaml")
	redefines := filepath.Join("..", "..", "testdata", "context", "1", "redefines-platform-connection.yaml")
	for name, tc := range map[string]struct {
		set  func(o *options)
		want string
	}{
		"no release":   {func(o *options) { o.release = "" }, "--release is required"},
		"no namespace": {func(o *options) { o.namespace = "" }, "--namespace is required"},
		"no output":    {func(o *options) { o.output = "" }, "--output (-o) is required"},
		"a compile":    {func(o *options) { o.values = append(o.values, protected) }, "ingress.hosts is set by the platform"},
		"a package":    {func(o *options) { o.pkg = pkg }, `Chart.yaml: version "1.0": SemVer (1.2.3), the version of the umbrella chart`},
		"a level":      {func(o *options) { o.contexts = append(o.contexts, "1:"+redefines) }, "context layer 2 (level 1): connections.platform-db is set at level 0"},
		"no level":     {func(o *options) { o.contexts = append(o.contexts, "dev:x.yaml") }, "open dev:x.yaml: no such file or directory"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			o := example(dir)
			tc.set(&o)
			if err := run(o); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
			if after := tree(t, dir); len(after) > 0 {
				t.Errorf("written: %v", slices.Sorted(maps.Keys(after)))
			}
		})
	}
}

// TestScript runs the scripts of testdata/cli: okdp-compile as a
// process, its exit status, stdout and stderr, $OKDP_CHARTS and
// $OKDP_CONTRACTS. A script runs from the repository root, as the commands
// of the documentation, with no $OKDP_* variable but those it sets; its
// files and outputs are under $WORK.
func TestScript(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	testscript.Run(t, testscript.Params{
		Dir:                 filepath.Join(root, "testdata", "cli"),
		Setup:               func(e *testscript.Env) error { e.Cd = root; return nil },
		Cmds:                map[string]func(*testscript.TestScript, bool, []string){"status": status},
		RequireExplicitExec: true,
	})
}

// status <n> <command> [args...]: runs the command, which must exit with
// status n; the next stdout and stderr commands read its output.
func status(ts *testscript.TestScript, neg bool, args []string) {
	if neg || len(args) < 2 {
		ts.Fatalf("usage: status <n> <command> [args...]")
	}
	want, err := strconv.Atoi(args[0])
	ts.Check(err)
	got := 0
	if err := ts.Exec(args[1], args[2:]...); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			ts.Fatalf("%s: %v", args[1], err)
		}
		got = exit.ExitCode()
	}
	if got != want {
		ts.Fatalf("exit status %d, want %d", got, want)
	}
}
