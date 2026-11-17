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

const rulesDir = "../../testdata/rules"

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

// example: a tree of the example of one package, its input/ laid out as a
// repository, and what it compiles to, under its instance's directory; the
// instance named after the package, in the project demo.
func example(t *testing.T, name, service, version string) (repo string, want map[string]string) {
	t.Helper()
	input := filepath.Join("..", "..", "examples", name, "input")
	tree := map[string]string{}
	for p, c := range files(t, filepath.Join(input, "catalog")) {
		tree["catalog/"+service+"/"+version+"/"+p] = c
	}
	for p, c := range files(t, filepath.Join(input, "config")) {
		switch p {
		case "platform.yaml":
			tree[p] = c
		case "project.yaml":
			tree["projects/demo/"+p] = c
		default:
			tree["projects/demo/instances/"+service+"/"+p] = c
		}
	}
	repo = t.TempDir()
	write(t, repo, tree)
	want = map[string]string{}
	for p, c := range files(t, filepath.Join("..", "..", "examples", name, "output")) {
		want["projects/demo/instances/demo-"+service+"/"+p] = c
	}
	return repo, want
}

// TestRun: the tree of an example compiled into a directory the command
// wrote before, holding a stale instance: it holds the example's output
// and the marker, the stale instance removed.
func TestRun(t *testing.T) {
	for _, tc := range []struct{ example, service, version string }{
		{"00_passthrough", "echo", "1.2.0"},
		{"01_minimal", "hello", "1.0.0"},
	} {
		t.Run(tc.example, func(t *testing.T) {
			repo, want := example(t, tc.example, tc.service, tc.version)
			out := t.TempDir()
			write(t, out, map[string]string{outdir.MarkerFile: outdir.Marker, "projects/demo/instances/demo-gone/values.yaml": "{}\n"})
			if err := run(options{repositories: []string{repo}, rules: rulesDir, output: out, costLimit: defaultCostLimit}); err != nil {
				t.Fatal(err)
			}
			want[outdir.MarkerFile] = outdir.Marker
			if got := files(t, out); !maps.Equal(got, want) {
				t.Errorf("wrote\n%v\nwant\n%v", got, want)
			}
		})
	}
}

// TestRunWithoutRules: no --rules, the compile applies none and still
// writes the values of a package that needs no rule.
func TestRunWithoutRules(t *testing.T) {
	repo, _ := example(t, "00_passthrough", "echo", "1.2.0")
	out := t.TempDir()
	if err := run(options{repositories: []string{repo}, output: out}); err != nil {
		t.Fatal(err)
	}
	if _, ok := files(t, out)["projects/demo/instances/demo-echo/values.yaml"]; !ok {
		t.Errorf("no values.yaml: %v", files(t, out))
	}
}

// TestRunRefused: an instance refused, the command fails and writes
// nothing; --check reports the same.
func TestRunRefused(t *testing.T) {
	repo, _ := example(t, "01_minimal", "hello", "1.0.0")
	write(t, repo, map[string]string{"projects/demo/instances/hello/config.yaml": "replicas: many\n"})
	out := filepath.Join(t.TempDir(), "out")
	if err := run(options{repositories: []string{repo}, rules: rulesDir, output: out}); !errors.Is(err, errReported) {
		t.Errorf("run: %v", err)
	}
	if _, err := os.Stat(out); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("output written: %v", err)
	}
	if err := run(options{repositories: []string{repo}, rules: rulesDir, check: true}); !errors.Is(err, errReported) {
		t.Errorf("run --check: %v", err)
	}
}

// TestCheck: --check compiles and writes nothing.
func TestCheck(t *testing.T) {
	repo, _ := example(t, "01_minimal", "hello", "1.0.0")
	before := files(t, repo)
	if err := run(options{repositories: []string{repo}, rules: rulesDir, check: true}); err != nil {
		t.Fatal(err)
	}
	if got := files(t, repo); !maps.Equal(got, before) {
		t.Errorf("--check changed the tree: %v", got)
	}
}

// TestRefusedOptions: what the command refuses before it reads a tree.
func TestRefusedOptions(t *testing.T) {
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	write(t, repo, map[string]string{"platform.yaml": "{}\n"})
	notMarked := filepath.Join(dir, "other")
	write(t, notMarked, map[string]string{"projects/keep.yaml": "{}\n"})
	for _, tc := range []struct {
		name string
		o    options
		want string
	}{
		{"no repository", options{output: dir}, "--repository is required"},
		{"no output", options{repositories: []string{repo}}, "--output (-o) is required"},
		{"check with output", options{repositories: []string{repo}, check: true, output: dir}, "--check writes nothing"},
		{"check with force", options{repositories: []string{repo}, check: true, force: true}, "--check writes nothing"},
		{"charts without check", options{repositories: []string{repo}, output: dir, charts: dir}, "--charts is read by --check only"},
		{"repository twice", options{repositories: []string{repo, repo}, check: true}, "is given twice"},
		{"output in repository", options{repositories: []string{repo}, output: filepath.Join(repo, "out")}, "must not contain one another"},
		{"output in rules", options{repositories: []string{repo}, rules: dir, output: filepath.Join(dir, "out")}, "must not contain one another"},
		{"output not ours", options{repositories: []string{repo}, output: notMarked}, "was not written by okdp-compile"},
		{"rules missing", options{repositories: []string{repo}, rules: filepath.Join(dir, "none"), check: true}, "--rules"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := run(tc.o)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %v, want %q", err, tc.want)
			}
		})
	}
}
