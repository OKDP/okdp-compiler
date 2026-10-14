package compiler_test

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"sync"
	"testing"

	compiler "github.com/okdp/okdp-compiler"
)

// example: the example package (examples/02_advanced/input/service), and
// the inputs of its instance over the context, these files after
// platform.yaml, at level 0.
func example(platform ...string) (*compiler.Definition, compiler.Inputs) {
	def, err := compiler.LoadDefinition(os.DirFS("examples/02_advanced/input/service"))
	if err != nil {
		panic(err)
	}
	in := compiler.Inputs{
		Charts:    os.DirFS("examples/02_advanced/input/charts"),
		Contracts: os.DirFS("examples/02_advanced/input/contracts"),
		Context:   []compiler.ContextLayer{{Values: values("examples/02_advanced/input/platform.yaml")}},
		Values:    []map[string]any{values("examples/02_advanced/input/config.yaml")},
		Release:   "demo-webapp",
		Namespace: "demo",
	}
	for _, f := range platform {
		in.Context = append(in.Context, compiler.ContextLayer{Values: values(f)})
	}
	return def, in
}

// values: a values file, as a values layer.
func values(file string) map[string]any {
	b, err := os.ReadFile(file)
	if err != nil {
		panic(err)
	}
	v, err := compiler.ParseValues(b)
	if err != nil {
		panic(err)
	}
	return v
}

// TestConcurrentCompile: the concurrent use the documentation promises,
// one Definition and the same values layers shared by goroutines, in the
// existing and the dcr modes (the OAuth client chart added to a copy of the
// Definition's charts): each compile gives what it gives alone, and the
// inputs are not changed. Meaningful under go test -race.
func TestConcurrentCompile(t *testing.T) {
	def, in := example()
	dcr := in
	dcr.Context = append(slices.Clip(in.Context), compiler.ContextLayer{Values: values("examples/02_advanced/input/platform-dcr.yaml")})
	inputs := []compiler.Inputs{in, dcr}
	snapshot := func() string {
		b, err := json.Marshal([]any{def.Chart, def.Package.Require, def.Package.Defaults, def.Package.Upstream, string(def.Okdp), string(def.Values), dcr.Context, in.Values})
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	before := snapshot()
	want := make([]*compiler.Result, len(inputs))
	for i, in := range inputs {
		res, err := compiler.Compile(context.Background(), def, in)
		if err != nil {
			t.Fatal(err)
		}
		want[i] = res
	}
	got := make([]*compiler.Result, 16)
	errs := make([]error, len(got))
	var wg sync.WaitGroup
	for i := range got {
		wg.Go(func() { got[i], errs[i] = compiler.Compile(context.Background(), def, inputs[i%len(inputs)]) })
	}
	wg.Wait()
	for i, res := range got {
		if errs[i] != nil || !reflect.DeepEqual(res, want[i%len(inputs)]) {
			t.Errorf("compile %d (inputs %d): %v, not the result of a compile alone", i, i%len(inputs), errs[i])
		}
	}
	if snapshot() != before {
		t.Error("the Definition or the values layers changed")
	}
}
