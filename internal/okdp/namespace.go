// Namespace: instances compiled together (CompileAll), each after the
// instances whose outputs it references, or that provide its claims, with
// what they publish.

package okdp

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"path"
	"reflect"
	"slices"
	"strings"

	"github.com/okdp/okdp-compiler/internal/value"
)

// Instance: an instance CompileAll compiles: its package and its inputs,
// Published, Claims and Bindings left out: CompileAll gives them.
type Instance struct {
	Definition *Definition
	Inputs     Inputs
}

// Outcome: the compile of one instance of CompileAll: its result, nil
// when it is refused, and why; the claims of its references, bound.
type Outcome struct {
	Result *Result
	Err    error
	Claims []Bound
}

// Bound: a claim of a reference, bound to its provider (CompileAll).
type Bound struct {
	Field              string // the reference's parameter path
	Contract           string // its contract
	ID                 string // the claim's ID: the name of the output its provider publishes for it
	Namespace, Release string // the provider's instance
	Class              string // the class whose provider it is (global.okdp.provisioning.<contract>.classes); "" when the claim names the provider
	Named              bool   // the claim names its class or its provider; else the class is the default one of the claimer's context (global.okdp.provisioning.<contract>.default)
}

// CompileAll compiles instances together. Each claim of a reference is
// bound to the instance that provisions its contract, the one it names or
// the provider of its class, the one it names or the default one of the
// claimer's context, global.okdp.provisioning (bind): the provider gets it
// (Inputs.Claims), its claimer the binding (Inputs.Bindings). Each instance
// is compiled after the instances of its namespace whose outputs it
// references and the providers of its claims, with what they publish
// (Inputs.Published, set by CompileAll). An output two instances of a
// namespace publish refuses both, and the instances whose references
// resolved to it, or whose claims are bound to one of them, of any namespace.
// Outcomes in the order of instances, which changes none
// of them. Whether they deploy together is the graph's (graph.New).
func CompileAll(ctx context.Context, instances []Instance) []Outcome {
	t := &tree{ctx: ctx, all: instances, out: make([]Outcome, len(instances)), published: map[string]map[string]Info{}}
	count := map[[2]string]int{}
	for _, inst := range instances {
		count[[2]string{inst.Inputs.Namespace, inst.Inputs.Release}]++
	}
	var idx []int
	for i, inst := range instances {
		if in := inst.Inputs; count[[2]string{in.Namespace, in.Release}] > 1 {
			t.out[i].Err = fmt.Errorf("release %q is given twice in namespace %q", in.Release, in.Namespace)
		} else {
			idx = append(idx, i)
		}
	}
	idx = t.bind(idx)
	t.index = map[[2]string]int{}
	for _, i := range idx {
		t.index[[2]string{instances[i].Inputs.Namespace, instances[i].Inputs.Release}] = i
	}
	t.compileAll(idx)
	byNamespace := map[string][]int{}
	for _, i := range idx {
		ns := instances[i].Inputs.Namespace
		byNamespace[ns] = append(byNamespace[ns], i)
	}
	for _, ns := range slices.Sorted(maps.Keys(byNamespace)) {
		refuseShared(byNamespace[ns], t.out, t.compile)
	}
	t.refuseClaimers(idx)
	t.notes(idx)
	return t.out
}

// tree: the instances CompileAll compiles, their outcomes, what those
// compiled publish, by namespace and release, and their claims, bound.
type tree struct {
	ctx       context.Context
	all       []Instance
	out       []Outcome
	published map[string]map[string]Info
	claims    map[int][]Claim            // a provider's
	bindings  map[int]map[string]Binding // a claimer's, by field, Output not set
	index     map[[2]string]int          // the instances CompileAll compiles, by namespace and release
}

// compile: the instance i, with what is published (its namespace's), its
// claims and its bindings, their outputs those published so far; into
// out, what it publishes published, removed when it fails.
func (t *tree) compile(i int) error {
	in := t.all[i].Inputs
	ns := in.Namespace
	if t.published[ns] == nil {
		t.published[ns] = map[string]Info{}
	}
	in.Published, in.Claims, in.Bindings = t.published[ns], t.claims[i], nil
	if bindings := t.bindings[i]; bindings != nil {
		in.Bindings = map[string]Binding{}
		for field, b := range bindings {
			// A provider refused publishes nothing a claim may use, whatever
			// it published before (refuseShared leaves it published).
			if p, ok := t.index[[2]string{b.Namespace, b.Release}]; ok && t.out[p].Result != nil {
				b.Output = t.output(b.Namespace, b.Release, b.Contract, b.ID)
			}
			in.Bindings[field] = b
		}
	}
	res, err := Compile(t.ctx, t.all[i].Definition, in)
	claims := t.out[i].Claims
	t.out[i] = Outcome{Result: res, Err: err, Claims: claims}
	if res != nil {
		t.published[ns][in.Release] = res.Info
	} else {
		delete(t.published[ns], in.Release)
	}
	return err
}

