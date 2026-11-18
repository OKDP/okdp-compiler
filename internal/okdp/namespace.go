// Namespace: instances compiled together (CompileAll), each after the
// instances it references and the providers of its claims.

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

	"github.com/okdp/okdp-compiler/value"
)

// Instance is one instance CompileAll compiles: its package and its
// inputs, of which CompileAll sets Name, Published, Claims and Bindings.
type Instance struct {
	Definition *Definition
	Inputs     Inputs
	// Name is the instance's name. A claim or a class names its provider by
	// this name or by its release. LoadRepository uses the instance's
	// directory name. Empty means the release.
	Name string
}

func (i Instance) name() string { return cmp.Or(i.Name, i.Inputs.Release) }

// Claimers returns the namespaces that may claim connections of contract
// from this provider across namespaces: Inputs.Claimers, else those of the
// package's provisions entry. "*" means any namespace, nil none.
func (i Instance) Claimers(contract string) []string {
	if i.Inputs.Claimers != nil || i.Definition == nil {
		return i.Inputs.Claimers
	}
	return i.Definition.Package.claimers(contract)
}

// Outcome is the compile of one instance by CompileAll. Result is nil when
// it is refused; Claims are its claims, bound to their providers.
type Outcome struct {
	Result *Result
	Err    error
	Claims []Bound
}

// Bound is a claim of a reference, bound to its provider by CompileAll.
type Bound struct {
	Field              string // the parameter path of the reference
	Contract           string // the contract of the reference
	ID                 string // the claim's ID: the name of the output its provider publishes for it
	Namespace, Release string // the provider's instance
	Class              string // the class whose provider was chosen; "" when the claim names its provider
	// Named says the claim names its class or its provider. Otherwise the
	// class is the default of the claimer's context.
	Named bool
}

// CompileAll compiles instances together and returns their outcomes in the
// same order, which changes none of them. See CompileAllWith for the steps.
func CompileAll(ctx context.Context, instances []Instance) []Outcome {
	return CompileAllWith(ctx, instances, NewMemo())
}

// CompileAllWith is CompileAll with a memo, for a caller that compiles the
// same instances again: the second run reuses the parameters and the
// contracts the first one read. The memo is only valid for the same
// instances with unchanged files and layers.
//
// The steps:
//
//  1. An instance is refused when its release or name is given twice in its
//     namespace, or when its context declares a connection that does not hold.
//  2. Each claim is bound to the instance that provisions its contract
//     (bind). The provider receives the claim and the claimer the binding.
//  3. Each instance is compiled after the instances it references and the
//     providers of its claims, with what they publish (compileAll).
//  4. Two instances of a namespace that publish the same output are both
//     refused, with the instances that referenced it (refuseShared).
//  5. Instances whose objects write the same Secret of a namespace are
//     refused (refuseSecretTargets).
//  6. Instances that depend on an instance refused since they compiled
//     are compiled again (refuseClaimers).
func CompileAllWith(ctx context.Context, instances []Instance, memo *Memo) []Outcome {
	if memo == nil {
		memo = NewMemo()
	}
	t := &compilation{ctx: ctx, memo: memo, out: make([]Outcome, len(instances)), published: map[string]map[string]Info{}}
	releases, names := map[instKey]int{}, map[instKey]int{}
	for i, inst := range instances {
		m := &member{Instance: inst, index: i, outcome: &t.out[i]}
		t.members = append(t.members, m)
		releases[m.key()]++
		names[instKey{inst.Inputs.Namespace, inst.name()}]++
	}
	var ms []*member
	for _, m := range t.members {
		in := t.inputs(m)
		switch {
		case releases[m.key()] > 1:
			m.outcome.Err = fmt.Errorf("release %q is given twice in namespace %q", in.Release, in.Namespace)
		case names[instKey{in.Namespace, m.name()}] > 1:
			// Its outputs and its claims are named after it.
			m.outcome.Err = fmt.Errorf("instance name %q is given twice in namespace %q", m.name(), in.Namespace)
		default:
			if err := declaredConnections(ctx, in); err != nil {
				m.outcome.Err = err
				continue
			}
			ms = append(ms, m)
		}
	}
	ms = t.bind(ms)
	t.byRelease = map[instKey]*member{}
	for _, m := range ms {
		t.byRelease[m.key()] = m
	}
	t.compileAll(ms)
	byNamespace := map[string][]*member{}
	for _, m := range ms {
		ns := m.Inputs.Namespace
		byNamespace[ns] = append(byNamespace[ns], m)
	}
	for _, ns := range slices.Sorted(maps.Keys(byNamespace)) {
		t.refuseShared(byNamespace[ns])
	}
	t.refuseSecretTargets(ms)
	t.refuseClaimers(ms)
	t.notes(ms)
	return t.out
}

