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
		platform:  multi{filepath.Join(input, "platform.yaml")},
		values:    multi{filepath.Join(input, "config.yaml")},
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

// TestParse: an argument that is not a flag is refused, where it would end
// the flags and leave those after it unread.
func TestParse(t *testing.T) {
	for args, want := range map[string]string{
		"-f a.yaml -f b.yaml -o out":       "",
		"-f a.yaml -f b.yaml --output out": "",
		"-f a.yaml b.yaml -o out":          `unexpected argument "b.yaml": every input is a flag`,
		"-o":                               "flag needs an argument: -o",
	} {
		var stderr bytes.Buffer
		o, err := parse(strings.Fields(args), &stderr)
		switch {
		case want == "" && (err != nil || o.output != "out" || !slices.Equal(o.values, multi{"a.yaml", "b.yaml"})):
			t.Errorf("%s: %v %#v", args, err, o)
		case want != "" && (err == nil || !strings.Contains(stderr.String(), want) || !strings.Contains(stderr.String(), "Usage of okdp-compile")):
			t.Errorf("%s: got %v, want %q and the usage on stderr, got %q", args, err, want, stderr.String())
		}
	}
}

// TestRun: the example compiled into directories, one per output, the
// first holding the files of a previous compile: they hold what the
// example's outputs hold, a file no longer written removed
// (okdp.dependencies.yaml, without --mkdag). A compile with --mkdag writes
// okdp.dependencies.yaml.
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
			o.platform = append(o.platform, filepath.Join(input, "platform-dcr.yaml"))
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
	o := example(noDcr)
	o.mkdag = true
	if err := run(o); err != nil {
		t.Fatal(err)
	}
	const deps = "- instance: demo-db\n  contract: database\n  field: database\n"
	if b, err := os.ReadFile(filepath.Join(noDcr, "okdp.dependencies.yaml")); err != nil || string(b) != deps {
		t.Errorf("--mkdag: got %v %q, want %q", err, b, deps)
	}
}

// TestRunErrors: what run refuses, before it writes anything.
func TestRunErrors(t *testing.T) {
	// A package whose version an umbrella chart cannot take: it compiles,
	// its umbrella fails.
	pkg := t.TempDir()
	if err := os.CopyFS(pkg, os.DirFS(filepath.Join(input, "service"))); err != nil {
		t.Fatal(err)
	}
	svc, err := os.ReadFile(filepath.Join(pkg, "service.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	svc = bytes.Replace(svc, []byte("version: 1.0.0"), []byte(`version: "1.0"`), 1)
	if err := os.WriteFile(filepath.Join(pkg, "service.yaml"), svc, 0o644); err != nil {
		t.Fatal(err)
	}
	protected := filepath.Join("..", "..", "testdata", "protected-upstream.yaml")
	for name, tc := range map[string]struct {
		set   func(o *options)
		files []string // created first under the output directory; a directory when ending with /
		want  string
	}{
		"no release":            {func(o *options) { o.release = "" }, nil, "--release is required"},
		"no namespace":          {func(o *options) { o.namespace = "" }, nil, "--namespace is required"},
		"no output":             {func(o *options) { o.output = "" }, nil, "--output (-o) is required"},
		"a compile":             {func(o *options) { o.values = append(o.values, protected) }, nil, "ingress.hosts is set by the platform"},
		"an umbrella":           {func(o *options) { o.pkg = pkg }, nil, `service.yaml: version "1.0": the version of the umbrella chart`},
		"another file":          {nil, []string{"notes.txt"}, "notes.txt is not an output of okdp-compile"},
		"a chart's values file": {nil, []string{"app.values.yaml"}, "app.values.yaml is not an output of okdp-compile"},
		"an install script":     {nil, []string{"install.sh"}, "install.sh is not an output of okdp-compile"},
		"a directory":           {nil, []string{"instance.values.yaml/"}, "instance.values.yaml is not an output of okdp-compile"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			o := example(dir)
			if tc.set != nil {
				tc.set(&o)
			}
			for _, f := range tc.files {
				p := filepath.Join(dir, f)
				if strings.HasSuffix(f, "/") {
					if err := os.MkdirAll(p, 0o755); err != nil {
						t.Fatal(err)
					}
					continue
				}
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, nil, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			before := tree(t, dir)
			if err := run(o); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
			if after := tree(t, dir); !maps.Equal(after, before) {
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
