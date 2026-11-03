package tree

import (
	"errors"
	"reflect"
	"testing"
	"testing/fstest"

	"github.com/okdp/okdp-compiler/internal/okdp"
)

// on: a dependency on release, of namespace when not "".
func on(namespace, release string) okdp.Dependency {
	return okdp.Dependency{Namespace: namespace, Instance: release, Contract: "database", Field: "database", Output: "db"}
}

// compiled: the outcome of an instance of namespace that compiled, its
// release <namespace>-<name>, publishing db, depending on deps.
func compiled(namespace, name string, deps ...okdp.Dependency) okdp.Outcome {
	return okdp.Outcome{Result: &okdp.Result{
		Namespace: namespace, Release: namespace + "-" + name, Dependencies: deps,
		Info: okdp.Info{Provides: []okdp.Output{{Contract: "database", Name: "db"}}},
	}}
}

// TestPartial: a refused instance keeps its project; an instance that
// depends on a release of a kept project keeps its own, round after
// round, said so; a problem of the graph of its own (a cycle, a release
// that does not exist) keeps it with that problem; the others written.
func TestPartial(t *testing.T) {
	var instances []okdp.Instance
	var outcomes []okdp.Outcome
	add := func(namespace, name string, o okdp.Outcome) {
		instances = append(instances, instance(namespace, name))
		outcomes = append(outcomes, o)
	}
	add("a", "a1", okdp.Outcome{Err: errors.New("refused")})
	add("a", "a2", compiled("a", "a2"))
	add("b", "b1", compiled("b", "b1", on("a", "a-a2")))
	add("c", "c1", compiled("c", "c1", on("b", "b-b1")))
	add("d", "d1", compiled("d", "d1", on("", "d-d2")))
	add("d", "d2", compiled("d", "d2", on("", "d-d1")))
	add("e", "e1", compiled("e", "e1", on("", "e-gone")))
	add("f", "f1", compiled("f", "f1"))
	add("g", "g1", compiled("g", "g1", on("a", "a-a1")))
	tr := build(instances, outcomes)

	results, g, kept := Partial(tr)
	if g == nil || len(results) != 1 || results[0].Release != "f-f1" {
		t.Fatalf("written: %v %v", results, g)
	}
	type problem struct {
		namespace string
		problems  []string
		depends   []bool
	}
	var got []problem
	for _, k := range kept {
		p := problem{namespace: k.Namespace, problems: k.Problems}
		for i, ki := range k.Instances {
			p.depends = append(p.depends, ki.Depends)
			if want := ki.Namespace() + "/" + ki.Name + ": " + ki.Err.Error(); k.Problems[i] != want {
				t.Errorf("problem %q, want %q", k.Problems[i], want)
			}
		}
		got = append(got, p)
	}
	want := []problem{
		{"a", []string{"a/a1: refused"}, []bool{false}},
		{"b", []string{"b/b1: it depends on a/a-a2, of the project a, whose output is kept"}, []bool{true}},
		{"c", []string{"c/c1: it depends on b/b-b1, of the project b, whose output is kept"}, []bool{true}},
		{"d", []string{"d/d1: dependency cycle: d-d1 -> d-d2 -> d-d1: the releases would wait for each other"}, []bool{false}},
		{"e", []string{`e/e1: database: it references "e-gone", which is not another instance of namespace "e"`}, []bool{false}},
		{"g", []string{"g/g1: it depends on a/a-a1, of the project a, whose output is kept"}, []bool{true}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("kept:\n%v\nwant\n%v", got, want)
	}

	// Nothing refused, no problem: everything written, nothing kept.
	results, g, kept = Partial(build(instances[7:8], outcomes[7:8]))
	if g == nil || len(results) != 1 || kept != nil {
		t.Errorf("a tree that deploys: %v %v %v", results, g, kept)
	}
}

// TestRegressions: the instances that deployed before and do not after,
// in after's order; a new one, or one that did not deploy before, is none.
func TestRegressions(t *testing.T) {
	ok, refused := compiled("demo", "x"), okdp.Outcome{Err: errors.New("refused")}
	before := build([]okdp.Instance{instance("demo", "x"), instance("demo", "y"), instance("demo", "z")}, []okdp.Outcome{ok, ok, refused})
	after := build([]okdp.Instance{instance("demo", "w"), instance("demo", "y"), instance("demo", "x"), instance("demo", "z")}, []okdp.Outcome{refused, refused, ok, refused})
	var names []string
	for _, ti := range Regressions(before, after) {
		names = append(names, ti.Name)
	}
	if !reflect.DeepEqual(names, []string{"y"}) {
		t.Errorf("regressions: %v", names)
	}
	if Regressions(nil, after) != nil {
		t.Error("regressions of no tree before")
	}
}

// TestDir: a chart cache filled already is every instance's at once; the
// instances given are not changed.
func TestDir(t *testing.T) {
	def, err := okdp.LoadDefinition(fstest.MapFS{"Chart.yaml": {Data: []byte("apiVersion: v2\nname: app\nversion: 1.0.0\n")}})
	if err != nil {
		t.Fatal(err)
	}
	charts := fstest.MapFS{"x": {}}
	in := instance("demo", "a")
	in.Definition = def
	instances := []okdp.Instance{in}
	tr := Compile(t.Context(), instances, Dir{FS: charts})
	if got := tr.Instances[0].Inputs.Charts; !reflect.DeepEqual(got, charts) || instances[0].Inputs.Charts != nil {
		t.Errorf("charts: %v, given %v", got, instances[0].Inputs.Charts)
	}
	if err := tr.Instances[0].Err(); err != nil || tr.ChartsErr != nil {
		t.Errorf("compiled: %v %v", err, tr.ChartsErr)
	}
}