// instKey identifies an instance by its namespace and its release or name.
type instKey struct{ namespace, name string }

// member is one instance of a compilation.
type member struct {
	Instance
	index   int      // its position in the instances, and its slot in the Memo
	outcome *Outcome // its entry in compilation.out

	claims   []Claim            // as a provider: the claims bound to it, in ID order
	bindings map[string]Binding // as a claimer: the provider of each claim, by parameter path; Output is set at each compile

	// What bind reads and records for the instance.
	classes  M              // the provisioning classes of its context, by contract
	platform M              // those of them the platform defines (level 0)
	rank     map[string]int // the position of each of its references, in parameter path order
	errs     []claimError   // why its claims do not bind
}

func (m *member) key() instKey { return instKey{m.Inputs.Namespace, m.Inputs.Release} }

// compilation is the state of one CompileAll.
type compilation struct {
	ctx       context.Context
	memo      *Memo
	members   []*member // every instance, in order
	out       []Outcome
	published map[string]map[string]Info // what the compiled instances publish, by namespace and release
	byRelease map[instKey]*member        // the instances that reach the compile, by namespace and release
	byName    map[instKey][]*member      // the same, by namespace and by each name a provider may use: release and name
}

func (t *compilation) inputs(m *member) Inputs {
	in := m.Inputs
	if m.Definition != nil {
		in.memo = t.memo.forInstance(m.index, m.Definition, in)
	}
	return in
}

// compile compiles m with what its namespace publishes so far, its claims
// and its bindings. It records the outcome, and publishes the result or
// withdraws what m published before when it fails.
func (t *compilation) compile(m *member) error {
	in := t.inputs(m)
	ns := in.Namespace
	if t.published[ns] == nil {
		t.published[ns] = map[string]Info{}
	}
	in.Name, in.Published, in.Claims, in.Bindings = m.name(), t.published[ns], m.claims, nil
	if m.bindings != nil {
		in.Bindings = map[string]Binding{}
		for field, b := range m.bindings {
			// A refused provider publishes nothing a claim may use, even
			// though refuseShared leaves its earlier outputs published.
			if p, ok := t.byRelease[instKey{b.Namespace, b.Release}]; ok && p.outcome.Result != nil {
				b.Output = t.output(b.Namespace, b.Release, b.Contract, b.ID)
			}
			in.Bindings[field] = b
		}
	}
	res, err := Compile(t.ctx, m.Definition, in)
	*m.outcome = Outcome{Result: res, Err: err, Claims: m.outcome.Claims}
	if res != nil {
		t.published[ns][in.Release] = res.Info
	} else {
		delete(t.published[ns], in.Release)
	}
	return err
}

// output returns the output release publishes, or nil when it has none.
func (t *compilation) output(namespace, release, contract, name string) *Output {
	for _, o := range t.published[namespace][release].Provides {
		if o.Contract == contract && o.Name == name {
			return &o
		}
	}
	return nil
}

// compileAll compiles ms in dependency order. The dependencies come from
// the mappings, so they are discovered by compiling (Kahn's algorithm):
//
//  1. Each instance is compiled with what the others have published so
//     far. A compile that only lacks outputs nobody has published yet
//     (missingOutputs) waits for them. Any other result is final.
//  2. A waiting instance is compiled again, once, as soon as all the
//     outputs it lacks are published. That result is final.
//  3. The instances still waiting are compiled a last time with
//     everything published, to get the exact error message.
func (t *compilation) compileAll(ms []*member) {
	waiting := map[*member][]outputKey{}
	for _, m := range ms {
		if missing, ok := errors.AsType[*missingOutputs](t.compile(m)); ok {
			waiting[m] = missing.outputs
		}
	}
	for progress := true; progress; {
		progress = false
		for _, m := range ms {
			if keys, ok := waiting[m]; ok && t.publishes(m.Inputs.Namespace, keys) {
				delete(waiting, m)
				_ = t.compile(m)
				progress = true
			}
		}
	}
	for _, m := range ms {
		if _, ok := waiting[m]; ok {
			_ = t.compile(m)
		}
	}
}