// output: the output of contract named name that release of namespace
// publishes; nil when none, or not compiled.
func (t *tree) output(namespace, release, contract, name string) *Output {
	for _, o := range t.published[namespace][release].Provides {
		if o.Contract == contract && o.Name == name {
			return &o
		}
	}
	return nil
}

// compileAll: the instances idx, Kahn's algorithm, its edges discovered
// by compiling:
//
//  1. Each instance is compiled with what the others compiled so far
//     publish. A success publishes its Info and is final; a compile that
//     lacks outputs no instance has published (missingOutputs), of its
//     namespace or a claim's of its provider's, waits for them; any other
//     failure is final.
//  2. An instance whose outputs are all published is compiled again, once:
//     final, whatever it gives.
//  3. Those still waiting are compiled again, everything published, for
//     the exact message.
func (t *tree) compileAll(idx []int) {
	waiting := map[int][]outputKey{}
	for _, i := range idx {
		if m, ok := errors.AsType[*missingOutputs](t.compile(i)); ok {
			waiting[i] = m.outputs
		}
	}
	for progress := true; progress; {
		progress = false
		for _, i := range idx {
			if keys, ok := waiting[i]; ok && t.publishes(t.all[i].Inputs.Namespace, keys) {
				delete(waiting, i)
				_ = t.compile(i)
				progress = true
			}
		}
	}
	for _, i := range idx {
		if _, ok := waiting[i]; ok {
			_ = t.compile(i)
		}
	}
}

// publishes reports whether every output of keys is published, a key of
// no namespace in namespace, a key of a release by that release.
func (t *tree) publishes(namespace string, keys []outputKey) bool {
	for _, k := range keys {
		found := false
		for release, info := range t.published[cmp.Or(k.namespace, namespace)] {
			found = found || (k.release == "" || k.release == release) && slices.ContainsFunc(info.Provides, func(o Output) bool { return o.Contract == k.contract && o.Name == k.name })
		}
		if !found {
			return false
		}
	}
	return true
}

// notes: the refusals of the instances idx for an output nobody published
// say which instances do not compile, what they publish unknown, in name
// order: those of the namespace, and the providers of its claims of
// another namespace.
func (t *tree) notes(idx []int) {
	refused := map[string][]string{}
	for i, o := range t.out {
		if in := t.all[i].Inputs; o.Result == nil {
			refused[in.Namespace] = append(refused[in.Namespace], in.Release)
		}
	}
	for _, i := range idx {
		if _, ok := errors.AsType[*missingOutputs](t.out[i].Err); !ok {
			continue
		}
		in := t.all[i].Inputs
		others := slices.DeleteFunc(slices.Clone(refused[in.Namespace]), func(r string) bool { return r == in.Release })
		for _, b := range t.bindings[i] {
			if b.Namespace != in.Namespace && slices.Contains(refused[b.Namespace], b.Release) {
				others = append(others, path.Join(b.Namespace, b.Release))
			}
		}
		slices.Sort(others)
		t.out[i].Err = notCompiled(t.out[i].Err, slices.Compact(others))
	}
}

