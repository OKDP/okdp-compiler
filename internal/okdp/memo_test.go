package okdp

import (
	"context"
	"io/fs"
	"os"
	"reflect"
	"sync"
	"testing"
	"testing/fstest"
)

// counted: a file system that counts the opens of each file.
type counted struct {
	fs.FS
	mu    sync.Mutex
	opens map[string]int
}

func count(fsys fs.FS) *counted {
	return &counted{FS: fsys, opens: map[string]int{}}
}

func (c *counted) Open(name string) (fs.File, error) {
	c.mu.Lock()
	c.opens[name]++
	c.mu.Unlock()
	return c.FS.Open(name)
}

func (c *counted) reads(name string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.opens[name]
}

// TestCompileAllReadsOnce: the parameter schema of each instance and the
// contracts of a set of instances are read once by the compiles of
// CompileAll.
func TestCompileAllReadsOnce(t *testing.T) {
	files, contractFiles := count(nodeDef.Files), count(nodeContracts)
	def := *nodeDef
	def.Files = files
	var instances []Instance
	for _, name := range []string{"c", "b", "a"} {
		in := node(name, M{"publishes": []any{name}})
		if name != "a" {
			in = node(name, M{"uses": []any{"a"}, "publishes": []any{name}})
		}
		in.Definition = &def
		in.Inputs.Contracts = contractFiles
		instances = append(instances, in)
	}
	for i, o := range CompileAll(context.Background(), instances) {
		if o.Err != nil {
			t.Fatalf("instance %d: %v", i, o.Err)
		}
	}
	if got := files.reads("config.schema.json"); got != len(instances) {
		t.Errorf("config.schema.json read %d times by %d instances", got, len(instances))
	}
	if got := contractFiles.reads("server.schema.json"); got != 1 {
		t.Errorf("server.schema.json read %d times", got)
	}
}

// TestContractDefaultsAreCopies: a default the contract completes a
// connection with is a copy: the contract is read once and shared.
func TestContractDefaultsAreCopies(t *testing.T) {
	c, err := readContract(fstest.MapFS{"c.schema.json": {Data: []byte(`{"properties": {"tags": {"default": ["a"]}, "conf": {"default": {"k": "v"}}}}`)}}, "c")
	if err != nil {
		t.Fatal(err)
	}
	first := M{}
	if err := c.complete(first, "first"); err != nil {
		t.Fatal(err)
	}
	first["tags"].([]any)[0] = "changed"
	first["conf"].(M)["k"] = "changed"
	second := M{}
	if err := c.complete(second, "second"); err != nil {
		t.Fatal(err)
	}
	if want := (M{"tags": []any{"a"}, "conf": M{"k": "v"}}); !reflect.DeepEqual(second, want) {
		t.Errorf("a second completion = %#v, want %#v", second, want)
	}
}

func TestSameFS(t *testing.T) {
	dir := os.DirFS(".")
	one := count(dir)
	for name, tc := range map[string]struct {
		a, b fs.FS
		want bool
	}{
		"nil, nil":         {nil, nil, true},
		"nil, a directory": {nil, dir, false},
		"one directory":    {dir, os.DirFS("."), true},
		"two directories":  {dir, os.DirFS(".."), false},
		"two maps":         {fstest.MapFS{}, fstest.MapFS{}, false},
		"one map":          {nodeContracts, nodeContracts, true},
		"two types":        {dir, fstest.MapFS{}, false},
		"one pointer":      {one, one, true},
		"two pointers":     {count(dir), count(dir), false},
	} {
		if got := sameFS(tc.a, tc.b); got != tc.want {
			t.Errorf("%s: sameFS = %v, want %v", name, got, tc.want)
		}
	}
}
