package graph

import (
	"reflect"
	"strings"
	"testing"

	"github.com/okdp/okdp-compiler/internal/okdp"
)

// result: a compiled instance of namespace demo, by hand: what New reads
// of it, its release, its database output, named after it, these outputs
// of contract server, and its dependencies.
func result(release string, servers []string, deps ...okdp.Dependency) *okdp.Result {
	res := &okdp.Result{Release: release, Namespace: "demo", Dependencies: deps}
	res.Info.Provides = append(res.Info.Provides, okdp.Output{Name: release, Contract: "database"})
	for _, s := range servers {
		res.Info.Provides = append(res.Info.Provides, okdp.Output{Name: s, Contract: "server"})
	}
	return res
}

// named: a reference to the database output of instance, named
// after it.
func named(field, instance string) okdp.Dependency {
	return okdp.Dependency{Instance: instance, Contract: "database", Field: field, Output: instance}
}

// output: a reference to a server output of instance.
func output(field, instance, name string) okdp.Dependency {
	return okdp.Dependency{Instance: instance, Contract: "server", Field: field, Output: name}
}

// TestWaves: a diamond's waves, the longest path to a release without
// dependencies, its dependencies deduplicated and sorted; a namespace
// apart, its releases named as another's.
func TestWaves(t *testing.T) {
	other := result("demo-a", nil)
	other.Namespace = "other"
	g, err := New([]*okdp.Result{
		result("demo-a", nil, named("x", "demo-c"), output("y", "demo-b", "b"), named("z", "demo-b")),
		result("demo-b", []string{"b"}, named("x", "demo-d")),
		result("demo-c", nil, named("x", "demo-d")),
		result("demo-d", nil),
		other,
	})
	if err != nil {
		t.Fatal(err)
	}
	node := func(r string) Node { return Node{"demo", r} }
	want := map[Node]int{node("demo-a"): 2, node("demo-b"): 1, node("demo-c"): 1, node("demo-d"): 0, {"other", "demo-a"}: 0}
	for n, w := range want {
		if got := g.Wave(n); got != w {
			t.Errorf("%s: wave %d, want %d", n, got, w)
		}
	}
	if got, want := g.DependsOn(node("demo-a")), []Node{node("demo-b"), node("demo-c")}; !reflect.DeepEqual(got, want) {
		t.Errorf("depends on %v, want %v", got, want)
	}
	if got := g.DependsOn(Node{"other", "demo-a"}); len(got) != 0 {
		t.Errorf("other/demo-a depends on %v", got)
	}
}

// TestErrors: every problem at once, by release: a missing release, one
// that does not publish the output referenced,
// a reference to itself, a release given twice, a cycle of two, and one
// of three reached from a release outside it, each once.
func TestErrors(t *testing.T) {
	_, err := New([]*okdp.Result{
		result("demo-a", nil, named("db", "demo-x"), output("api", "demo-b", "nope"), named("self", "demo-a")),
		result("demo-b", []string{"b"}),
		result("demo-c", nil, output("api", "demo-b", "demo-b")),
		result("demo-c", nil),
		result("demo-d", nil, named("x", "demo-e")),
		result("demo-e", nil, named("x", "demo-d")),
		result("demo-f", nil, named("x", "demo-g")),
		result("demo-g", nil, named("x", "demo-h")),
		result("demo-h", nil, named("x", "demo-i")),
		result("demo-i", nil, named("x", "demo-g")),
	})
	want := []string{
		`demo/demo-a: db: it references "demo-x", which is not another instance of namespace "demo"`,
		`demo/demo-a: api: it references the server output "nope" of "demo-b", which "demo-b" does not publish`,
		`demo/demo-a: self: it references "demo-a", which is not another instance of namespace "demo"`,
		`demo/demo-c: release "demo-c" is given twice in namespace "demo"`,
		`demo/demo-d: dependency cycle: demo-d -> demo-e -> demo-d: the releases would wait for each other`,
		`demo/demo-g: dependency cycle: demo-g -> demo-h -> demo-i -> demo-g: the releases would wait for each other`,
	}
	if err == nil || err.Error() != strings.Join(want, "\n") {
		t.Fatalf("got %v\nwant %s", err, strings.Join(want, "\n"))
	}
	var nodes []Node
	for _, e := range err.(interface{ Unwrap() []error }).Unwrap() {
		nodes = append(nodes, e.(*Error).Node)
	}
	if len(nodes) != len(want) || nodes[0] != (Node{"demo", "demo-a"}) {
		t.Errorf("nodes: %v", nodes)
	}
}

// TestNotProvided: a reference to an output named after a release that
// does not publish it.
func TestNotProvided(t *testing.T) {
	b := result("demo-b", nil)
	b.Info.Provides = nil
	_, err := New([]*okdp.Result{result("demo-a", nil, named("db", "demo-b")), b})
	want := `demo/demo-a: db: it references the database output "demo-b" of "demo-b", which "demo-b" does not publish`
	if err == nil || err.Error() != want {
		t.Errorf("got %v\nwant %s", err, want)
	}
}