// bind: the claims of the instances idx (claimsOf), each bound to the
// instance of idx that provisions its contract (provider): its provider
// gets it, deduplicated, by ID (claims), its claimer the binding
// (bindings, Outcome.Claims). A claim template that binds to none refuses
// its instance, which is not compiled, nor are its other claims given to
// their providers. The named claims of a namespace are those of its
// instances' contexts, declared alike in each, bound once, each given to
// its provider whether a reference names it or not: one declared
// differently in two contexts refuses every instance of the namespace,
// one that binds to none every instance whose context declares it. The
// instances not refused, in order.
func (t *tree) bind(idx []int) []int {
	provisioning := map[string][]int{} // the instances that provision a contract, by contract
	for _, i := range idx {
		for _, c := range t.all[i].Definition.Package.Provisions {
			provisioning[c] = append(provisioning[c], i)
		}
	}
	type bound struct {
		claimer int
		choice
		ref     claimRef
		id      string
		request M // ref's request, the class's merged under it, checked, the claim schema's defaults applied
	}
	type refused struct { // an error of a claimer, at the field it is about
		field string
		err   error
	}
	type declared struct {
		instance int
		spec     claimSpec
	}
	var claims []bound
	var toNamed []bound // the references to a named claim, its provider not known yet
	// The checks run in any order: the field orders the report, the named
	// claims of the context first, in name order, then the references in
	// parameter path order (claimsOf), as the connection errors are.
	errs := map[int][]refused{}
	rank := map[int]map[string]int{}            // an instance's references' fields, to their position in parameter path order
	decls := map[string]map[string][]declared{} // by namespace and name
	classes := map[int]M{}                      // an instance's, by contract
	for _, i := range idx {
		in := t.all[i].Inputs
		c := claimsOf(t.all[i].Definition, in)
		classes[i] = c.classes
		rank[i] = map[string]int{}
		for k, ref := range c.refs {
			rank[i][ref.field] = k
		}
		fields := map[string]string{} // the claim templates' IDs of the instance, to their field
		for name, spec := range c.named {
			if decls[in.Namespace] == nil {
				decls[in.Namespace] = map[string][]declared{}
			}
			decls[in.Namespace][name] = append(decls[in.Namespace][name], declared{i, spec})
		}
		for _, ref := range c.refs {
			if ref.name != "" {
				toNamed = append(toNamed, bound{claimer: i, ref: ref, id: claimID(in.Namespace, "", "", ref.name)})
				continue
			}
			id := claimID(in.Namespace, in.Release, ref.field, "")
			if first, dup := fields[id]; dup {
				errs[i] = append(errs[i], refused{ref.field, fmt.Errorf("%s: claim: it gives the same ID (%s) as %s: rename one of the parameters", ref.field, id, first)})
				continue
			}
			fields[id] = ref.field
			ch, err := t.provider(c.classes, in.Namespace, i, ref.field, ref.contract, ref.spec, provisioning[ref.contract])
			request, rerr := t.request(in, ref.field, ref.contract, ref.spec.Request, ch)
			if err = errors.Join(err, rerr); err != nil {
				errs[i] = append(errs[i], refused{ref.field, err})
				continue
			}
			claims = append(claims, bound{i, ch, ref, id, request})
		}
	}
	// A named claim: one declaration, bound with the context of the first
	// instance that declares it, in release order.
	release := func(i int) string { return t.all[i].Inputs.Release }
	namedBound := map[[2]string]bound{}
	for _, ns := range slices.Sorted(maps.Keys(decls)) {
		for _, name := range slices.Sorted(maps.Keys(decls[ns])) {
			ds := slices.SortedFunc(slices.Values(decls[ns][name]), func(a, b declared) int { return strings.Compare(release(a.instance), release(b.instance)) })
			where := value.Key("claims", name)
			if k := slices.IndexFunc(ds, func(d declared) bool { return !reflect.DeepEqual(d.spec, ds[0].spec) }); k > 0 {
				err := fmt.Errorf("%s: declared differently in the context of %s and %s", where, release(ds[0].instance), release(ds[k].instance))
				for _, i := range idx {
					if t.all[i].Inputs.Namespace == ns {
						errs[i] = append(errs[i], refused{where, err})
					}
				}
				continue
			}
			spec, in := ds[0].spec, t.all[ds[0].instance].Inputs
			ch, err := t.provider(classes[ds[0].instance], ns, -1, where, spec.Contract, spec, provisioning[spec.Contract])
			request, rerr := t.request(in, where, spec.Contract, spec.Request, ch)
			if err = errors.Join(err, rerr); err != nil {
				for _, d := range ds {
					errs[d.instance] = append(errs[d.instance], refused{where, err})
				}
				continue
			}
			namedBound[[2]string{ns, name}] = bound{choice: ch, ref: claimRef{where, spec.Contract, name, spec}, id: claimID(ns, "", "", name), request: request}
		}
	}
	for _, b := range toNamed {
		nb, ok := namedBound[[2]string{t.all[b.claimer].Inputs.Namespace, b.ref.name}]
		switch {
		case !ok: // refused above
		case nb.provider == b.claimer:
			errs[b.claimer] = append(errs[b.claimer], refused{b.ref.field, fmt.Errorf("%s: claim (%s): the instance provisions it itself: a claim is of another instance", b.ref.field, b.ref.contract)})
		default:
			b.choice, b.request = nb.choice, nb.request
			claims = append(claims, b)
		}
	}
	t.claims, t.bindings = map[int][]Claim{}, map[int]map[string]Binding{}
	give := func(provider int, c Claim) {
		if !slices.ContainsFunc(t.claims[provider], func(o Claim) bool { return o.ID == c.ID }) {
			t.claims[provider] = append(t.claims[provider], c)
		}
	}
	for k, b := range namedBound {
		give(b.provider, Claim{ID: b.id, Contract: b.ref.contract, Namespace: k[0], Name: k[1], Request: b.request})
	}
	for _, b := range claims {
		if errs[b.claimer] != nil {
			continue
		}
		in, p := t.all[b.claimer].Inputs, t.all[b.provider].Inputs
		if t.bindings[b.claimer] == nil {
			t.bindings[b.claimer] = map[string]Binding{}
		}
		t.bindings[b.claimer][b.ref.field] = Binding{Namespace: p.Namespace, Release: p.Release, Contract: b.ref.contract, ID: b.id}
		t.out[b.claimer].Claims = append(t.out[b.claimer].Claims, Bound{Field: b.ref.field, Contract: b.ref.contract, ID: b.id, Namespace: p.Namespace, Release: p.Release, Class: b.class, Named: b.named})
		give(b.provider, Claim{ID: b.id, Contract: b.ref.contract, Namespace: in.Namespace, Name: b.ref.name, Request: b.request})
	}
	for _, c := range t.claims {
		slices.SortFunc(c, func(a, b Claim) int { return strings.Compare(a.ID, b.ID) })
	}
	for _, i := range idx {
		slices.SortStableFunc(t.out[i].Claims, func(a, b Bound) int { return strings.Compare(a.Field, b.Field) })
	}
	var kept []int
	for _, i := range idx {
		if errs[i] != nil {
			at := func(r refused) int {
				k, ok := rank[i][r.field]
				if !ok {
					return -1
				}
				return k
			}
			slices.SortStableFunc(errs[i], func(a, b refused) int { return cmp.Or(cmp.Compare(at(a), at(b)), strings.Compare(a.field, b.field)) })
			joined := make([]error, len(errs[i]))
			for k, e := range errs[i] {
				joined[k] = e.err
			}
			t.out[i] = Outcome{Err: errors.Join(joined...)}
		} else {
			kept = append(kept, i)
		}
	}
	return kept
}

