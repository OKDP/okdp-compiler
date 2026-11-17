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

// Kept is a project of a tree whose output is not written (Partial): some
// of its instances do not deploy, or the project does not load (Unloaded).
type Kept struct {
	Namespace string // the project
	// Problems says why, one per instance, as "<namespace>/<instance>:
	// <why>", in the order of Instances.
	Problems  []string
	Instances []KeptInstance // the instances that Problems is about
	// Unloaded reports that the project does not load (Tree.Refused).
	// Problems then holds its load problems, and Instances is empty.
	Unloaded bool
	// Dropped says why the project's output is neither kept nor written:
	// its namespace is one no project may have today (okdp.NamespaceError).
	// It is "" when the output is kept (output.Files).
	Dropped string
	// Rederived lists the files that the output writes from today's
	// settings although the project is kept: its manifests and its
	// instances' identity, in path order (output.Files).
	Rederived []string
}

// KeptInstance is an instance of a kept project that does not deploy. Err
// says why.
type KeptInstance struct {
	*Instance
	Err error
	// Depends reports that the instance has no problem of its own: it
	// depends on a release of a kept project, whose output is not written.
	Depends bool
}

// Partial splits the projects of t into those written (results, with their
// graph g) and those kept, in namespace order. A project is kept when it
// does not load or one of its instances is refused. Then, round after round
// until no project is added, a project is kept when one of its instances
// has a graph problem of its own, or depends on a release of a kept
// project: it compiled with what that release publishes now, which is not
// deployed. A graph problem is an instance's own when it remains with the
// dependencies on kept projects left out.
//
// g is nil only when the graph has a problem that is at no instance of t.
func Partial(t *Tree) (results []*okdp.Result, g *graph.Graph, kept []Kept) {
	return PartialWith(t, nil)
}

// PartialWith is Partial with more instances kept. Each instance of also
// that compiled is kept like a refused one, with also[ti] as its problem. A
// target uses it for an instance it cannot write.
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
	for _, r := range t.Refused {
		byNamespace[r.Namespace] = &Kept{Namespace: r.Namespace, Problems: slices.Clone(r.Problems), Unloaded: true}
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

// dependsOnKept names the first dependency of res on a kept project.
func dependsOnKept(res *okdp.Result, kept map[string]*Kept) error {
	for _, d := range res.Dependencies {
		if to := cmp.Or(d.Namespace, res.Namespace); kept[to] != nil {
			return fmt.Errorf("it depends on %s/%s, of the project %s, whose output is kept", to, d.Instance, to)
		}
	}
	return nil
}

func sorted(byNamespace map[string]*Kept) []Kept {
	var out []Kept
	for _, ns := range slices.Sorted(maps.Keys(byNamespace)) {
		out = append(out, *byNamespace[ns])
	}
	return out
}

// Regressions returns the instances of after that do not deploy although
// they did in before, in after's order. Instances are matched by namespace
// and name.
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