// publishes reports whether every output of keys is published. A key
// without namespace is looked up in namespace.
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

// recompile compiles again, once each, the compiled instances of ms that
// have a dependency stale reports, until no more of them fails. failed, if
// not nil, is called with the previous result of each one that fails.
func (t *compilation) recompile(ms []*member, stale func(res *Result, d Dependency) bool, failed func(res *Result)) {
	again := map[*member]bool{}
	for more := true; more; {
		more = false
		for _, m := range ms {
			res := m.outcome.Result
			if res == nil || again[m] || !slices.ContainsFunc(res.Dependencies, func(d Dependency) bool { return stale(res, d) }) {
				continue
			}
			again[m] = true
			if t.compile(m) != nil {
				more = true
				if failed != nil {
					failed(res)
				}
			}
		}
	}
}

// notes completes the error of each instance of ms refused for an output
// nobody published: it lists the instances that do not compile, because
// one of them may be the provider.
func (t *compilation) notes(ms []*member) {
	refused := map[string][]string{}
	for _, m := range t.members {
		if in := m.Inputs; m.outcome.Result == nil {
			refused[in.Namespace] = append(refused[in.Namespace], in.Release)
		}
	}
	for _, m := range ms {
		if _, ok := errors.AsType[*missingOutputs](m.outcome.Err); !ok {
			continue
		}
		in := m.Inputs
		others := slices.DeleteFunc(slices.Clone(refused[in.Namespace]), func(r string) bool { return r == in.Release })
		for _, b := range m.bindings {
			if b.Namespace != in.Namespace && slices.Contains(refused[b.Namespace], b.Release) {
				others = append(others, path.Join(b.Namespace, b.Release))
			}
		}
		slices.Sort(others)
		m.outcome.Err = notCompiled(m.outcome.Err, slices.Compact(others))
	}
}

// notCompiled adds to err the other instances that do not compile.
func notCompiled(err error, others []string) error {
	switch len(others) {
	case 0:
		return err
	case 1:
		return fmt.Errorf("%w (%s does not compile either: what it publishes is unknown)", err, others[0])
	}
	return fmt.Errorf("%w (%s do not compile either: what they publish is unknown)", err, strings.Join(others, ", "))
}

// bind binds the claims of ms to their providers and returns the instances
// that are not refused, in order. A claim template that does not bind
// refuses its instance.
//
// The named claims of a namespace are those its instances' contexts
// declare. Each is bound once and given to its provider whether a
// reference uses it or not. One declared differently in two contexts
// refuses every instance of the namespace; one that does not bind, every
// instance whose context declares it.
func (t *compilation) bind(ms []*member) []*member {
	b := t.newBinder(ms)
	for _, m := range ms {
		b.instance(m)
	}
	named := b.named(ms)
	b.attachNamed(named)
	b.give(ms, named)
	return b.outcomes(ms)
}

// claimSite is where a claim is declared, for binding and for errors.
type claimSite struct {
	namespace string    // the namespace of the claimers
	field     string    // the parameter path of the reference, or claims.<name> for a named claim
	contract  string    // the contract claimed
	spec      ClaimSpec // the claim as written
}

func (s claimSite) where() string { return fmt.Sprintf("%s: claim (%s)", s.field, s.contract) }

// choice is the provider a claim is bound to.
type choice struct {
	provider *member // nil until a provider is found
	class    string  // the class it is the provider of; "" when the claim names the provider
	named    bool    // the claim names its class or its provider
	fixed    M       // what the class fixes of the claim's request
}

// boundClaim is a claim of a reference, bound to its provider.
type boundClaim struct {
	claimer *member // nil for a named claim, which belongs to its namespace
	choice
	ref     claimRef
	id      string
	request M // the class's fixed keys, then the claim's own, checked, with the claim schema's defaults
}