// request: what a claim at field of contract requests (req) of the
// provider of ch: what its class fixes, then req, which sets none of the
// class's keys, then checked against the contract's claim schema, read
// from the contracts of in, its defaults applied (contract.request). Every
// key the class fixes that req sets fails, in key order.
func (t *tree) request(in Inputs, field, contract string, req M, ch choice) (M, error) {
	var errs []error
	for _, k := range slices.Sorted(maps.Keys(req)) {
		if _, fixed := ch.fixed[k]; fixed {
			errs = append(errs, fmt.Errorf("%s: claim: %s: set by the class %s (%s): remove it, or name another class", field, value.Key("request", k), ch.class, value.Key(provisioningKey(contract)+".classes", ch.class)))
		}
	}
	if errs != nil {
		return nil, errors.Join(errs...)
	}
	c, err := readContract(in.Contracts, contract)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", field, err)
	}
	return c.request(value.Merge(value.AsMap(value.Copy(ch.fixed)), req), field)
}

// choice: the provider a claim is bound to (tree.provider): its index, the
// class it is the provider of ("" when the claim names it), whether the
// claim names its class or its provider (named), and what the class fixes
// of its request.
type choice struct {
	provider int
	class    string
	named    bool
	fixed    M
}

// provisioningKey: global.okdp.provisioning.<contract>, where the classes
// of a contract are.
func provisioningKey(contract string) string { return value.Key("global.okdp.provisioning", contract) }

