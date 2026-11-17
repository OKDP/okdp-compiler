// Package graph builds the deployment graph of compiled instances. A
// release waits for the releases its connection references point to: of
// its own namespace, or of another namespace for a claim.
package graph

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/okdp/okdp-compiler/internal/okdp"
)

// Node is a release in its namespace.
type Node struct{ Namespace, Release string }

func (n Node) String() string { return n.Namespace + "/" + n.Release }

func compare(a, b Node) int {
	return cmp.Or(cmp.Compare(a.Namespace, b.Namespace), cmp.Compare(a.Release, b.Release))
}

// Graph is the deployment graph. It is acyclic, and each edge leads to a
// release that provides what it is referenced for. It is immutable.
type Graph struct {
	deps  map[Node][]Node // direct dependencies, sorted
	waves map[Node]int
}

// Error is a problem of the graph, at the release where it is found.
type Error struct {
	Node Node
	Err  error
}

func (e *Error) Error() string { return e.Node.String() + ": " + e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

// New builds the graph of results, which must hold every instance of
// their namespaces that compiles. It reports every problem at once,
// joined, each an *Error, sorted by node: a release given twice, a
// dependency on a release that is missing or does not publish the output
// referenced, and each cycle, once, at its smallest release.
func New(results []*okdp.Result) (*Graph, error) {
	byNode := map[Node]*okdp.Result{}
	var errs []*Error
	for _, res := range results {
		n := Node{res.Namespace, res.Release}
		if _, twice := byNode[n]; twice {
			errs = append(errs, &Error{n, fmt.Errorf("release %q is given twice in namespace %q", n.Release, n.Namespace)})
		}
		byNode[n] = res
	}
	g := &Graph{deps: map[Node][]Node{}, waves: map[Node]int{}}
	for n, res := range byNode {
		g.deps[n] = []Node{}
		for _, d := range res.Dependencies {
			to := Node{cmp.Or(d.Namespace, n.Namespace), d.Instance}
			if err := provides(byNode[to], d, n, to); err != nil {
				errs = append(errs, &Error{n, err})
				continue
			}
			g.deps[n] = append(g.deps[n], to)
		}
		slices.SortFunc(g.deps[n], compare)
		g.deps[n] = slices.Compact(g.deps[n])
	}
	errs = append(errs, g.levels()...)
	if errs != nil {
		slices.SortStableFunc(errs, func(a, b *Error) int { return compare(a.Node, b.Node) })
		joined := make([]error, len(errs))
		for i, e := range errs {
			joined[i] = e
		}
		return nil, errors.Join(joined...)
	}
	return g, nil
}

// provides returns nil when res, the result of the release to, publishes
// what the dependency d of from references. res is nil without result.
func provides(res *okdp.Result, d okdp.Dependency, from, to Node) error {
	name, another := d.Instance, "another instance"
	if to.Namespace != from.Namespace {
		name, another = to.String(), "an instance"
	}
	if res == nil || to == from {
		return fmt.Errorf("%s: it references %q, which is not %s of namespace %q", d.Field, name, another, to.Namespace)
	}
	if slices.ContainsFunc(res.Info.Provides, func(o okdp.Output) bool { return o.Contract == d.Contract && o.Name == d.Output }) {
		return nil
	}
	return fmt.Errorf("%s: it references the %s output %q of %q, which %q does not publish", d.Field, d.Contract, d.Output, name, name)
}

// levels sets the wave of each release, level by level (Kahn's
// algorithm). The releases left lie on a cycle or wait for one (cycles).
func (g *Graph) levels() []*Error {
	left := maps.Clone(g.deps)
	for wave := 0; len(left) > 0; wave++ {
		var level []Node
		for n, deps := range left {
			if !slices.ContainsFunc(deps, func(d Node) bool { _, waits := left[d]; return waits }) {
				level = append(level, n)
			}
		}
		if level == nil {
			return cycles(left)
		}
		for _, n := range level {
			g.waves[n] = wave
			delete(left, n)
		}
	}
	return nil
}

// cycles returns an error per cycle of left, the releases that could not
// be ordered, with their dependencies. From each release it searches a
// shortest path back to it. It reports the cycle only when that release
// is the smallest of it, so each cycle is reported once.
func cycles(left map[Node][]Node) []*Error {
	var errs []*Error
	for _, start := range slices.SortedFunc(maps.Keys(left), compare) {
		from := map[Node]Node{} // the release each was reached from
		for queue := []Node{start}; len(queue) > 0; {
			n := queue[0]
			queue = queue[1:]
			for _, d := range left[n] {
				_, seen := from[d]
				if _, ok := left[d]; ok && !seen {
					from[d] = n
					queue = append(queue, d)
				}
			}
			if _, back := from[start]; back {
				break
			}
		}
		if _, back := from[start]; !back {
			continue
		}
		path := []string{start.Release}
		smallest := true
		for n := from[start]; n != start; n = from[n] {
			path = append(path, n.Release)
			smallest = smallest && compare(start, n) < 0
		}
		if smallest {
			path = append(path, start.Release)
			slices.Reverse(path)
			errs = append(errs, &Error{start, fmt.Errorf("dependency cycle: %s: the releases would wait for each other", strings.Join(path, " -> "))})
		}
	}
	return errs
}

// DependsOn returns the releases n waits for, sorted (Flux's dependsOn).
func (g *Graph) DependsOn(n Node) []Node { return slices.Clone(g.deps[n]) }

// Wave returns the Argo CD sync wave of n: 0 for a release that waits for
// none, else one more than the highest wave of those it waits for.
func (g *Graph) Wave(n Node) int { return g.waves[n] }