type claimError struct {
	field string
	err   error
}

// declaredClaim is a named claim as one instance's context declares it.
type declaredClaim struct {
	by   *member
	spec ClaimSpec
}

// binder is the state of compilation.bind.
type binder struct {
	t            *compilation
	provisioning map[string][]*member                  // the instances that provision each contract
	claims       []boundClaim                          // the claims bound so far
	toNamed      []boundClaim                          // the references to a named claim, waiting for its provider
	decls        map[string]map[string][]declaredClaim // the named claims declared, by namespace and name
}

// newBinder indexes ms by the contracts they provision and by the names a
// provider may be called by (compilation.byName).
func (t *compilation) newBinder(ms []*member) *binder {
	b := &binder{t: t, provisioning: map[string][]*member{}, decls: map[string]map[string][]declaredClaim{}}
	t.byName = map[instKey][]*member{}
	for _, m := range ms {
		for _, p := range m.Definition.Package.Provisions {
			b.provisioning[p.Contract] = append(b.provisioning[p.Contract], m)
		}
		for _, n := range slices.Compact([]string{m.Inputs.Release, m.name()}) {
			k := instKey{m.Inputs.Namespace, n}
			t.byName[k] = append(t.byName[k], m)
		}
	}
	return b
}

// bindOne binds the claim at s: it picks the provider, checks its consent
// and the request. self is the claiming instance, nil for a named claim.
func (b *binder) bindOne(in Inputs, of *member, s claimSite, id string, self *member) (choice, M, error) {
	ch, err := b.t.provider(of.classes, s, self, b.provisioning[s.contract])
	if err == nil {
		err = consent(s, id, ch, of.platform)
	}
	request, rerr := claimRequest(in, s, ch)
	return ch, request, errors.Join(err, rerr)
}

// instance reads the claims of m: it binds its claim templates, and leaves
// its references to a named claim waiting until those are bound (toNamed).
func (b *binder) instance(m *member) {
	t := b.t
	in := t.inputs(m)
	c := claimsOf(t.ctx, m.Definition, in)
	m.classes, m.platform = c.classes, c.platform
	// The checks below run in any order, so the rank orders the report as
	// the connection errors are ordered: by parameter path (outcomes).
	m.rank = map[string]int{}
	for k, ref := range c.refs {
		m.rank[ref.field] = k
	}
	for name, spec := range c.named {
		if b.decls[in.Namespace] == nil {
			b.decls[in.Namespace] = map[string][]declaredClaim{}
		}
		b.decls[in.Namespace][name] = append(b.decls[in.Namespace][name], declaredClaim{m, spec})
	}
	fields := map[string]string{} // the field of each claim template of m, by ID
	for _, ref := range c.refs {
		if ref.name != "" {
			b.toNamed = append(b.toNamed, boundClaim{claimer: m, ref: ref, id: namedClaimID(in.Namespace, ref.name)})
			continue
		}
		id := templateClaimID(in.Namespace, m.name(), ref.field)
		if first, dup := fields[id]; dup {
			m.errs = append(m.errs, claimError{ref.field, fmt.Errorf("%s: claim: it gives the same ID (%s) as %s: rename one of the parameters", ref.field, id, first)})
			continue
		}
		fields[id] = ref.field
		ch, request, err := b.bindOne(in, m, claimSite{in.Namespace, ref.field, ref.contract, ref.spec}, id, m)
		if err != nil {
			m.errs = append(m.errs, claimError{ref.field, err})
			continue
		}
		b.claims = append(b.claims, boundClaim{claimer: m, choice: ch, ref: ref, id: id, request: request})
	}
}

