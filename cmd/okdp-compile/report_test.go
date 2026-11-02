package main

import (
	"errors"
	"reflect"
	"testing"

	compiler "github.com/okdp/okdp-compiler"
)

// TestReportGraph: a problem of the graph refuses the instance of its
// release, its messages joined; one of no instance of the report, or
// that is no problem of a release, is the tree's.
func TestReportGraph(t *testing.T) {
	r := newReport()
	r.add(compiler.Instance{Name: "web", Inputs: compiler.Inputs{Namespace: "demo", Release: "demo-web"}}, compiler.Outcome{Result: &compiler.Result{Namespace: "demo", Release: "demo-web", Service: "web", Version: "1.0.0"}})
	node := compiler.GraphNode{Namespace: "demo", Release: "demo-web"}
	r.graphError(&compiler.GraphError{Node: node, Err: errors.New("a cycle")})
	r.graphError(&compiler.GraphError{Node: node, Err: errors.New("a dependency")})
	r.graphError(&compiler.GraphError{Node: compiler.GraphNode{Namespace: "demo", Release: "demo-gone"}, Err: errors.New("x")})
	r.graphError(errors.New("y"))
	if ir := r.Instances[0]; ir.Status != statusRefused || ir.Error != "a cycle\na dependency" {
		t.Errorf("the instance: %+v", ir)
	}
	if want := []string{"demo/demo-gone: x", "y"}; !reflect.DeepEqual(r.Errors, want) {
		t.Errorf("the tree's: %q", r.Errors)
	}
}
