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
// Name, Published, Claims and Bindings left out: CompileAll gives them; and
// its name, which a provider names it by, as by its release, and which
// names its outputs and its claims (Inputs.Name).
type Instance struct {
	Definition *Definition
	Inputs     Inputs
	Name       string // the instance's name: a claim's or a class's provider, <namespace>/<name> or <name> of the claimer's namespace, names it by its name or its release; the name of its outputs that name none and of its claims' IDs; LoadRepository's, its directory's; "": its release
}

// name: the name of the instance, its release when it has none.
func (i Instance) name() string { return cmp.Or(i.Name, i.Inputs.Release) }

// Claimers: the namespaces whose instances may claim connections of
// contract of the instance, a provider, from another namespace, by naming
// it or through a class of their project's (CompileAll; a class of the
// platform's, level 0, needs none): Inputs.Claimers, else the claimers of
// its package's provisions entry of contract; "*" any namespace; nil
// none.
func (i Instance) Claimers(contract string) []string {
	if i.Inputs.Claimers != nil || i.Definition == nil {
		return i.Inputs.Claimers
	}
	return i.Definition.Package.claimers(contract)
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
// claimer's context, global.okdp.provisioning (bind), one of another
// namespace with its consent (consent): the provider gets it
// (Inputs.Claims), its claimer the binding (Inputs.Bindings). Each instance
// is compiled after the instances of its namespace whose outputs it
// references and the providers of its claims, with what they publish
// (Inputs.Published, set by CompileAll). An output two instances of a
// namespace publish refuses both, and the instances whose references
// resolved to it, or whose claims are bound to one of them, of any namespace.
// Two objects that write one Secret of a namespace refuse the instances
// that hold them (refuseSecretTargets), and those that depend on them.
// An instance a connection of whose context, referenced or not, does not
// hold at its declaration is refused first (declaredConnections).
// Outcomes in the order of instances, which changes none
// of them. Whether they deploy together is the graph's (graph.New).
func CompileAll(ctx context.Context, instances []Instance) []Outcome {
	return CompileAllWith(ctx, instances, NewMemo())
}

// CompileAllWith: CompileAll, reading once what the memo holds: for a
// caller that compiles the same instances again (with a chart cache), the
// parameters of each, the contracts and the chart cache's charts, which the
// first run read. The memo of instances other than the same ones, or whose
// files or layers changed, gives their parameters of the first.
func CompileAllWith(ctx context.Context, instances []Instance, memo *Memo) []Outcome {
	if memo == nil {
		memo = NewMemo()
	}
	t := &compilation{ctx: ctx, all: instances, memo: memo, out: make([]Outcome, len(instances)), published: map[string]map[string]Info{}}
	count, names := map[[2]string]int{}, map[[2]string]int{}
	for _, inst := range instances {
		count[[2]string{inst.Inputs.Namespace, inst.Inputs.Release}]++
		names[[2]string{inst.Inputs.Namespace, inst.name()}]++
	}
	var idx []int
	for i, inst := range instances {
		switch in := t.inputs(i); {
		case count[[2]string{in.Namespace, in.Release}] > 1:
			t.out[i].Err = fmt.Errorf("release %q is given twice in namespace %q", in.Release, in.Namespace)
		case names[[2]string{in.Namespace, inst.name()}] > 1:
			// Its outputs and its claims are named after it.
			t.out[i].Err = fmt.Errorf("instance name %q is given twice in namespace %q", inst.name(), in.Namespace)
		default:
			// Every connection of its context, referenced or not.
			if err := declaredConnections(in); err != nil {
				t.out[i].Err = err
				continue
			}
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
	t.refuseSecretTargets(idx)
	t.refuseClaimers(idx)
	t.notes(idx)
	return t.out
}

// compilation: the instances CompileAll compiles, their outcomes, what those
// compiled publish, by namespace and release, and their claims, bound.
type compilation struct {
	ctx       context.Context
	all       []Instance
	memo      *Memo // what the compiles read once
	out       []Outcome
	published map[string]map[string]Info
	claims    map[int][]Claim            // a provider's
	bindings  map[int]map[string]Binding // a claimer's, by field, Output not set
	index     map[[2]string]int          // the instances CompileAll compiles, by namespace and release
	names     map[[2]string][]int        // the instances bind binds, by namespace and each name a provider may name them by: their release and their name
}

// inputs: the inputs of instance i, with its Memo.
func (t *compilation) inputs(i int) Inputs {
	in := t.all[i].Inputs
	if t.all[i].Definition != nil {
		in.memo = t.memo.forInstance(i, t.all[i].Definition, in)
	}
	return in
}

// compile: the instance i, with what is published (its namespace's), its
// claims and its bindings, their outputs those published so far; into
// out, what it publishes published, removed when it fails.
func (t *compilation) compile(i int) error {
	in := t.inputs(i)
	ns := in.Namespace
	if t.published[ns] == nil {
		t.published[ns] = map[string]Info{}
	}
	in.Name, in.Published, in.Claims, in.Bindings = t.all[i].name(), t.published[ns], t.claims[i], nil
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
func (t *compilation) output(namespace, release, contract, name string) *Output {
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
func (t *compilation) compileAll(idx []int) {
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
func (t *compilation) publishes(namespace string, keys []outputKey) bool {
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
func (t *compilation) notes(idx []int) {
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
// instance of idx that provisions its contract (provider), with its
// consent when it is of another namespace (consent): its provider
// gets it, deduplicated, by ID (claims), its claimer the binding
// (bindings, Outcome.Claims). A claim template that binds to none refuses
// its instance, which is not compiled, nor are its other claims given to
// their providers. The named claims of a namespace are those of its
// instances' contexts, declared alike in each, bound once, each given to
// its provider whether a reference names it or not: one declared
// differently in two contexts refuses every instance of the namespace,
// one that binds to none every instance whose context declares it. The
// instances not refused, in order.
func (t *compilation) bind(idx []int) []int {
	b := t.newBinder(idx)
	for _, i := range idx {
		b.instance(i)
	}
	namedBound := b.named(idx)
	b.attachNamed(namedBound)
	b.give(idx, namedBound)
	return b.outcomes(idx)
}

// boundClaim: a claim of a reference, bound to its provider (binder).
type boundClaim struct {
	claimer int
	choice
	ref     claimRef
	id      string
	request M // ref's request, the class's merged under it, checked, the claim schema's defaults applied
}

// claimError: an error of a claimer, at the field it is about.
type claimError struct {
	field string
	err   error
}

// declaredClaim: a named claim as the context of an instance declares it.
type declaredClaim struct {
	instance int
	spec     ClaimSpec
}

// binder: the state of compilation.bind, the claims of the instances idx found,
// bound or refused.
type binder struct {
	t            *compilation
	provisioning map[string][]int // the instances that provision a contract, by contract
	claims       []boundClaim
	toNamed      []boundClaim // the references to a named claim, its provider not known yet
	// The checks run in any order: the field orders the report, the named
	// claims of the context first, in name order, then the references in
	// parameter path order (claimsOf), as the connection errors are.
	errs     map[int][]claimError
	rank     map[int]map[string]int                // an instance's references' fields, to their position in parameter path order
	decls    map[string]map[string][]declaredClaim // by namespace and name
	classes  map[int]M                             // an instance's, by contract
	platform map[int]M                             // the platform's among them, by contract
}

// newBinder: the binder of the instances idx: what they provision, and the
// names each is known by (compilation.names).
func (t *compilation) newBinder(idx []int) *binder {
	b := &binder{t: t, provisioning: map[string][]int{}, errs: map[int][]claimError{}, rank: map[int]map[string]int{}, decls: map[string]map[string][]declaredClaim{}, classes: map[int]M{}, platform: map[int]M{}}
	t.names = map[[2]string][]int{}
	for _, i := range idx {
		for _, p := range t.all[i].Definition.Package.Provisions {
			b.provisioning[p.Contract] = append(b.provisioning[p.Contract], i)
		}
		in := t.all[i].Inputs
		for _, n := range slices.Compact([]string{in.Release, t.all[i].name()}) {
			t.names[[2]string{in.Namespace, n}] = append(t.names[[2]string{in.Namespace, n}], i)
		}
	}
	return b
}

// instance: the claims of instance i (claimsOf): its named claims to the
// declarations of its namespace, its references to a named claim kept for
// when the named claims are bound (toNamed), and each claim template
// bound to its provider (provider), with its consent (consent) and its
// request checked (compilation.request), or refused.
func (b *binder) instance(i int) {
	t := b.t
	in := t.inputs(i)
	c := claimsOf(t.all[i].Definition, in)
	b.classes[i], b.platform[i] = c.classes, c.platform
	b.rank[i] = map[string]int{}
	for k, ref := range c.refs {
		b.rank[i][ref.field] = k
	}
	fields := map[string]string{} // the claim templates' IDs of the instance, to their field
	for name, spec := range c.named {
		if b.decls[in.Namespace] == nil {
			b.decls[in.Namespace] = map[string][]declaredClaim{}
		}
		b.decls[in.Namespace][name] = append(b.decls[in.Namespace][name], declaredClaim{i, spec})
	}
	for _, ref := range c.refs {
		if ref.name != "" {
			b.toNamed = append(b.toNamed, boundClaim{claimer: i, ref: ref, id: claimID(in.Namespace, "", "", ref.name)})
			continue
		}
		id := claimID(in.Namespace, t.all[i].name(), ref.field, "")
		if first, dup := fields[id]; dup {
			b.errs[i] = append(b.errs[i], claimError{ref.field, fmt.Errorf("%s: claim: it gives the same ID (%s) as %s: rename one of the parameters", ref.field, id, first)})
			continue
		}
		fields[id] = ref.field
		ch, err := t.provider(c.classes, in.Namespace, i, ref.field, ref.contract, ref.spec, b.provisioning[ref.contract])
		if err == nil {
			err = t.consent(in.Namespace, ref.field, ref.contract, id, ref.spec, ch, c.platform)
		}
		request, rerr := t.request(in, ref.field, ref.contract, ref.spec.Request, ch)
		if err = errors.Join(err, rerr); err != nil {
			b.errs[i] = append(b.errs[i], claimError{ref.field, err})
			continue
		}
		b.claims = append(b.claims, boundClaim{i, ch, ref, id, request})
	}
}

// named: the named claims of the namespaces, each bound once, by
// namespace and name: one declaration, bound with the context of the first
// instance that declares it, in release order. One declared differently in
// two contexts refuses every instance of idx of its namespace, one that
// binds to none every instance whose context declares it.
func (b *binder) named(idx []int) map[[2]string]boundClaim {
	t := b.t
	release := func(i int) string { return t.all[i].Inputs.Release }
	namedBound := map[[2]string]boundClaim{}
	for _, ns := range slices.Sorted(maps.Keys(b.decls)) {
		for _, name := range slices.Sorted(maps.Keys(b.decls[ns])) {
			ds := slices.SortedFunc(slices.Values(b.decls[ns][name]), func(a, c declaredClaim) int { return strings.Compare(release(a.instance), release(c.instance)) })
			where := value.Key("claims", name)
			if k := slices.IndexFunc(ds, func(d declaredClaim) bool { return !reflect.DeepEqual(d.spec, ds[0].spec) }); k > 0 {
				err := fmt.Errorf("%s: declared differently in the context of %s and %s", where, release(ds[0].instance), release(ds[k].instance))
				for _, i := range idx {
					if t.all[i].Inputs.Namespace == ns {
						b.errs[i] = append(b.errs[i], claimError{where, err})
					}
				}
				continue
			}
			spec, in := ds[0].spec, t.inputs(ds[0].instance)
			ch, err := t.provider(b.classes[ds[0].instance], ns, -1, where, spec.Contract, spec, b.provisioning[spec.Contract])
			if err == nil {
				err = t.consent(ns, where, spec.Contract, claimID(ns, "", "", name), spec, ch, b.platform[ds[0].instance])
			}
			request, rerr := t.request(in, where, spec.Contract, spec.Request, ch)
			if err = errors.Join(err, rerr); err != nil {
				for _, d := range ds {
					b.errs[d.instance] = append(b.errs[d.instance], claimError{where, err})
				}
				continue
			}
			namedBound[[2]string{ns, name}] = boundClaim{choice: ch, ref: claimRef{where, spec.Contract, name, spec}, id: claimID(ns, "", "", name), request: request}
		}
	}
	return namedBound
}

// attachNamed: the references to a named claim bound to its provider, the
// one of namedBound; one whose claim is refused is refused with it, one
// whose provider is its own instance fails.
func (b *binder) attachNamed(namedBound map[[2]string]boundClaim) {
	for _, c := range b.toNamed {
		nb, ok := namedBound[[2]string{b.t.all[c.claimer].Inputs.Namespace, c.ref.name}]
		switch {
		case !ok: // refused above
		case nb.provider == c.claimer:
			b.errs[c.claimer] = append(b.errs[c.claimer], claimError{c.ref.field, fmt.Errorf("%s: claim (%s): the instance provisions it itself: a claim is of another instance", c.ref.field, c.ref.contract)})
		default:
			c.choice, c.request = nb.choice, nb.request
			b.claims = append(b.claims, c)
		}
	}
}

// give: the claims to their providers, deduplicated, by ID (compilation.claims):
// the named claims of namedBound, whether a reference names them or not,
// and each claim of an instance of idx not refused, with its binding
// (compilation.bindings) and the Bound of its outcome. A provider's claims in ID
// order, an outcome's in field order.
func (b *binder) give(idx []int, namedBound map[[2]string]boundClaim) {
	t := b.t
	t.claims, t.bindings = map[int][]Claim{}, map[int]map[string]Binding{}
	give := func(provider int, c Claim) {
		if !slices.ContainsFunc(t.claims[provider], func(o Claim) bool { return o.ID == c.ID }) {
			t.claims[provider] = append(t.claims[provider], c)
		}
	}
	for k, c := range namedBound {
		give(c.provider, Claim{ID: c.id, Contract: c.ref.contract, Namespace: k[0], Name: k[1], Request: c.request})
	}
	for _, c := range b.claims {
		if b.errs[c.claimer] != nil {
			continue
		}
		in, p := t.all[c.claimer].Inputs, t.all[c.provider].Inputs
		if t.bindings[c.claimer] == nil {
			t.bindings[c.claimer] = map[string]Binding{}
		}
		t.bindings[c.claimer][c.ref.field] = Binding{Namespace: p.Namespace, Release: p.Release, Contract: c.ref.contract, ID: c.id}
		t.out[c.claimer].Claims = append(t.out[c.claimer].Claims, Bound{Field: c.ref.field, Contract: c.ref.contract, ID: c.id, Namespace: p.Namespace, Release: p.Release, Class: c.class, Named: c.named})
		give(c.provider, Claim{ID: c.id, Contract: c.ref.contract, Namespace: in.Namespace, Name: c.ref.name, Request: c.request})
	}
	for _, claims := range t.claims {
		slices.SortFunc(claims, func(a, o Claim) int { return strings.Compare(a.ID, o.ID) })
	}
	for _, i := range idx {
		slices.SortStableFunc(t.out[i].Claims, func(a, o Bound) int { return strings.Compare(a.Field, o.Field) })
	}
}

// outcomes: the instances of idx not refused, in order; the others get
// the outcome of their errors, joined in the order of the parameter paths
// (rank), each at its field.
func (b *binder) outcomes(idx []int) []int {
	var kept []int
	for _, i := range idx {
		if b.errs[i] == nil {
			kept = append(kept, i)
			continue
		}
		at := func(r claimError) int {
			k, ok := b.rank[i][r.field]
			if !ok {
				return -1
			}
			return k
		}
		slices.SortStableFunc(b.errs[i], func(a, c claimError) int { return cmp.Or(cmp.Compare(at(a), at(c)), strings.Compare(a.field, c.field)) })
		joined := make([]error, len(b.errs[i]))
		for k, e := range b.errs[i] {
			joined[k] = atField(e.field, e.err)
		}
		b.t.out[i] = Outcome{Err: errors.Join(joined...)}
	}
	return kept
}

// request: what a claim at field of contract requests (req) of the
// provider of ch: what its class fixes, then req, which sets none of the
// class's keys, then checked against the contract's claim schema, read
// from the contracts of in, its defaults applied (contract.request). Every
// key the class fixes that req sets fails, in key order.
func (t *compilation) request(in Inputs, field, contract string, req M, ch choice) (M, error) {
	var errs []error
	for _, k := range slices.Sorted(maps.Keys(req)) {
		if _, fixed := ch.fixed[k]; fixed {
			errs = append(errs, fmt.Errorf("%s: claim: %s: set by the class %s (%s): remove it, or name another class", field, value.Key("request", k), ch.class, value.Key(provisioningKey(contract)+".classes", ch.class)))
		}
	}
	if errs != nil {
		return nil, errors.Join(errs...)
	}
	c, err := contractCacheOf(in).get(contract)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", field, err)
	}
	return c.request(value.Merge(value.AsMap(value.Copy(ch.fixed)), req), field)
}

// choice: the provider a claim is bound to (compilation.provider): its index, the
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

// claimClass: the class a claim at field of contract is of, of classes
// (by contract, global.okdp.provisioning of its context), and the
// provider it names: the claim's own when it names one, no class then;
// else the class it names, or the default one of the context, which must
// be a class whose provider is of the form providerName reads, what the
// class fixes of the request in the choice (its provider not looked for:
// compilation.provider); and the class as messages name it (which, "" for a
// provider named).
func claimClass(classes M, namespace, field, contract string, spec ClaimSpec) (choice, string, string, error) {
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
				return ch, "", "", fmt.Errorf("%s: it names no class, and the context sets no default one, %s.default (Inputs.Context): %s", where, key, these)
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
				return ch, "", "", fmt.Errorf("%s: %s (%s.default) is not a class of %s connections (%s.classes): %s", where, which, key, contract, key, these)
			}
			return ch, "", "", fmt.Errorf("%s: %s is not a class of %s connections (%s.classes): %s", where, which, contract, key, these)
		}
		name, ch.fixed = value.Str(class["provider"]), value.AsMap(class["request"])
		if _, _, ok := providerName(name, namespace); !ok {
			return ch, "", "", fmt.Errorf("%s: %s (%s): provider %q: %s", where, which, value.Key(key+".classes", ch.class), name, providerForms)
		}
	}
	return ch, name, which, nil
}

// provider: the instance of candidates, those that provision contract, a
// claim (spec) at field, of the instance self (-1 for a named claim, the
// namespace's) of namespace, is bound to: the provider it names, else the
// provider of the class it names, else of the default class of the
// contract, of classes (global.okdp.provisioning of the context, by
// contract), each <name> of its namespace or <namespace>/<name>, the
// instance of that namespace whose name or release it is (names). A class
// that is none, no default, a provider that is not of the form, that
// names no instance, or more than one, or one that is no candidate, or the
// instance itself, fail (claimClass, then the provider).
func (t *compilation) provider(classes M, namespace string, self int, field, contract string, spec ClaimSpec, candidates []int) (choice, error) {
	where := fmt.Sprintf("%s: claim (%s)", field, contract)
	ch, name, which, err := claimClass(classes, namespace, field, contract, spec)
	if err != nil {
		return ch, err
	}
	// The provider as named, after the class it is of.
	named := where
	if which != "" {
		named += ": " + which
	}
	ns, n, _ := providerName(name, namespace)
	matches, k := t.names[[2]string{ns, n}], -1
	if len(matches) == 1 {
		k = slices.Index(candidates, matches[0])
	}
	switch {
	case len(matches) > 1:
		return ch, fmt.Errorf("%s: %s/%s names more than one instance, %s: the claim would not know which", named, ns, n, t.matched(n, matches))
	case k < 0:
		return ch, fmt.Errorf("%s: %s/%s is no instance that provisions %s connections", named, ns, n, contract)
	case candidates[k] == self:
		return ch, fmt.Errorf("%s: the instance provisions it itself: a claim is of another instance", where)
	}
	ch.provider = candidates[k]
	return ch, nil
}

// consent: nil when the claim id of contract, of the instances of
// namespace, at field, may be bound to the provider of ch
// (compilation.provider): an instance of namespace; or, of another namespace,
// the provider of a class of the platform's (platform, the classes of
// level 0 by contract, which no level above changes: the platform routes
// every namespace to it), or one whose claimers (Instance.Claimers) list
// namespace, or "*". Else the error, naming the claim, the provider, the
// class when the claim is bound through one, the claimers' namespace and
// the provider's claimers, its instance's or its package's.
func (t *compilation) consent(namespace, field, contract, id string, spec ClaimSpec, ch choice, platform M) error {
	p := t.all[ch.provider]
	if p.Inputs.Namespace == namespace {
		return nil
	}
	by := ""
	if spec.Provider == "" {
		if _, ok := value.AsMap(value.AsMap(platform[contract])["classes"])[ch.class]; ok {
			return nil
		}
		by = fmt.Sprintf(", the provider of class %q", ch.class)
		if spec.Class == "" {
			by = fmt.Sprintf(", the provider of the default class %q", ch.class)
		}
	}
	claimers := p.Claimers(contract)
	if consents(claimers, namespace) {
		return nil
	}
	provider := path.Join(p.Inputs.Namespace, p.Inputs.Release)
	list := cmp.Or(strings.Join(claimers, ", "), "none")
	if p.Inputs.Claimers == nil {
		list += ", its package's"
	}
	return fmt.Errorf("%s: claim %q (%s): of %s%s, another namespace: %s is not one of its claimers (claimers of instance %s: %s): the provider lists the namespaces that may claim of it (claimers of its instance.yaml, else of its package's provisions), or the platform routes them through a class of platform.yaml", field, id, contract, provider, by, namespace, provider, list)
}

// matched: the instances matches, that the provider name n names, in
// release order, each its release and how n names it: "<release> (its
// release)", "<release> (its name)", the last after "and".
func (t *compilation) matched(n string, matches []int) string {
	var each []string
	for _, j := range matches {
		by := "its name"
		if t.all[j].Inputs.Release == n {
			by = "its release"
		}
		each = append(each, fmt.Sprintf("%s (%s)", t.all[j].Inputs.Release, by))
	}
	slices.Sort(each)
	last := len(each) - 1
	return strings.Join(each[:last], ", ") + " and " + each[last]
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

// secretTarget: what an object of OKDP writes that another object of its
// namespace must not: a Secret (kind "Secret", or "push" a key of the
// secret store), of the namespace ns, named name.
type secretTarget struct{ ns, kind, name string }

// secretTargets: what the object o of an instance of namespace ns writes:
// a v1 Secret its own name; an ExternalSecret the Secret of
// spec.target.name, its own name when unset, unless its creationPolicy
// is None; a PushSecret each key (remoteKey, and its property) of the
// store it pushes.
func secretTargets(ns string, o M) []secretTarget {
	ns = cmp.Or(value.Str(value.Dig(o, "metadata.namespace")), ns)
	name := value.Str(value.Dig(o, "metadata.name"))
	group := apiGroup(value.Str(o["apiVersion"]))
	switch kind := value.Str(o["kind"]); {
	case group == "" && kind == "Secret":
		return []secretTarget{{ns, "Secret", name}}
	case group == "external-secrets.io" && kind == "ExternalSecret":
		if value.Str(value.Dig(o, "spec.target.creationPolicy")) == "None" {
			return nil
		}
		return []secretTarget{{ns, "Secret", cmp.Or(value.Str(value.Dig(o, "spec.target.name")), name)}}
	case group == "external-secrets.io" && kind == "PushSecret":
		var out []secretTarget
		for _, d := range value.AsList(value.Dig(o, "spec.data")) {
			ref := value.AsMap(value.Dig(value.AsMap(d), "match.remoteRef"))
			key := value.Str(ref["remoteKey"])
			if p := value.Str(ref["property"]); p != "" {
				key += " (property " + p + ")"
			}
			out = append(out, secretTarget{ns, "push", key})
		}
		return out
	}
	return nil
}

// refuseSecretTargets: step 5 of CompileAll, for the instances idx. Two
// objects that write one Secret of a namespace (secretTargets), of one
// instance or of two, an item of a List among them (eachObject), overwrite each other's credentials: each instance
// that holds one is refused, its error naming the objects and the
// instances, and no longer published: refuseClaimers then compiles again
// those that reference its outputs, which fail. A provider's
// secretRef.name, which names its claimers' copies (<release>-<name>),
// could otherwise forge the Secret of a connection of another instance
// (<release>.<connection>-credentials).
func (t *compilation) refuseSecretTargets(idx []int) {
	out := t.out
	type writer struct {
		release, object string
	}
	writers := map[secretTarget][]writer{}
	for _, i := range idx {
		res := out[i].Result
		if res == nil {
			continue
		}
		_ = eachObject(res.Objects, func(o M) error {
			for _, x := range secretTargets(res.Namespace, o) {
				w := writer{res.Release, value.Str(o["kind"]) + " " + value.Str(value.Dig(o, "metadata.name"))}
				writers[x] = append(writers[x], w)
			}
			return nil
		})
	}
	for _, i := range idx {
		res := out[i].Result
		if res == nil {
			continue
		}
		var errs []error
		seen := map[secretTarget]bool{}
		_ = eachObject(res.Objects, func(o M) error {
			for _, x := range secretTargets(res.Namespace, o) {
				if len(writers[x]) < 2 || seen[x] {
					continue
				}
				seen[x] = true
				count := map[string]int{}
				for _, w := range writers[x] {
					count[fmt.Sprintf("%s of %s", w.object, w.release)]++
				}
				var each []string
				for _, w := range slices.Sorted(maps.Keys(count)) {
					if n := count[w]; n > 1 {
						w += fmt.Sprintf(" (%d times)", n)
					}
					each = append(each, w)
				}
				what := fmt.Sprintf("Secret %q", x.name)
				if x.kind == "push" {
					what = fmt.Sprintf("the key %s of the secret store", x.name)
				}
				errs = append(errs, fmt.Errorf("%s of namespace %s is written by %s: two objects may not write one Secret", what, x.ns, strings.Join(each, " and ")))
			}
			return nil
		})
		if errs != nil {
			out[i] = Outcome{Err: errors.Join(errs...), Claims: out[i].Claims}
			delete(t.published[res.Namespace], res.Release)
		}
	}
}

// refuseClaimers: step 6 of CompileAll, for the instances idx. A provider
// refused by refuseShared stays published, so that a claimer of another
// namespace was compiled with an output of a provider that is refused: each
// successful instance with a dependency on a refused instance is compiled
// again, once, until none more fails. A claim's provider refused gives no
// output (compilation.compile): the claimer then fails as it does when its
// provider does not compile.
func (t *compilation) refuseClaimers(idx []int) {
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
