// Package graph: the deployment graph of compiled instances: a release
// waits for the releases its connection references point to
// (Result.Dependencies), of its namespace. Built once from the results of a compile and
// checked, it is what a deployer orders the releases with.
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

// Node: a release, in its namespace.
type Node struct{ Namespace, Release string }

func (n Node) String() string { return n.Namespace + "/" + n.Release }

// compare: nodes by namespace, then release.
func compare(a, b Node) int {
	return cmp.Or(cmp.Compare(a.Namespace, b.Namespace), cmp.Compare(a.Release, b.Release))
}

// Graph: the deployment graph, acyclic, each edge to a release that
// provides what it is referenced for; immutable.
type Graph struct {
	deps  map[Node][]Node // direct dependencies, sorted
	waves map[Node]int
}

// Error: a problem of the graph, at the release it is found at.
type Error struct {
	Node Node
	Err  error
}

func (e *Error) Error() string { return e.Node.String() + ": " + e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

// New: the graph of results, every instance of their namespaces, the
// refused ones being missing. Every problem at once, each an *Error, by
// node: a release given twice; a dependency on a release no result of the
// namespace has, or on one that does not publish what is referenced (an
// output of the contract named Dependency.Output, in its Info.Provides);
// then each
// cycle, once, from its smallest release, by a shortest cycle through it.
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
			to := Node{n.Namespace, d.Instance}
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

// provides: nil when res, the result of the dependency's release to,
// provides what d references from the release from.
func provides(res *okdp.Result, d okdp.Dependency, from, to Node) error {
	if res == nil || to == from {
		return fmt.Errorf("%s: it references %q, which is not another instance of namespace %q", d.Field, d.Instance, to.Namespace)
	}
	if slices.ContainsFunc(res.Info.Provides, func(o okdp.Output) bool { return o.Contract == d.Contract && o.Name == d.Output }) {
		return nil
	}
	return fmt.Errorf("%s: it references the %s output %q of %q, which %q does not publish", d.Field, d.Contract, d.Output, d.Instance, d.Instance)
}

// levels sets the waves, Kahn's algorithm by levels: wave 0, the releases
// with no dependency, then each release once its dependencies have theirs,
// one more than the highest. The releases left lie on a cycle or wait for
// one: each cycle is returned (cycles).
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

// cycles: an error per cycle of left, the releases that could not be
// ordered and their dependencies: from each release, a shortest path back
// to it (breadth first, dependencies in order), reported when the release
// is its smallest.
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

// DependsOn: the releases n waits for, sorted: Flux dependsOn.
func (g *Graph) DependsOn(n Node) []Node { return slices.Clone(g.deps[n]) }

// Wave: 0 for a release that waits for none, else one more than the
// highest of those it waits for: an Argo CD sync wave.
func (g *Graph) Wave(n Node) int { return g.waves[n] }
