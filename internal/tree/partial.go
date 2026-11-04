package tree

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/okdp/okdp-compiler/internal/graph"
	"github.com/okdp/okdp-compiler/internal/okdp"
)

// Kept: a project of a tree whose output is not written (Partial), and
// why: its instances that do not deploy, each with its problem.
type Kept struct {
	// Namespace is the project.
	Namespace string
	// Problems are why, an instance each, <namespace>/<instance>: <why>,
	// in the order of Instances.
	Problems []string
	// Instances are the instances of Problems.
	Instances []KeptInstance
	// Rederived are the files of the project the output writes of today's
	// settings though it is kept (output.Files): its manifests, its
	// instances' identity, in path order; none when its output is kept
	// whole.
	Rederived []string
}

// KeptInstance: an instance of a kept project that does not deploy, and
// why (Err): its refusal, the problems of the graph at its release,
// joined, or, Depends, the release of a kept project it depends on.
type KeptInstance struct {
	*Instance
	Err error
	// Depends: the instance compiled, the graph has no problem of its own
	// at its release, and it depends on a release of a kept project, whose
	// output is not written.
	Depends bool
}

// Partial: the projects of t written and those kept. results are the
// compiled instances of the projects written, g their graph; kept the
// others, in namespace order: the project of an instance refused, then,
// round after round, of an instance at which the graph of the results of
// the projects not kept yet has a problem of its own (a cycle, a release
// that does not provide what is referenced: its dependencies on the kept
// projects left out), or that depends on a release of a kept project
// (Result.Dependencies: a connection reference, a claim), compiled with
// what that release publishes now, which is not deployed. g is nil only
// when the graph has a problem at no instance of t.
func Partial(t *Tree) (results []*okdp.Result, g *graph.Graph, kept []Kept) {
	return PartialWith(t, nil)
}

// PartialWith: Partial, each instance of also that compiled kept too, as
// one refused, also[ti] its problem: what a target that cannot write it
// keeps (output.Files, a release name in two namespaces); its project,
// then those that depend on a release of it, kept.
func PartialWith(t *Tree, also map[*Instance]error) (results []*okdp.Result, g *graph.Graph, kept []Kept) {
	byNamespace := map[string]*Kept{}
	keep := func(ti *Instance, err error, depends bool) {
		k := byNamespace[ti.Namespace()]
		if k == nil {
			k = &Kept{Namespace: ti.Namespace()}
			byNamespace[ti.Namespace()] = k
		}
		k.Problems = append(k.Problems, fmt.Sprintf("%s/%s: %v", ti.Namespace(), ti.Name, err))
		k.Instances = append(k.Instances, KeptInstance{Instance: ti, Err: err, Depends: depends})
	}
	for _, ti := range t.Instances {
		if ti.Outcome.Err != nil {
			keep(ti, ti.Outcome.Err, false)
		} else if err := also[ti]; err != nil {
			keep(ti, err, false)
		}
	}
	for {
		var left, own []*okdp.Result
		var depends []*Instance
		for _, ti := range t.Instances {
			res := ti.Outcome.Result
			if res == nil || byNamespace[ti.Namespace()] != nil {
				continue
			}
			left = append(left, res)
			mine := *res
			mine.Dependencies = slices.DeleteFunc(slices.Clone(res.Dependencies), func(d okdp.Dependency) bool {
				return byNamespace[cmp.Or(d.Namespace, res.Namespace)] != nil
			})
			if len(mine.Dependencies) < len(res.Dependencies) {
				depends = append(depends, ti)
			}
			own = append(own, &mine)
		}
		_, err := graph.New(own)
		problems := map[*Instance][]error{}
		if err != nil {
			problems = atInstances(t, err)
		}
		if len(problems) == 0 && len(depends) == 0 {
			g, err := graph.New(left)
			if err != nil {
				return nil, nil, sorted(byNamespace)
			}
			return left, g, sorted(byNamespace)
		}
		if err != nil && len(problems) == 0 {
			return nil, nil, sorted(byNamespace)
		}
		var round []KeptInstance
		for _, ti := range t.Instances {
			if errs := problems[ti]; errs != nil {
				round = append(round, KeptInstance{Instance: ti, Err: errors.Join(errs...)})
			} else if slices.Contains(depends, ti) {
				round = append(round, KeptInstance{Instance: ti, Err: dependsOnKept(ti.Outcome.Result, byNamespace), Depends: true})
			}
		}
		for _, ki := range round {
			keep(ki.Instance, ki.Err, ki.Depends)
		}
	}
}

// dependsOnKept: the first dependency of res on a release of a kept
// project, as a problem.
func dependsOnKept(res *okdp.Result, kept map[string]*Kept) error {
	for _, d := range res.Dependencies {
		if to := cmp.Or(d.Namespace, res.Namespace); kept[to] != nil {
			return fmt.Errorf("it depends on %s/%s, of the project %s, whose output is kept", to, d.Instance, to)
		}
	}
	return nil
}

// sorted: the kept projects, in namespace order.
func sorted(byNamespace map[string]*Kept) []Kept {
	var out []Kept
	for _, ns := range slices.Sorted(maps.Keys(byNamespace)) {
		out = append(out, *byNamespace[ns])
	}
	return out
}

// Regressions: the instances of after that do not deploy (Instance.Err)
// though they did in before, matched by namespace and name, in after's
// order: what a change from before to after broke.
func Regressions(before, after *Tree) []*Instance {
	if before == nil || after == nil {
		return nil
	}
	var out []*Instance
	for _, ti := range after.Instances {
		if was := before.Instance(ti.Namespace(), ti.Name); ti.Err() != nil && was != nil && was.Err() == nil {
			out = append(out, ti)
		}
	}
	return out
}
