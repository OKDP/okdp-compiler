package main

import (
	"bytes"
	"errors"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/rogpeppe/go-internal/testscript"
)

// The example: what a compile reads (example/input) and writes
// (example/output).
var (
	input  = filepath.Join("..", "..", "example", "input")
	output = filepath.Join("..", "..", "example", "output")
)

// TestMain: the test binary is also okdp-compile, the command of the
// scripts (TestScript).
func TestMain(m *testing.M) {
	testscript.Main(m, map[string]func(){"okdp-compile": main})
}

// example: the options that compile the example instance into dir: files/
// (--emit-dir), umbrella/ (--umbrella) and out.yaml (--out).
func example(dir string) options {
	return options{
		pkg:       filepath.Join(input, "package"),
		charts:    filepath.Join(input, "charts"),
		contracts: filepath.Join(input, "contracts"),
		instance:  filepath.Join(input, "instance"),
		platform:  multi{filepath.Join(input, "platform.yaml")},
		values:    multi{filepath.Join(input, "instance.yaml")},
		release:   "demo-webapp",
		namespace: "demo",
		indent:    2,
		emitDir:   filepath.Join(dir, "files"),
		umbrella:  filepath.Join(dir, "umbrella"),
		out:       filepath.Join(dir, "out.yaml"),
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
		"-f a.yaml -f b.yaml --emit-dir out": "",
		"-f a.yaml b.yaml --emit-dir out":    `unexpected argument "b.yaml": every input is a flag`,
		"--emit-dir":                         "flag needs an argument: -emit-dir",
	} {
		var stderr bytes.Buffer
		o, err := parse(strings.Fields(args), &stderr)
		switch {
		case want == "" && (err != nil || o.emitDir != "out" || !slices.Equal(o.values, multi{"a.yaml", "b.yaml"})):
			t.Errorf("%s: %v %#v", args, err, o)
		case want != "" && (err == nil || !strings.Contains(stderr.String(), want) || !strings.Contains(stderr.String(), "Usage of okdp-compile")):
			t.Errorf("%s: got %v, want %q and the usage on stderr, got %q", args, err, want, stderr.String())
		}
	}
}

// TestVersionOf: the version --version prints, by how the binary was built.
func TestVersionOf(t *testing.T) {
	const module = "github.com/okdp/okdp-compiler"
	// built: the build info of okdp-compile, built in module m, with deps.
	built := func(m debug.Module, deps ...*debug.Module) *debug.BuildInfo {
		return &debug.BuildInfo{Path: module + "/cmd/okdp-compile", Main: m, Deps: deps}
	}
	at := func(v string) debug.Module { return debug.Module{Path: module, Version: v} }
	// another: a main module whose path is a prefix of the package's.
	another := debug.Module{Path: "github.com/okdp", Version: "(devel)"}
	for name, tc := range map[string]struct {
		set  string
		info *debug.BuildInfo
		want string
	}{
		"set at build time":      {"1.2.3", built(at("v0.1.0")), "1.2.3"},
		"go install @v0.1.0":     {"", built(at("v0.1.0")), "0.1.0"},
		"go build in a checkout": {"", built(at("v0.1.1-0.20261012192101-1872c2523100+dirty")), "0.1.1-0.20261012192101-1872c2523100+dirty"},
		"no VCS information":     {"", built(at("(devel)")), "dev"},
		"no build info":          {"", nil, "dev"},
		"go tool in another module": {"", built(another,
			&debug.Module{Path: "cel.dev/cel-go", Version: "v0.32.0"},
			&debug.Module{Path: module, Version: "v0.2.0"}), "0.2.0"},
		"replaced by a directory": {"", built(another,
			&debug.Module{Path: module, Version: "v0.2.0", Replace: &debug.Module{Path: "../okdp-compiler"}}), "dev"},
	} {
		if got := versionOf(tc.set, tc.info); got != tc.want {
			t.Errorf("%s: got %q, want %q", name, got, tc.want)
		}
	}
}

// TestRun: the example compiled into directories holding the files of a
// previous compile: they hold what example/output holds, a file no longer
// written removed; dcr/, its --emit-dir in dcr mode.
func TestRun(t *testing.T) {
	dir := t.TempDir()
	o := example(dir)
	if err := os.MkdirAll(o.emitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"app.values.yaml", "oidc-dcr.values.yaml"} {
		if err := os.WriteFile(filepath.Join(o.emitDir, name), []byte("stale: true\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := run(o); err != nil {
		t.Fatal(err)
	}
	dcr := example(dir)
	dcr.platform = append(dcr.platform, filepath.Join(input, "platform-dcr.yaml"))
	dcr.emitDir, dcr.umbrella, dcr.out = filepath.Join(dir, "dcr"), "", ""
	if err := run(dcr); err != nil {
		t.Fatal(err)
	}
	want := tree(t, output)
	got := tree(t, dir)
	if _, ok := got["out.yaml"]; !ok {
		t.Error("--out: out.yaml not written")
	}
	delete(got, "out.yaml")
	if !maps.Equal(got, want) {
		t.Errorf("got %v, want example/output: %v", slices.Sorted(maps.Keys(got)), slices.Sorted(maps.Keys(want)))
	}
}

// TestRunErrors: what run refuses, before it writes anything.
func TestRunErrors(t *testing.T) {
	// A package whose version an umbrella chart cannot take: it compiles,
	// its umbrella fails.
	pkg := t.TempDir()
	if err := os.CopyFS(pkg, os.DirFS(filepath.Join(input, "package"))); err != nil {
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
	instance := filepath.Join(input, "instances")
	notDir := filepath.Join(input, "platform.yaml")
	protected := filepath.Join("..", "..", "testdata", "protected-upstream.yaml")
	for name, tc := range map[string]struct {
		set   func(o *options)
		files []string // created first under the output directory; a directory when ending with /
		want  string
	}{
		"no release":     {func(o *options) { o.release = "" }, nil, "--release is required"},
		"no namespace":   {func(o *options) { o.namespace = "" }, nil, "--namespace is required"},
		"no instance":    {func(o *options) { o.instance = instance }, nil, "--instance: stat " + instance + ": no such file or directory"},
		"not a dir":      {func(o *options) { o.charts = notDir }, nil, "--charts " + notDir + ": not a directory"},
		"same directory": {func(o *options) { o.umbrella = o.emitDir + "/" }, nil, "--emit-dir and --umbrella name the same directory"},
		"a compile":      {func(o *options) { o.values = append(o.values, protected) }, nil, "ingress.hosts is set by the platform"},
		"an umbrella":    {func(o *options) { o.pkg = pkg }, nil, `service.yaml: version "1.0": the version of the umbrella chart`},
		"another file":   {nil, []string{"umbrella/notes.txt"}, "umbrella: notes.txt is not an output of okdp-compile"},
		"a directory":    {nil, []string{"files/app.values.yaml/"}, "files: app.values.yaml is not an output of okdp-compile"},
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