// provider: the instance of candidates, those that provision contract, a
// claim (spec) at field, of the instance self (-1 for a named claim, the
// namespace's) of namespace, is bound to: the provider it names, else the
// provider of the class it names, else of the default class of the
// contract, of classes (global.okdp.provisioning of the context, by
// contract), each a release of its namespace or <namespace>/<release>. A
// class that is none, no default, a provider that is not of the form, or
// that is no candidate, or the instance itself, fail.
func (t *tree) provider(classes M, namespace string, self int, field, contract string, spec claimSpec, candidates []int) (choice, error) {
	where := fmt.Sprintf("%s: claim (%s)", field, contract)
	key := provisioningKey(contract)
	ch := choice{provider: -1, class: spec.Class, named: spec.Class != "" || spec.Provider != ""}
	name, which := spec.Provider, ""
	if spec.Provider == "" {
		of := value.AsMap(classes[contract])
		defined := value.AsMap(of["classes"])
		these := fmt.Sprintf("no class of %s connections is defined (%s.classes)", contract, key)
		if len(defined) > 0 {
			these = "the classes are " + strings.Join(slices.Sorted(maps.Keys(defined)), ", ")
		}
		which = fmt.Sprintf("class %q", ch.class)
		if ch.class == "" {
			if ch.class = value.Str(of["default"]); ch.class == "" {
				return ch, fmt.Errorf("%s: it names no class, and the context sets no default one, %s.default (Inputs.Context): %s", where, key, these)
			}
			which = fmt.Sprintf("the default class %q", ch.class)
		}
		class, ok := defined[ch.class].(M)
		if !ok {
			if len(defined) > 0 {
				these = strings.Join(slices.Sorted(maps.Keys(defined)), ", ")
			} else {
				these = "none is defined"
			}
			if spec.Class == "" {
				return ch, fmt.Errorf("%s: %s (%s.default) is not a class of %s connections (%s.classes): %s", where, which, key, contract, key, these)
			}
			return ch, fmt.Errorf("%s: %s is not a class of %s connections (%s.classes): %s", where, which, contract, key, these)
		}
		name, ch.fixed = value.Str(class["provider"]), value.AsMap(class["request"])
		if _, _, ok := providerName(name, namespace); !ok {
			return ch, fmt.Errorf("%s: %s (%s): provider %q: <release> or <namespace>/<release>", where, which, value.Key(key+".classes", ch.class), name)
		}
	}
	ns, release, _ := providerName(name, namespace)
	k := slices.IndexFunc(candidates, func(j int) bool {
		c := t.all[j].Inputs
		return c.Namespace == ns && c.Release == release
	})
	switch {
	case k < 0 && spec.Provider != "":
		return ch, fmt.Errorf("%s: %s/%s is no instance that provisions %s connections", where, ns, release, contract)
	case k < 0:
		return ch, fmt.Errorf("%s: %s: %s/%s is no instance that provisions %s connections", where, which, ns, release, contract)
	case candidates[k] == self:
		return ch, fmt.Errorf("%s: the instance provisions it itself: a claim is of another instance", where)
	}
	ch.provider = candidates[k]
	return ch, nil
}

// refuseShared: step 4 of CompileAll, for the instances idx of a
// namespace. The instances that publish an output of the same contract and
// name are refused, each: a reference to it must find one. They stay
// published. Then the instances whose references resolved through such an
// output, or through an output of an instance refused since, are compiled
// again, once each, until none more fails: each gets the error it would
// have got compiled after every provider, whatever the order.
func refuseShared(idx []int, out []Outcome, compile func(int) error) {
	providers := map[outputKey][]string{}
	for _, i := range idx {
		if res := out[i].Result; res != nil {
			// One output of a contract and name each: a compile refuses two.
			for _, o := range res.Info.Provides {
				k := outputKey{"", o.Contract, o.Name, ""}
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
			k := outputKey{"", o.Contract, o.Name, ""}
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
			out[i] = Outcome{Err: errors.Join(errs...), Claims: out[i].Claims}
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
				return d.Namespace == "" && (shared[outputKey{"", d.Contract, d.Output, ""}] || dropped[d.Instance])
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

// refuseClaimers: step 5 of CompileAll, for the instances idx. A provider
// refused by refuseShared stays published, so that a claimer of another
// namespace was compiled with an output of a provider that is refused: each
// successful instance with a dependency on a refused instance is compiled
// again, once, until none more fails. A claim's provider refused gives no
// output (tree.compile): the claimer then fails as it does when its
// provider does not compile.
func (t *tree) refuseClaimers(idx []int) {
	again := map[int]bool{}
	for more := true; more; {
		more = false
		for _, i := range idx {
			res := t.out[i].Result
			if res == nil || again[i] || !slices.ContainsFunc(res.Dependencies, func(d Dependency) bool {
				p, ok := t.index[[2]string{cmp.Or(d.Namespace, res.Namespace), d.Instance}]
				return ok && t.out[p].Result == nil
			}) {
				continue
			}
			again[i] = true
			if t.compile(i) != nil {
				more = true
			}
		}
	}
}

// notCompiled: err, the refusal of an instance for an output nobody
// published, with the other instances that do not compile, in name order:
// one of them may be its provider.
func notCompiled(err error, others []string) error {
	switch len(others) {
	case 0:
		return err
	case 1:
		return fmt.Errorf("%w (%s does not compile either: what it publishes is unknown)", err, others[0])
	}
	return fmt.Errorf("%w (%s do not compile either: what they publish is unknown)", err, strings.Join(others, ", "))
}
