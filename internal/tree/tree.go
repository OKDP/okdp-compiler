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

	"github.com/okdp/okdp-compiler/chart"
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
	// ChartFailures: the charts the ChartSource could not fetch, why, by
	// chart; nil without a ChartSource or when every chart was fetched.
	// The final values of the instances that name others are checked; the
	// keys of each instance that names one are its ChartFailures.
	ChartFailures map[chart.Ref]error
	// Refused are the projects that do not load (repository.Load), no
	// instance of them in Instances, in name order: their output is kept
	// (Partial), not written.
	Refused []Refused
}

// Refused: a project of a Tree that does not load, why, and its own
// context for what the platform decides of it all the same.
type Refused struct {
	// Namespace is the project.
	Namespace string
	// Problems are why, each naming its repository and path, with their
	// errors in Errs.
	Problems []string
	Errs     []error
	// Inputs are the project's context (Namespace, Context, Contracts).
	Inputs okdp.Inputs
	// Own: Inputs holds the project's own layer; not when its
	// project.yaml does not read, the platform's alone.
	Own bool
}

// ChartSource: a chart cache that fetches the upstream charts it lacks,
// unpacked as a compile reads them (okdp.Inputs.Charts), for the checks of
// the final values against them. A chart is fetched or not on its own: a
// tree does not depend on each of its charts' availability.
type ChartSource interface {
	// Ensure: the chart cache, holding every chart of refs it could
	// fetch, and why each of the others could not be, by chart: an entry
	// of that map (non nil) is what leaves a chart unchecked and reported
	// per instance (Tree.ChartFailures, Instance.ChartFailures); a chart
	// of refs that is neither in the cache nor in the map is treated as
	// any chart missing from a filled cache, the instance naming it
	// refused (Dir).
	Ensure(ctx context.Context, refs []chart.Ref) (fs.FS, map[chart.Ref]error)
}

// Instance: an instance of a Tree and its compile.
type Instance struct {
	okdp.Instance
	// Outcome is its compile with the other instances: its result, nil
	// when refused, and why; the claims of its references, bound.
	Outcome okdp.Outcome
	// GraphErr is the problem of the deployment graph at its release, nil
	// when none: a dependency on a release that does not provide what is
	// referenced, a cycle; several joined once (errors.Join), each its own.
	GraphErr error
	// ChartFailures are why charts of its result could not be fetched, by
	// chart key (Result.Charts), nil when none: the values of these keys
	// were not checked, an instance compiled without them; those of the
	// others were (Tree.ChartFailures).
	ChartFailures map[string]error
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

// Dir: a chart cache filled already, a directory of unpacked charts
// (helm pull --untar), which fetches nothing: Compile sets it on every
// instance at once, and compiles them once.
type Dir struct{ FS fs.FS }

// Ensure: the directory as it is, fetching nothing and reporting no
// failure: a chart it lacks fails the check of the instance that names it.
func (d Dir) Ensure(context.Context, []chart.Ref) (fs.FS, map[chart.Ref]error) { return d.FS, nil }

// Compile: instances compiled together (okdp.CompileAll), then the graph
// of those that compile, its problems at their releases, and the claims
// bound to each provider. With charts, the upstream charts the compile
// names (Result.Charts: those of the packages and of the platform, the
// OAuth clients' among them) are fetched, then the instances compiled
// again with them (Inputs.Charts), their final values checked against
// the charts' defaults and schemas. Fetching fails per chart: a chart
// that cannot be fetched leaves the keys of the instances that name it
// unchecked, reported per instance (Instance.ChartFailures) and per chart
// (Tree.ChartFailures), the rest of the checks and the others' charts
// as they are. A Dir is set at once, the instances compiled once. instances
// are not changed.
func Compile(ctx context.Context, instances []okdp.Instance, charts ChartSource) *Tree {
	if d, ok := charts.(Dir); ok {
		checked := withCharts(instances, d.FS, nil)
		return build(checked, okdp.CompileAll(ctx, checked), nil)
	}
	// The second run reads the parameters and the contracts the first read.
	memo := okdp.NewMemo()
	outcomes := okdp.CompileAllWith(ctx, instances, memo)
	if charts == nil {
		return build(instances, outcomes, nil)
	}
	cache, failed := charts.Ensure(ctx, refs(outcomes))
	checked := withCharts(instances, cache, failed)
	return build(checked, okdp.CompileAllWith(ctx, checked, memo), failed)
}

// withCharts: a copy of instances, each with the chart cache charts and
// the charts failed that could not be fetched.
func withCharts(instances []okdp.Instance, charts fs.FS, failed map[chart.Ref]error) []okdp.Instance {
	checked := slices.Clone(instances)
	for i := range checked {
		checked[i].Inputs.Charts, checked[i].Inputs.ChartFailures = charts, failed
	}
	return checked
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

// build: the tree of instances and their outcomes, in their order, each
// with the charts of failed its result names (Tree.ChartFailures).
func build(instances []okdp.Instance, outcomes []okdp.Outcome, failed map[chart.Ref]error) *Tree {
	t := &Tree{}
	if len(failed) > 0 {
		t.ChartFailures = failed
	}
	var results []*okdp.Result
	for i, inst := range instances {
		ti := &Instance{Instance: inst, Outcome: outcomes[i]}
		t.Instances = append(t.Instances, ti)
		res := outcomes[i].Result
		if res == nil {
			continue
		}
		results = append(results, res)
		for key, ref := range res.Charts {
			if why := failed[ref]; why != nil {
				if ti.ChartFailures == nil {
					ti.ChartFailures = map[string]error{}
				}
				ti.ChartFailures[key] = why
			}
		}
	}
	g, err := graph.New(results)
	if err != nil {
		for ti, errs := range atInstances(t, err) {
			ti.GraphErr = errors.Join(errs...)
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

// atInstances: the problems of the graph, err of graph.New, at the
// instances of their releases, each instance's in their order; those at
// no instance of t left out.
func atInstances(t *Tree, err error) map[*Instance][]error {
	out := map[*Instance][]error{}
	for _, e := range unjoin(err) {
		if ge, ok := errors.AsType[*graph.Error](e); ok {
			if ti := t.Release(ge.Node.Namespace, ge.Node.Release); ti != nil {
				out[ti] = append(out[ti], ge.Err)
			}
		}
	}
	return out
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
	var out []string
	for _, p := range ti.Definition.Package.Provisions {
		out = append(out, p.Contract)
	}
	return out
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