// named binds each named claim once, by namespace and name, in the context
// of the first instance that declares it, in release order.
func (b *binder) named(ms []*member) map[instKey]boundClaim {
	release := func(d declaredClaim) string { return d.by.Inputs.Release }
	bound := map[instKey]boundClaim{}
	for _, ns := range slices.Sorted(maps.Keys(b.decls)) {
		for _, name := range slices.Sorted(maps.Keys(b.decls[ns])) {
			ds := slices.SortedFunc(slices.Values(b.decls[ns][name]), func(a, c declaredClaim) int { return strings.Compare(release(a), release(c)) })
			where := value.Key("claims", name)
			if k := slices.IndexFunc(ds, func(d declaredClaim) bool { return !reflect.DeepEqual(d.spec, ds[0].spec) }); k > 0 {
				err := fmt.Errorf("%s: declared differently in the context of %s and %s", where, release(ds[0]), release(ds[k]))
				for _, m := range ms {
					if m.Inputs.Namespace == ns {
						m.errs = append(m.errs, claimError{where, err})
					}
				}
				continue
			}
			first, spec, id := ds[0].by, ds[0].spec, namedClaimID(ns, name)
			ch, request, err := b.bindOne(b.t.inputs(first), first, claimSite{ns, where, spec.Contract, spec}, id, nil)
			if err != nil {
				for _, d := range ds {
					d.by.errs = append(d.by.errs, claimError{where, err})
				}
				continue
			}
			bound[instKey{ns, name}] = boundClaim{choice: ch, ref: claimRef{where, spec.Contract, name, spec}, id: id, request: request}
		}
	}
	return bound
}

// attachNamed gives each reference to a named claim the provider of that
// claim. A reference whose provider is its own instance fails.
func (b *binder) attachNamed(named map[instKey]boundClaim) {
	for _, c := range b.toNamed {
		nb, ok := named[instKey{c.claimer.Inputs.Namespace, c.ref.name}]
		switch {
		case !ok: // refused by named
		case nb.provider == c.claimer:
			c.claimer.errs = append(c.claimer.errs, claimError{c.ref.field, fmt.Errorf("%s: claim (%s): the instance provisions it itself: a claim is of another instance", c.ref.field, c.ref.contract)})
		default:
			c.choice, c.request = nb.choice, nb.request
			b.claims = append(b.claims, c)
		}
	}
}

// give hands the claims to their providers, once per ID: the named claims
// and those of every instance that is not refused, which gets its
// bindings. A provider's claims end in ID order, an outcome's in field
// order.
func (b *binder) give(ms []*member, named map[instKey]boundClaim) {
	give := func(provider *member, c Claim) {
		if !slices.ContainsFunc(provider.claims, func(o Claim) bool { return o.ID == c.ID }) {
			provider.claims = append(provider.claims, c)
		}
	}
	for k, c := range named {
		give(c.provider, Claim{ID: c.id, Contract: c.ref.contract, Namespace: k.namespace, Name: k.name, Request: c.request})
	}
	for _, c := range b.claims {
		m := c.claimer
		if m.errs != nil {
			continue
		}
		p := c.provider.Inputs
		if m.bindings == nil {
			m.bindings = map[string]Binding{}
		}
		m.bindings[c.ref.field] = Binding{Namespace: p.Namespace, Release: p.Release, Contract: c.ref.contract, ID: c.id}
		m.outcome.Claims = append(m.outcome.Claims, Bound{
			Field:     c.ref.field,
			Contract:  c.ref.contract,
			ID:        c.id,
			Namespace: p.Namespace,
			Release:   p.Release,
			Class:     c.class,
			Named:     c.named,
		})
		give(c.provider, Claim{ID: c.id, Contract: c.ref.contract, Namespace: m.Inputs.Namespace, Name: c.ref.name, Request: c.request})
	}
	for _, m := range b.t.members {
		slices.SortFunc(m.claims, func(a, o Claim) int { return strings.Compare(a.ID, o.ID) })
	}
	for _, m := range ms {
		slices.SortStableFunc(m.outcome.Claims, func(a, o Bound) int { return strings.Compare(a.Field, o.Field) })
	}
}

// outcomes returns the instances of ms that are not refused, in order.
// Each of the others gets its errors as its outcome, in parameter path
// order.
func (b *binder) outcomes(ms []*member) []*member {
	var kept []*member
	for _, m := range ms {
		if m.errs == nil {
			kept = append(kept, m)
			continue
		}
		at := func(r claimError) int {
			k, ok := m.rank[r.field]
			if !ok {
				return -1
			}
			return k
		}
		slices.SortStableFunc(m.errs, func(a, c claimError) int { return cmp.Or(cmp.Compare(at(a), at(c)), strings.Compare(a.field, c.field)) })
		joined := make([]error, len(m.errs))
		for k, e := range m.errs {
			joined[k] = atField(e.field, e.err)
		}
		*m.outcome = Outcome{Err: errors.Join(joined...)}
	}
	return kept
}

