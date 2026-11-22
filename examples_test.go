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

	compiler "github.com/okdp/okdp-compiler"
)

var update = flag.Bool("update", false, "rewrite the examples' outputs from the current compiler")

// exampleInstances: the instances of the example name, its input/ read
// as okdp-compile reads it: the input documents of input/config/ (-f), the
// packages and contracts of input/ (--catalog), for its rule set.
func exampleInstances(name string, rules *compiler.RuleSet) ([]compiler.Instance, error) {
	input := filepath.Join("examples", name, "input")
	cat, err := compiler.LoadCatalog([]compiler.CatalogFiles{{Name: name, FS: os.DirFS(input)}})
	if err != nil {
		return nil, err
	}
	return compiler.LoadInputs(context.Background(), []compiler.InputFiles{{Name: name + "/config", FS: os.DirFS(filepath.Join(input, "config"))}}, cat, rules)
}

// exampleRules: the rule set of the example name, its rules/ over the
// mandatory set, or the mandatory set alone when it has none.
func exampleRules(name string) *compiler.RuleSet {
	dir := filepath.Join("examples", name, "rules")
	if _, err := os.Stat(dir); err != nil {
		return compiler.Mandatory()
	}
	rules, err := compiler.LoadRules(os.DirFS(dir))
	if err != nil {
		panic(err)
	}
	return rules
}

// TestExample: each example of examples/, compiled together with its rule
// set (exampleRules): what Write gives of each instance is, byte for
// byte, its output: output/ for one instance, output/<project>/<instance>/
// otherwise.
func TestExample(t *testing.T) {
	dirs, err := os.ReadDir("examples")
	if err != nil || len(dirs) == 0 {
		t.Fatalf("no example: %v", err)
	}
	for _, e := range dirs {
		t.Run(e.Name(), func(t *testing.T) {
			rules := exampleRules(e.Name())
			instances, err := exampleInstances(e.Name(), rules)
			if err != nil {
				t.Fatal(err)
			}
			for i := range instances {
				instances[i].Inputs.Rules = rules
			}
			single := len(instances) == 1
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
