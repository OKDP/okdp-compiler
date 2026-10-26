package main

import (
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

// TestMain: the test binary is also okdp-compile, the command of the
// scripts (TestScript).
func TestMain(m *testing.M) {
	testscript.Main(m, map[string]func(){"okdp-compile": main})
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

// TestRun: the tree of each example, its input/db/, compiled into a
// directory holding a stale instance: it holds the example's output, the
// stale instance removed; the full example's tree split into three
// repositories, in either order, gives the same.
func TestRun(t *testing.T) {
	examples := filepath.Join("..", "..", "examples")
	for _, example := range []string{"00_passthrough", "01_minimal", "02_advanced", "03_full"} {
		t.Run(example, func(t *testing.T) {
			dir := t.TempDir()
			stale := filepath.Join(dir, "projects", "demo", "instances", "demo-gone")
			if err := os.MkdirAll(stale, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(stale, "Chart.yaml"), []byte("stale: true\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := run(options{repositories: []string{filepath.Join(examples, example, "input", "db")}, output: dir}); err != nil {
				t.Fatal(err)
			}
			if got, want := tree(t, dir), tree(t, filepath.Join(examples, example, "output")); !maps.Equal(got, want) {
				t.Errorf("got %v, want the example's output: %v", slices.Sorted(maps.Keys(got)), slices.Sorted(maps.Keys(want)))
			}
		})
	}
	db := filepath.Join(examples, "03_full", "input", "db")
	parts := t.TempDir()
	for part, names := range map[string][]string{"platform": {"platform.yaml"}, "catalog": {"catalog", "contracts"}, "projects": {"projects", "contracts"}} {
		for _, name := range names {
			src, dst := filepath.Join(db, name), filepath.Join(parts, part, name)
			var err error
			if name == "platform.yaml" {
				var b []byte
				if b, err = os.ReadFile(src); err == nil {
					err = errors.Join(os.MkdirAll(filepath.Dir(dst), 0o755), os.WriteFile(dst, b, 0o644))
				}
			} else {
				err = os.CopyFS(dst, os.DirFS(src))
			}
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, order := range [][]string{{"platform", "catalog", "projects"}, {"projects", "catalog", "platform"}} {
		dir := t.TempDir()
		var repos []string
		for _, part := range order {
			repos = append(repos, filepath.Join(parts, part))
		}
		if err := run(options{repositories: repos, output: dir}); err != nil {
			t.Fatal(err)
		}
		if got, want := tree(t, dir), tree(t, filepath.Join(examples, "03_full", "output")); !maps.Equal(got, want) {
			t.Errorf("%v: got %v", order, slices.Sorted(maps.Keys(got)))
		}
	}
}

// TestRunErrors: what run refuses, before it reads the tree: no
// repository, --check with an output, --charts without --check, an option
// of another target, a target without its option, repositories inside one
// another or the output directory, a missing one; nothing written.
func TestRunErrors(t *testing.T) {
	full := filepath.Join("..", "..", "examples", "03_full", "input", "db")
	for name, tc := range map[string]struct {
		set  func(o *options)
		want string
	}{
		"no repository":  {func(o *options) { o.repositories = nil }, "--repository is required"},
		"check output":   {func(o *options) { o.check = true }, "--check writes nothing: no --output (-o)"},
		"charts":         {func(o *options) { o.charts = filepath.Join("..", "..", "examples", "03_full", ".charts") }, "--charts is read by --check only: a compile reads no chart"},
		"no output":      {func(o *options) { o.output = "" }, "--output (-o) is required"},
		"target":         {func(o *options) { o.target = "kustomize" }, `--target "kustomize": helm, flux or argocd`},
		"repo url":       {func(o *options) { o.target = "argocd" }, "--repo-url is required by --target argocd"},
		"flux source":    {func(o *options) { o.fluxSource = "a/b" }, "--flux-source is read by --target flux only"},
		"path prefix":    {func(o *options) { o.pathPrefix = "x" }, "--path-prefix is read by --target flux and argocd only"},
		"revision":       {func(o *options) { o.target, o.revision = "flux", "main" }, "--revision is read by --target argocd only"},
		"flux options":   {func(o *options) { o.target, o.fluxSource = "flux", "okdp" }, `Flux.Source "okdp": the GitRepository of the tree, <namespace>/<name>`},
		"twice":          {func(o *options) { o.repositories = append(o.repositories, full) }, "--repository " + full + " is given twice"},
		"inside":         {func(o *options) { o.repositories = append(o.repositories, filepath.Join(full, "projects")) }, "--repository " + filepath.Join(full, "projects") + " is inside --repository " + full},
		"output inside":  {func(o *options) { o.output = filepath.Join(full, "out") }, "--output (-o) " + filepath.Join(full, "out") + " and --repository " + full + " must not contain one another"},
		"a missing tree": {func(o *options) { o.repositories = []string{filepath.Join(full, "nope")} }, "--repository: stat " + filepath.Join(full, "nope") + ": no such file or directory"},
		"a file":         {func(o *options) { o.repositories = []string{filepath.Join(full, "platform.yaml")} }, "--repository " + filepath.Join(full, "platform.yaml") + ": not a directory"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			o := options{repositories: []string{full}, output: dir}
			tc.set(&o)
			err := run(o)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
			if after := tree(t, dir); len(after) > 0 {
				t.Errorf("written: %v", slices.Sorted(maps.Keys(after)))
			}
		})
	}
}

// TestRunCheck: --check compiles and writes nothing, in the current
// directory or elsewhere, with or without a chart cache (03_full's, which
// holds the charts of its instances).
func TestRunCheck(t *testing.T) {
	full, err := filepath.Abs(filepath.Join("..", "..", "examples", "03_full"))
	if err != nil {
		t.Fatal(err)
	}
	for name, charts := range map[string]string{"no chart cache": "", "a chart cache": filepath.Join(full, ".charts")} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			if err := run(options{repositories: []string{filepath.Join(full, "input", "db")}, charts: charts, check: true}); err != nil {
				t.Fatal(err)
			}
			if after := tree(t, dir); len(after) > 0 {
				t.Errorf("written: %v", slices.Sorted(maps.Keys(after)))
			}
		})
	}
}

// TestScript runs the scripts of testdata/cli: okdp-compile as a
// process, its exit status, stdout and stderr, $OKDP_CHARTS. A script
// runs from the repository root, as the commands of the documentation,
// with no $OKDP_* variable but those it sets; its files and outputs are
// under $WORK.
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