// claimRequest returns what the claim at s requests of the provider of
// ch: the keys its class fixes, then the claim's own, which may set none
// of them, checked against the contract's claim schema.
func claimRequest(in Inputs, s claimSite, ch choice) (M, error) {
	req := s.spec.Request
	var errs []error
	for _, k := range slices.Sorted(maps.Keys(req)) {
		if _, fixed := ch.fixed[k]; fixed {
			errs = append(errs, fmt.Errorf("%s: claim: %s: set by the class %s (%s): remove it, or name another class", s.field, value.Key("request", k), ch.class, value.Key(provisioningKey(s.contract)+".classes", ch.class)))
		}
	}
	if errs != nil {
		return nil, errors.Join(errs...)
	}
	c, err := contractCacheOf(in).get(s.contract)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", s.field, err)
	}
	return c.request(value.Merge(value.AsMap(value.Copy(ch.fixed)), req), s.field)
}

func provisioningKey(contract string) string { return value.Key("global.okdp.provisioning", contract) }

// claimClass works out which provider the claim at s names, without
// looking the instance up. A claim that names a provider has no class.
// Otherwise the class is the one it names, or the default of the context,
// and must be defined in classes. It also returns the provider's name as
// written, and how messages call the class ("" without one).
func claimClass(classes M, s claimSite) (ch choice, name, which string, err error) {
	where, contract, spec := s.where(), s.contract, s.spec
	key := provisioningKey(contract)
	ch = choice{class: spec.Class, named: spec.Class != "" || spec.Provider != ""}
	if spec.Provider != "" {
		return ch, spec.Provider, "", nil
	}
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
	if _, _, ok := providerName(name, s.namespace); !ok {
		return ch, "", "", fmt.Errorf("%s: %s (%s): provider %q: %s", where, which, value.Key(key+".classes", ch.class), name, providerForms)
	}
	return ch, name, which, nil
}

// provider picks the instance the claim at s binds to: the provider the
// claim names, else that of its class. The name must match exactly one
// instance, by name or release, which provisions the contract
// (candidates) and is not the claimer (self; nil for a named claim).
func (t *compilation) provider(classes M, s claimSite, self *member, candidates []*member) (choice, error) {
	ch, name, which, err := claimClass(classes, s)
	if err != nil {
		return ch, err
	}
	// Messages name the provider after the class it comes from.
	named := s.where()
	if which != "" {
		named += ": " + which
	}
	ns, n, _ := providerName(name, s.namespace)
	matches := t.byName[instKey{ns, n}]
	switch {
	case len(matches) > 1:
		return ch, fmt.Errorf("%s: %s/%s names more than one instance, %s: the claim would not know which", named, ns, n, matched(n, matches))
	case len(matches) == 0 || !slices.Contains(candidates, matches[0]):
		return ch, fmt.Errorf("%s: %s/%s is no instance that provisions %s connections", named, ns, n, s.contract)
	case matches[0] == self:
		return ch, fmt.Errorf("%s: the instance provisions it itself: a claim is of another instance", s.where())
	}
	ch.provider = matches[0]
	return ch, nil
}

// consent reports whether the claim id at s may bind to the provider of
// ch. Within a namespace it always may. Across namespaces it may when the
// class is one the platform defines (platform: the classes of level 0,
// which no higher level can change), because the platform routes every
// namespace to it, or when the provider's claimers list the namespace, or
// "*".
func consent(s claimSite, id string, ch choice, platform M) error {
	p := ch.provider
	if p.Inputs.Namespace == s.namespace {
		return nil
	}
	by := ""
	if s.spec.Provider == "" {
		if _, ok := value.AsMap(value.AsMap(platform[s.contract])["classes"])[ch.class]; ok {
			return nil
		}
		by = fmt.Sprintf(", the provider of class %q", ch.class)
		if s.spec.Class == "" {
			by = fmt.Sprintf(", the provider of the default class %q", ch.class)
		}
	}
	claimers := p.Claimers(s.contract)
	if consents(claimers, s.namespace) {
		return nil
	}
	provider := path.Join(p.Inputs.Namespace, p.Inputs.Release)
	list := cmp.Or(strings.Join(claimers, ", "), "none")
	if p.Inputs.Claimers == nil {
		list += ", its package's"
	}
	return fmt.Errorf("%s: claim %q (%s): of %s%s, another namespace: %s is not one of its claimers (claimers of instance %s: %s): the provider lists the namespaces that may claim of it (claimers of its instance.yaml, else of its package's provisions), or the platform routes them through a class of platform.yaml", s.field, id, s.contract, provider, by, s.namespace, provider, list)
}

