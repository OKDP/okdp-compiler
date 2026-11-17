// Package tree compiles the instances of a tree together (okdp.CompileAll)
// and builds the deployment graph of those that compile (graph.New). It
// puts each problem of the graph at the instance of its release, and lists
// the claims bound to each provider.
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

// Tree is a set of instances compiled together, in their order.
type Tree struct {
	Instances []*Instance
	// Graph orders the releases of the instances that compile. It is nil
	// when the graph has a problem (Instance.GraphErr).
	Graph *graph.Graph
	// ChartFailures says why the ChartSource could not fetch a chart, by
	// chart. It is nil when every chart was fetched. An instance that names
	// a failed chart lists its key in its own ChartFailures.
	ChartFailures map[chart.Ref]error
	// Refused lists the projects that do not load, in name order. Instances
	// holds no instance of them. Partial keeps their output.
	Refused []Refused
}

// Refused is a project of a Tree that does not load.
type Refused struct {
	Namespace string      // the project
	Problems  []string    // why, each naming its repository and path
	Errs      []error     // the errors behind Problems
	Inputs    okdp.Inputs // the project's context (Namespace, Context, Contracts)
	// Own reports whether Inputs holds the project's own layer. It is false
	// when its project.yaml cannot be read: Inputs then holds the
	// platform's layers alone.
	Own bool
}

// ChartSource is a chart cache that fetches the upstream charts it lacks
// and gives them unpacked (okdp.Inputs.Charts). Each chart is fetched on
// its own: a tree does not depend on every chart being available.
type ChartSource interface {
	// Ensure returns the chart cache, which holds every chart of refs it
	// could fetch, and a map that says why each of the others could not be
	// fetched. A non-nil entry of the map leaves its chart unchecked. A
	// chart of refs that is in neither the cache nor the map counts as
	// missing from a filled cache: the instance that names it is refused.
	Ensure(ctx context.Context, refs []chart.Ref) (fs.FS, map[chart.Ref]error)
}

// Instance is an instance of a Tree, with its compile.
type Instance struct {
	okdp.Instance
	// Outcome is its compile with the other instances: its result (nil when
	// refused), the reason, and the bound claims of its references.
	Outcome okdp.Outcome
	// GraphErr is the problem of the deployment graph at its release, nil
	// when there is none. Several problems are joined (errors.Join).
	GraphErr error
	// ChartFailures says why charts of its result could not be fetched, by
	// chart key (Result.Charts), nil when there is none. The values of
	// these keys were not checked: the instance compiled without the charts.
	ChartFailures map[string]error
	// Provided lists the claims bound to it as a provider, sorted by ID.
	Provided []Provided
}

// Provided is a claim bound to a provider, with the instance claiming it.
type Provided struct {
	okdp.Bound
	Claimer *Instance
}

// Dir is a chart cache that is filled already: a directory of unpacked
// charts (helm pull --untar). It fetches nothing, so Compile sets it on
// every instance up front and compiles them once.
type Dir struct{ FS fs.FS }

// Ensure returns the directory as it is and reports no failure, so a chart
// it lacks fails the check of the instance that names it.
func (d Dir) Ensure(context.Context, []chart.Ref) (fs.FS, map[chart.Ref]error) { return d.FS, nil }

// Compile compiles instances together (okdp.CompileAll). It then builds the
// graph of those that compile, puts its problems at their releases, and
// lists the claims bound to each provider. It does not change instances.
//
// With charts, Compile fetches the upstream charts the first compile names
// (Result.Charts). It then compiles the instances again with them, which
// checks their final values against the charts' defaults and schemas. A
// chart that cannot be fetched leaves the keys of the instances that name
// it unchecked, and nothing else. A Dir needs no fetch: the instances are
// compiled once.
func Compile(ctx context.Context, instances []okdp.Instance, charts ChartSource) *Tree {
	if d, ok := charts.(Dir); ok {
		checked := withCharts(instances, d.FS, nil)
		return build(checked, okdp.CompileAll(ctx, checked), nil)
	}
	// The memo lets the second run reuse the parameters and the contracts
	// the first run read.
	memo := okdp.NewMemo()
	outcomes := okdp.CompileAllWith(ctx, instances, memo)
	if charts == nil {
		return build(instances, outcomes, nil)
	}
	cache, failed := charts.Ensure(ctx, refs(outcomes))
	checked := withCharts(instances, cache, failed)
	return build(checked, okdp.CompileAllWith(ctx, checked, memo), failed)
}

func withCharts(instances []okdp.Instance, charts fs.FS, failed map[chart.Ref]error) []okdp.Instance {
	checked := slices.Clone(instances)
	for i := range checked {
		checked[i].Inputs.Charts, checked[i].Inputs.ChartFailures = charts, failed
	}
	return checked
}

// refs returns the upstream charts of the compiled instances, once each.
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

// build makes the tree of instances and their outcomes, in their order.
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

// atInstances puts the problems of the graph at the instances of their
// releases. It leaves out the problems at no instance of t.
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

// unjoin returns the errors that errors.Join joined, err alone otherwise.
func unjoin(err error) []error {
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		return j.Unwrap()
	}
	return []error{err}
}

// Namespace returns the instance's namespace.
func (ti *Instance) Namespace() string { return ti.Inputs.Namespace }

// Release returns the instance's release.
func (ti *Instance) Release() string { return ti.Inputs.Release }

// Err says why the instance does not deploy: its refusal, else the problem
// of the graph at its release. It returns nil when the instance deploys.
func (ti *Instance) Err() error {
	if ti.Outcome.Err != nil {
		return ti.Outcome.Err
	}
	return ti.GraphErr
}

// Granted returns the output the provider publishes for the claim id, nil
// when it publishes none (refused).
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

// Provisions returns the contracts the instance's package provisions.
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

// Instance returns the instance name of namespace, nil when there is none.
func (t *Tree) Instance(namespace, name string) *Instance {
	for _, ti := range t.Instances {
		if ti.Namespace() == namespace && ti.Name == name {
			return ti
		}
	}
	return nil
}

// Release returns the instance of release in namespace, nil when none.
func (t *Tree) Release(namespace, release string) *Instance {
	for _, ti := range t.Instances {
		if ti.Namespace() == namespace && ti.Release() == release {
			return ti
		}
	}
	return nil
}

// Problems says why each instance that does not deploy fails, as
// "<namespace>/<name>: <why>", in the order of the instances.
func (t *Tree) Problems() []string {
	var out []string
	for _, ti := range t.Instances {
		if err := ti.Err(); err != nil {
			out = append(out, fmt.Sprintf("%s/%s: %v", ti.Namespace(), ti.Name, err))
		}
	}
	return out
}
