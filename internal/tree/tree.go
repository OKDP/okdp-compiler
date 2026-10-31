// Package tree: the instances of a tree compiled together
// (okdp.CompileAll), the deployment graph of those that compile
// (graph.New), each problem of the graph at the instance of its release,
// and the claims bound to each provider: what a caller that reads a
// compiled tree (a control plane, its console) asks of it.
package tree

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"

	"github.com/okdp/okdp-compiler/internal/chart"
	"github.com/okdp/okdp-compiler/internal/graph"
	"github.com/okdp/okdp-compiler/internal/okdp"
)

// Tree: instances compiled together, in their order, and the deployment
// graph of those that compile.
type Tree struct {
	Instances []*Instance
	// Graph orders the releases of the instances that compile; nil when it
	// has a problem (Instance.GraphErr).
	Graph *graph.Graph
	// ChartsErr is why the final values were not checked against the
	// upstream charts: a chart the ChartSource could not fetch; nil when
	// they were, or without a ChartSource.
	ChartsErr error
}

// ChartSource: a chart cache that fetches the upstream charts it lacks,
// unpacked as a compile reads them (okdp.Inputs.Charts), for the checks of
// the final values against them.
type ChartSource interface {
	// Ensure: the chart cache, every chart of refs in it, else why one
	// could not be fetched.
	Ensure(ctx context.Context, refs []chart.Ref) (fs.FS, error)
}

// Instance: an instance of a Tree and its compile.
type Instance struct {
	okdp.Instance
	// Outcome is its compile with the other instances: its result, nil
	// when refused, and why; the claims of its references, bound.
	Outcome okdp.Outcome
	// GraphErr is the problem of the deployment graph at its release, nil
	// when none: a dependency on a release that does not provide what is
	// referenced, a cycle.
	GraphErr error
	// Provided are the claims bound to it, its package provisioning their
	// contract, by ID, each with the instance that claims it.
	Provided []Provided
}

// Provided: a claim bound to a provider (Instance.Provided), and the
// instance whose reference claims it.
type Provided struct {
	okdp.Bound
	Claimer *Instance
}

// Compile: instances compiled together (okdp.CompileAll), then the graph
// of those that compile, its problems at their releases, and the claims
// bound to each provider. With charts, the upstream charts the compile
// names (Result.Charts: those of the packages and of the platform, the
// OAuth clients' among them) are fetched, then the instances compiled
// again with them (Inputs.Charts), their final values checked against
// the charts' defaults and schemas; a chart that cannot be fetched leaves
// the values unchecked (Tree.ChartsErr). instances are not changed.
func Compile(ctx context.Context, instances []okdp.Instance, charts ChartSource) *Tree {
	outcomes := okdp.CompileAll(ctx, instances)
	if charts == nil {
		return build(instances, outcomes)
	}
	cache, err := charts.Ensure(ctx, refs(outcomes))
	if err != nil {
		t := build(instances, outcomes)
		t.ChartsErr = err
		return t
	}
	checked := slices.Clone(instances)
	for i := range checked {
		checked[i].Inputs.Charts = cache
	}
	return build(checked, okdp.CompileAll(ctx, checked))
}

// refs: the upstream charts of the instances compiled, once each, in the
// order of the outcomes, then of their chart keys.
func refs(outcomes []okdp.Outcome) []chart.Ref {
	var out []chart.Ref
	for _, o := range outcomes {
		if o.Result == nil {
			continue
		}
		for _, key := range slices.Sorted(maps.Keys(o.Result.Charts)) {
			if ref := o.Result.Charts[key]; !slices.Contains(out, ref) {
				out = append(out, ref)
			}
		}
	}
	return out
}

// build: the tree of instances and their outcomes, in their order.
func build(instances []okdp.Instance, outcomes []okdp.Outcome) *Tree {
	t := &Tree{}
	var results []*okdp.Result
	for i, inst := range instances {
		t.Instances = append(t.Instances, &Instance{Instance: inst, Outcome: outcomes[i]})
		if res := outcomes[i].Result; res != nil {
			results = append(results, res)
		}
	}
	g, err := graph.New(results)
	if err != nil {
		for _, e := range unjoin(err) {
			if ge, ok := errors.AsType[*graph.Error](e); ok {
				if ti := t.Release(ge.Node.Namespace, ge.Node.Release); ti != nil {
					ti.GraphErr = errors.Join(ti.GraphErr, ge.Err)
				}
			}
		}
	} else {
		t.Graph = g
	}
	for _, claimer := range t.Instances {
		for _, b := range claimer.Outcome.Claims {
			if provider := t.Release(b.Namespace, b.Release); provider != nil {
				provider.Provided = append(provider.Provided, Provided{Bound: b, Claimer: claimer})
			}
		}
	}
	for _, ti := range t.Instances {
		slices.SortStableFunc(ti.Provided, func(a, b Provided) int { return strings.Compare(a.ID, b.ID) })
	}
	return t
}

// unjoin: the errors errors.Join joined, err alone otherwise.
func unjoin(err error) []error {
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		return j.Unwrap()
	}
	return []error{err}
}

// Namespace: the instance's namespace.
func (ti *Instance) Namespace() string { return ti.Inputs.Namespace }

// Release: the instance's release.
func (ti *Instance) Release() string { return ti.Inputs.Release }

// Err: why the instance does not deploy, its refusal, else the problem of
// the graph at its release; nil when it does.
func (ti *Instance) Err() error {
	if ti.Outcome.Err != nil {
		return ti.Outcome.Err
	}
	return ti.GraphErr
}

// Granted: what a provider granted a claim, the output it publishes for
// it, named the claim's ID; nil when it publishes none (refused).
func (ti *Instance) Granted(id string) *okdp.Output {
	if ti.Outcome.Result == nil {
		return nil
	}
	for i := range ti.Outcome.Result.Info.Provides {
		if o := &ti.Outcome.Result.Info.Provides[i]; o.Name == id {
			return o
		}
	}
	return nil
}

// Provisions: the contracts the instance's package provisions (okdp.yaml
// provisions), nil when none.
func (ti *Instance) Provisions() []string {
	if ti.Definition == nil {
		return nil
	}
	return ti.Definition.Package.Provisions
}

// Instance: the instance name of namespace, nil when the tree has none.
func (t *Tree) Instance(namespace, name string) *Instance {
	for _, ti := range t.Instances {
		if ti.Namespace() == namespace && ti.Name == name {
			return ti
		}
	}
	return nil
}

// Release: the instance of release of namespace, nil when the tree has
// none.
func (t *Tree) Release(namespace, release string) *Instance {
	for _, ti := range t.Instances {
		if ti.Namespace() == namespace && ti.Release() == release {
			return ti
		}
	}
	return nil
}

// Problems: why each instance that does not deploy does not,
// <namespace>/<name>: <why>, in the order of the instances.
func (t *Tree) Problems() []string {
	var out []string
	for _, ti := range t.Instances {
		if err := ti.Err(); err != nil {
			out = append(out, fmt.Sprintf("%s/%s: %v", ti.Namespace(), ti.Name, err))
		}
	}
	return out
}