// matched lists the instances the provider name n matches, in release
// order: "a (its release), b (its name) and c (its name)".
func matched(n string, matches []*member) string {
	var each []string
	for _, m := range matches {
		by := "its name"
		if m.Inputs.Release == n {
			by = "its release"
		}
		each = append(each, fmt.Sprintf("%s (%s)", m.Inputs.Release, by))
	}
	slices.Sort(each)
	last := len(each) - 1
	return strings.Join(each[:last], ", ") + " and " + each[last]
}

// refuseShared is step 4, for the instances ms of one namespace. Both
// publishers of an output are refused, but stay published. The instances
// that resolved through it, or through an instance refused since, are
// compiled again: each gets the error it would have got in any order.
func (t *compilation) refuseShared(ms []*member) {
	providers := map[outputKey][]string{}
	for _, m := range ms {
		if res := m.outcome.Result; res != nil {
			// A compile refuses two outputs of one contract and name, so each
			// release counts once.
			for _, o := range res.Info.Provides {
				k := outputKey{contract: o.Contract, name: o.Name}
				providers[k] = append(providers[k], res.Release)
			}
		}
	}
	shared := map[outputKey]bool{}
	for _, m := range ms {
		res := m.outcome.Result
		if res == nil {
			continue
		}
		var errs []error
		for _, o := range res.Info.Provides {
			k := outputKey{contract: o.Contract, name: o.Name}
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
			*m.outcome = Outcome{Err: errors.Join(errs...), Claims: m.outcome.Claims}
		}
	}
	if len(shared) == 0 {
		return
	}
	dropped := map[string]bool{}
	t.recompile(ms, func(_ *Result, d Dependency) bool {
		return d.Namespace == "" && (shared[outputKey{contract: d.Contract, name: d.Output}] || dropped[d.Instance])
	}, func(res *Result) { dropped[res.Release] = true })
}

// secretTarget is something only one object of a namespace may write: a
// Secret (kind "Secret") or a key of the secret store (kind "push").
type secretTarget struct{ ns, kind, name string }

// secretTargets returns what the object o, of an instance of namespace
// ns, writes: a v1 Secret its own name, an ExternalSecret its target
// (unless its creationPolicy is None), a PushSecret each key of the store
// it pushes.
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

// refuseSecretTargets is step 5. Two objects that write the same Secret
// would overwrite each other's credentials, whether they belong to one
// instance or two. Each instance that holds one is no longer published.
//
// Without this check a provider could forge the Secret of another
// instance's connection (<release>.<connection>-credentials) through its
// secretRef.name, which names its claimers' copies (<release>-<name>).
func (t *compilation) refuseSecretTargets(ms []*member) {
	type writer struct{ release, object string }
	writers := map[secretTarget][]writer{}
	for _, m := range ms {
		res := m.outcome.Result
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
	for _, m := range ms {
		res := m.outcome.Result
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
			*m.outcome = Outcome{Err: errors.Join(errs...), Claims: m.outcome.Claims}
			delete(t.published[res.Namespace], res.Release)
		}
	}
}

// refuseClaimers is step 6. A provider refused by refuseShared stays
// published, so a claimer of another namespace may have compiled with its
// output. Compiled again, it gets no output for its claim and fails.
func (t *compilation) refuseClaimers(ms []*member) {
	t.recompile(ms, func(res *Result, d Dependency) bool {
		p, ok := t.byRelease[instKey{cmp.Or(d.Namespace, res.Namespace), d.Instance}]
		return ok && p.outcome.Result == nil
	}, nil)
}
