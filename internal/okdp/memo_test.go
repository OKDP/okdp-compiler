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
// CompileAll, and not again by a second run with the same Memo, which gives
// the same outcomes.
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
	memo := NewMemo()
	first := CompileAllWith(context.Background(), instances, memo)
	for i, o := range first {
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
	second := CompileAllWith(context.Background(), instances, memo)
	if !reflect.DeepEqual(first, second) {
		t.Errorf("a second run with the Memo differs:\n%+v\n%+v", first, second)
	}
	if got := files.reads("config.schema.json"); got != len(instances) {
		t.Errorf("config.schema.json read %d times after a second run", got)
	}
	if got := contractFiles.reads("server.schema.json"); got != 1 {
		t.Errorf("server.schema.json read %d times after a second run", got)
	}
	// Without a Memo, each run reads them again.
	if third := CompileAll(context.Background(), instances); !reflect.DeepEqual(first, third) {
		t.Errorf("CompileAll differs from CompileAllWith:\n%+v\n%+v", first, third)
	}
	if got := files.reads("config.schema.json"); got <= len(instances) {
		t.Errorf("config.schema.json read %d times after a run without the Memo", got)
	}
}

// TestMemoPackage: the parameters of an instance are the Memo's only for
// the package they were read from.
func TestMemoPackage(t *testing.T) {
	memo := NewMemo()
	in := node("a", M{"publishes": []any{"a"}}).Inputs
	a := memo.forInstance(0, nodeDef, in)
	if b := memo.forInstance(0, nodeDef, in); a.params != b.params {
		t.Error("the same package, other parameters")
	}
	other := *nodeDef
	if b := memo.forInstance(0, &other, in); a.params == b.params {
		t.Error("another package, the same parameters")
	}
	if b := memo.forInstance(1, nodeDef, in); a.params == b.params || a.contracts != b.contracts {
		t.Error("another instance: its own parameters, the same contracts")
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
