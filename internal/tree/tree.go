// Package tree compiles the instances of a tree together (okdp.CompileAll)
// and builds the deployment graph of those that compile (graph.New). It
// puts each problem of the graph at the instance of its release, and lists
// the claims bound to each provider.
package tree

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/okdp/okdp-compiler/internal/graph"
	"github.com/okdp/okdp-compiler/internal/okdp"
)

// Tree is a set of instances compiled together, in their order.
type Tree struct {
	Instances []*Instance
	// Graph orders the releases of the instances that compile. It is nil
	// when the graph has a problem (Instance.GraphErr).
	Graph *graph.Graph
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

// Instance is an instance of a Tree, with its compile.
type Instance struct {
	okdp.Instance
	// Outcome is its compile with the other instances: its result (nil when
	// refused), the reason, and the bound claims of its references.
	Outcome okdp.Outcome
	// GraphErr is the problem of the deployment graph at its release, nil
	// when there is none. Several problems are joined (errors.Join).
	GraphErr error
	// Provided lists the claims bound to it as a provider, sorted by ID.
	Provided []Provided
}

// Provided is a claim bound to a provider, with the instance claiming it.
type Provided struct {
	okdp.Bound
	Claimer *Instance
}

// Compile compiles instances together (okdp.CompileAll). It then builds the
// graph of those that compile, puts its problems at their releases, and
// lists the claims bound to each provider. It does not change instances.
func Compile(ctx context.Context, instances []okdp.Instance) *Tree {
	return build(instances, okdp.CompileAll(ctx, instances))
}

// build makes the tree of instances and their outcomes, in their order.
func build(instances []okdp.Instance, outcomes []okdp.Outcome) *Tree {
	t := &Tree{}
	var results []*okdp.Result
	for i, inst := range instances {
		ti := &Instance{Instance: inst, Outcome: outcomes[i]}
		t.Instances = append(t.Instances, ti)
		res := outcomes[i].Result
		if res == nil {
			continue
		}
		results = append(results, res)
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
