package compiler_test

import (
	"context"
	"flag"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	compiler "github.com/okdp/okdp-compiler"
	"github.com/okdp/okdp-compiler/internal/testrules"
)

var update = flag.Bool("update", false, "rewrite the examples' outputs from the current compiler")

// exampleProject: the project of the single instance of a simple example.
const exampleProject = "demo"

// exampleRepository: the example name, its input/ laid out as a repository:
//
//	catalog/            one package, or one directory per service
//	contracts/          the contracts, if any
//	config/             platform.yaml, then for one package project.yaml and
//	                    the files of its instance; else <project>/project.yaml
//	                    and <project>/<instance>/, the files of each instance
//
// The instance of one package is named after it, in the project demo.
func exampleRepository(name string) fs.FS {
	input := filepath.Join("examples", name, "input")
	tree := fstest.MapFS{}
	read := func(p string) []byte {
		b, err := os.ReadFile(p)
		if err != nil {
			panic(err)
		}
		return b
	}
	add := func(from, to string) {
		_ = filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			rel, _ := filepath.Rel(from, p)
			tree[path.Join(to, filepath.ToSlash(rel))] = &fstest.MapFile{Data: read(p)}
			return nil
		})
	}
	pkg := func(dir string) string {
		c, err := compiler.ParseValues(read(filepath.Join(dir, "Chart.yaml")))
		if err != nil {
			panic(err)
		}
		add(dir, path.Join("catalog", c["name"].(string), c["version"].(string)))
		return c["name"].(string)
	}
	add(filepath.Join(input, "contracts"), "contracts")
	config := filepath.Join(input, "config")
	if b, err := os.ReadFile(filepath.Join(config, "platform.yaml")); err == nil {
		tree["platform.yaml"] = &fstest.MapFile{Data: b}
	}
	entries, err := os.ReadDir(config)
	if err != nil {
		panic(err)
	}
	if _, err := os.Stat(filepath.Join(input, "catalog", "Chart.yaml")); err == nil {
		service := pkg(filepath.Join(input, "catalog"))
		for _, e := range entries {
			switch e.Name() {
			case "platform.yaml":
			case "project.yaml":
				tree[path.Join("projects", exampleProject, e.Name())] = &fstest.MapFile{Data: read(filepath.Join(config, e.Name()))}
			default:
				tree[path.Join("projects", exampleProject, "instances", service, e.Name())] = &fstest.MapFile{Data: read(filepath.Join(config, e.Name()))}
			}
		}
		return tree
	}
	services, err := os.ReadDir(filepath.Join(input, "catalog"))
	if err != nil {
		panic(err)
	}
	for _, s := range services {
		pkg(filepath.Join(input, "catalog", s.Name()))
	}
	for _, p := range entries {
		if !p.IsDir() {
			continue
		}
		dir := filepath.Join(config, p.Name())
		inside, err := os.ReadDir(dir)
		if err != nil {
			panic(err)
		}
		for _, e := range inside {
			if e.IsDir() {
				add(filepath.Join(dir, e.Name()), path.Join("projects", p.Name(), "instances", e.Name()))
			} else {
				tree[path.Join("projects", p.Name(), e.Name())] = &fstest.MapFile{Data: read(filepath.Join(dir, e.Name()))}
			}
		}
	}
	return tree
}

// TestExample: each example of examples/, compiled together: what Write
// gives of each instance is, byte for byte, its output: output/ for one
// package, output/<project>/<instance>/ otherwise.
func TestExample(t *testing.T) {
	dirs, err := os.ReadDir("examples")
	if err != nil || len(dirs) == 0 {
		t.Fatalf("no example: %v", err)
	}
	for _, e := range dirs {
		t.Run(e.Name(), func(t *testing.T) {
			instances, err := compiler.LoadRepository([]compiler.Repository{{Name: e.Name(), FS: exampleRepository(e.Name()), Role: compiler.RoleTree}})
			if err != nil {
				t.Fatal(err)
			}
			for i := range instances {
				instances[i].Inputs.Rules = testrules.Standard()
			}
			_, statErr := os.Stat(filepath.Join("examples", e.Name(), "input", "catalog", "Chart.yaml"))
			single := statErr == nil
			got := map[string][]byte{}
			for i, o := range compiler.CompileAll(context.Background(), instances) {
				if o.Err != nil {
					t.Fatalf("%s: %v", instances[i].Inputs.Release, o.Err)
				}
				files, err := compiler.Write(o.Result)
				if err != nil {
					t.Fatalf("%s: %v", o.Result.Release, err)
				}
				for name, b := range files {
					if single {
						got[name] = b
					} else {
						got[path.Join(o.Result.Namespace, strings.TrimPrefix(o.Result.Release, o.Result.Namespace+"-"), name)] = b
					}
				}
			}
			output := filepath.Join("examples", e.Name(), "output")
			if *update {
				if err := os.RemoveAll(output); err != nil {
					t.Fatal(err)
				}
				for p, b := range got {
					f := filepath.Join(output, filepath.FromSlash(p))
					if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(f, b, 0o644); err != nil {
						t.Fatal(err)
					}
				}
				return
			}
			want := map[string][]byte{}
			if err := fs.WalkDir(os.DirFS(output), ".", func(p string, d fs.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return err
				}
				b, err := os.ReadFile(filepath.Join(output, filepath.FromSlash(p)))
				want[p] = b
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if g, w := slices.Sorted(maps.Keys(got)), slices.Sorted(maps.Keys(want)); !slices.Equal(g, w) {
				t.Fatalf("files %q, want %q (go test . -run TestExample -update)", g, w)
			}
			for p, b := range got {
				if string(b) != string(want[p]) {
					t.Errorf("%s differs (go test . -run TestExample -update):\n%s", p, b)
				}
			}
		})
	}
}
