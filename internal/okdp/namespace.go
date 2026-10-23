// Namespace: instances compiled together (CompileAll), each after the
// instances whose outputs it references, with what they publish.

package okdp

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// Instance: an instance CompileAll compiles: its package and its inputs,
// Published left out: CompileAll gives it.
type Instance struct {
	Definition *Definition
	Inputs     Inputs
}

// Outcome: the compile of one instance of CompileAll: its result, nil
// when it is refused, and why.
type Outcome struct {
	Result *Result
	Err    error
}

// CompileAll compiles instances together, those of one namespace
// (Inputs.Namespace) each after the instances whose outputs it references,
// with what they publish (Inputs.Published, set by CompileAll). An output
// two instances publish refuses both, and the instances whose references
// resolved to it. Outcomes in the order of instances; in a namespace, the
// instances are compiled in that order, which changes no outcome. Whether
// they deploy together is the graph's (graph.New).
func CompileAll(ctx context.Context, instances []Instance) []Outcome {
	out := make([]Outcome, len(instances))
	byNamespace := map[string][]int{}
	for i, inst := range instances {
		ns := inst.Inputs.Namespace
		byNamespace[ns] = append(byNamespace[ns], i)
	}
	for _, ns := range slices.Sorted(maps.Keys(byNamespace)) {
		compileNamespace(ctx, instances, byNamespace[ns], out)
	}
	return out
}

// compileNamespace: the instances idx of one namespace, into out. Kahn's
// algorithm, its edges discovered by compiling:
//
//  1. Each instance is compiled with what the others compiled so far
//     publish. A success publishes its Info and is final; a compile that
//     lacks outputs no instance has published (missingOutputs) waits for
//     them; any other failure is final.
//  2. An instance whose outputs are all published is compiled again, once:
//     final, whatever it gives.
//  3. Those still waiting are compiled again, everything published, for
//     the exact message.
//  4. An output two instances publish refuses both (refuseShared).
//
// A release given twice is refused, each time, and not compiled. Then the
// refusals for an output nobody published say which instances of the
// namespace do not compile: what they publish is unknown.
func compileNamespace(ctx context.Context, all []Instance, namespace []int, out []Outcome) {
	count := map[string]int{}
	for _, i := range namespace {
		count[all[i].Inputs.Release]++
	}
	var idx []int
	for _, i := range namespace {
		if in := all[i].Inputs; count[in.Release] > 1 {
			out[i].Err = fmt.Errorf("release %q is given twice in namespace %q", in.Release, in.Namespace)
		} else {
			idx = append(idx, i)
		}
	}
	published := map[string]Info{}
	compile := func(i int) error {
		in := all[i].Inputs
		in.Published = published
		res, err := Compile(ctx, all[i].Definition, in)
		out[i] = Outcome{Result: res, Err: err}
		if res != nil {
			published[in.Release] = res.Info
		} else {
			delete(published, in.Release)
		}
		return err
	}
	waiting := map[int][]outputKey{}
	for _, i := range idx {
		if m, ok := errors.AsType[*missingOutputs](compile(i)); ok {
			waiting[i] = m.outputs
		}
	}
	for progress := true; progress; {
		progress = false
		for _, i := range idx {
			if keys, ok := waiting[i]; ok && publishes(published, keys) {
				delete(waiting, i)
				_ = compile(i)
				progress = true
			}
		}
	}
	for _, i := range idx {
		if _, ok := waiting[i]; ok {
			_ = compile(i)
		}
	}
	refuseShared(idx, out, compile)
	var refused []string
	for _, i := range namespace {
		if out[i].Result == nil {
			refused = append(refused, all[i].Inputs.Release)
		}
	}
	slices.Sort(refused)
	for _, i := range idx {
		if _, ok := errors.AsType[*missingOutputs](out[i].Err); ok {
			release := all[i].Inputs.Release
			out[i].Err = notCompiled(out[i].Err, slices.DeleteFunc(slices.Clone(refused), func(r string) bool { return r == release }))
		}
	}
}

// publishes reports whether every output of keys is published.
func publishes(published map[string]Info, keys []outputKey) bool {
	for _, k := range keys {
		found := false
		for _, info := range published {
			found = found || slices.ContainsFunc(info.Provides, func(o Output) bool { return o.Contract == k.contract && o.Name == k.name })
		}
		if !found {
			return false
		}
	}
	return true
}

// refuseShared: step 4 of compileNamespace. The instances that publish an
// output of the same contract and name are refused, each: a reference to
// it must find one. They stay published. Then the instances whose
// references resolved through such an output, or through an output of an
// instance refused since, are compiled again, once each, until none more
// fails: each gets the error it would have got compiled after every
// provider, whatever the order.
func refuseShared(idx []int, out []Outcome, compile func(int) error) {
	providers := map[outputKey][]string{}
	for _, i := range idx {
		if res := out[i].Result; res != nil {
			// One output of a contract and name each: a compile refuses two.
			for _, o := range res.Info.Provides {
				k := outputKey{o.Contract, o.Name}
				providers[k] = append(providers[k], res.Release)
			}
		}
	}
	shared := map[outputKey]bool{}
	for _, i := range idx {
		res := out[i].Result
		if res == nil {
			continue
		}
		var errs []error
		for _, o := range res.Info.Provides {
			k := outputKey{o.Contract, o.Name}
			if len(providers[k]) < 2 {
				continue
			}
			shared[k] = true
			others := slices.DeleteFunc(slices.Clone(providers[k]), func(r string) bool { return r == res.Release })
			slices.Sort(others)
			verb := "publishes"
			if len(others) > 1 {
				verb = "publish"
			}
			errs = append(errs, fmt.Errorf("provides %q (%s): %s %s an output of that contract and name too, a reference to it would not know which", o.Name, o.Contract, strings.Join(others, ", "), verb))
		}
		if errs != nil {
			out[i] = Outcome{Err: errors.Join(errs...)}
		}
	}
	if len(shared) == 0 {
		return
	}
	dropped, again := map[string]bool{}, map[int]bool{}
	for more := true; more; {
		more = false
		for _, i := range idx {
			res := out[i].Result
			if res == nil || again[i] || !slices.ContainsFunc(res.Dependencies, func(d Dependency) bool {
				return shared[outputKey{d.Contract, d.Output}] || dropped[d.Instance]
			}) {
				continue
			}
			again[i] = true
			if compile(i) != nil {
				dropped[res.Release], more = true, true
			}
		}
	}
}

// notCompiled: err, the refusal of an instance for an output nobody
// published, with the other instances of its namespace that do not
// compile, in release order: one of them may be its provider.
func notCompiled(err error, others []string) error {
	switch len(others) {
	case 0:
		return err
	case 1:
		return fmt.Errorf("%w (%s does not compile either: what it publishes is unknown)", err, others[0])
	}
	return fmt.Errorf("%w (%s do not compile either: what they publish is unknown)", err, strings.Join(others, ", "))
}
