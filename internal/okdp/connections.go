// Connections: the connection references of the parameter schema
// (x-okdp-connection-ref) and the contracts they are checked against. A
// reference resolves to an external connection, a claim, or an output
// that another instance of the namespace publishes.

package okdp

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"reflect"
	"slices"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/okdp/okdp-compiler/chart"
	"github.com/okdp/okdp-compiler/value"
)

// resolver resolves the connection references of an instance, and records
// those that point to an instance (deps).
type resolver struct {
	rules     *RuleSet             // names the credentials Secrets
	contracts *contractCache       // Inputs.Contracts
	conns     M                    // the external connections
	levels    map[string]uint      // the context level each connection is given at, by name
	claims    map[string]ClaimSpec // the named claims of the context (namedClaims)
	classes   M                    // global.okdp.provisioning, by contract, for the default classes
	published map[string]Info      // Inputs.Published, by release; nil when unknown
	bindings  map[string]Binding   // Inputs.Bindings, by parameter path
	// store is global.okdp.secrets.store: the credentials of a claim of
	// another namespace and those of a secretRef.remoteKey go through it.
	store string
	// provider reports whether global.okdp.secrets.provider is set. Each
	// namespace then has its own SecretStore, which reads no platform/ key.
	provider  bool
	transfers []transfer // the credentials to read from the store, one per Secret
	release   string     // Inputs.Release; the instance is never its own provider
	namespace string
	draft     any          // $schema of the parameter schema, for the requirements that name no draft
	deps      []Dependency // the references to an instance that resolve, in order
	errs      []error      // the references that do not resolve, in order (refs)
	missing   []outputKey  // the outputs references name that nobody publishes, in order
	// reqs holds the requirement of each reference read so far, by the
	// address of its x-okdp-connection-ref: every item of a list has one.
	reqs map[uintptr]requirementRead
}

type requirementRead struct {
	req *requirement
	err error
}

// outputKey identifies an output by namespace, contract and name. An
// empty namespace is the instance's own. release is the instance that
// publishes it, when it is a claim's provider; "" means any instance.
type outputKey struct{ namespace, contract, name, release string }

// missingOutputs is the error of a compile whose references name outputs
// that nobody publishes yet. CompileAll waits for them before it compiles
// the instance again. err is the compile's error unchanged.
type missingOutputs struct {
	outputs []outputKey
	err     error
}

func (e *missingOutputs) Error() string { return e.err.Error() }
func (e *missingOutputs) Unwrap() error { return e.err }

// Dependency is an instance that a connection reference of the parameters
// points to: the instance of the namespace that publishes the output the
// reference names, or the provider a claim is bound to, which may be of
// another namespace. A deployer reads them to order the releases.
type Dependency struct {
	// Instance is the release name of the instance that publishes the output.
	Instance string `json:"instance,omitempty" yaml:"instance,omitempty"`
	// Namespace is the namespace of Instance when it is not the instance's
	// own: a claim bound to a provider of another namespace.
	Namespace string `json:"namespace,omitempty" yaml:"namespace,omitempty"`
	Contract  string `json:"contract,omitempty" yaml:"contract,omitempty"`
	// Field is the parameter that references it, e.g. backends[0].database.
	Field string `json:"field,omitempty" yaml:"field,omitempty"`
	// Output is the name of the output it resolved to; for a claim, its ID.
	Output string `json:"output,omitempty" yaml:"output,omitempty"`
}

// refs walks the value v along the schema s and replaces every value
// whose schema carries an x-okdp-connection-ref by what visit returns for
// it, in path order. visit gets the reference, the value, its parameter
// path and its key, which messages print the value with. Keys the schema
// does not declare are left as they are.
func refs(s M, v any, path, key string, visit func(ref M, v any, path, key string) any) any {
	if ref, ok := s["x-okdp-connection-ref"].(M); ok {
		return visit(ref, v, path, key)
	}
	switch t := v.(type) {
	case M:
		props := value.AsMap(s["properties"])
		for _, k := range slices.Sorted(maps.Keys(props)) {
			if x, ok := t[k]; ok {
				t[k] = refs(value.AsMap(props[k]), x, value.Key(path, k), k, visit)
			}
		}
	case []any:
		for i, x := range t {
			t[i] = refs(value.AsMap(s["items"]), x, value.Index(path, i), key, visit)
		}
	}
	return v
}

// reference resolves v, the value of a reference (ref) at path. Null or
// "" means not set, a string names a connection, a named claim or an
// output, and an object is a claim template. Any other value is refused,
// because it would reach the mapping unresolved and unchecked by the
// contract. On error the value is left as it is.
func (r *resolver) reference(ref M, v any, path, key string) any {
	if v == nil || v == "" {
		return v
	}
	contract, provision := value.Str(ref["contract"]), value.Truthy(ref["provision"])
	// It compiles: requirements checked it with the parameter schema.
	req, err := r.requirement(ref)
	if err != nil {
		r.errs = append(r.errs, atField(path, fmt.Errorf("%s: x-okdp-connection-ref: %w", path, err)))
		return v
	}
	var c M
	l := lookup{field: path, contract: contract, req: req, provision: provision}
	if m, ok := v.(M); ok && provision {
		var spec ClaimSpec
		if spec, err = parseClaim(m, path, false); err == nil {
			l.claim = &spec
			c, err = r.resolve(l)
		}
	} else if s, ok := v.(string); ok {
		l.name = s
		c, err = r.resolve(l)
	} else {
		err = fmt.Errorf("%s: a connection reference must be a string, got %s", path, value.ShortAt(key, v))
	}
	if err != nil {
		r.errs = append(r.errs, atField(path, err))
		return v
	}
	return c
}

// requirement returns the requirement of ref (readRequirement), read once
// per resolver.
func (r *resolver) requirement(ref M) (*requirement, error) {
	k := reflect.ValueOf(ref).Pointer()
	x, ok := r.reqs[k]
	if !ok {
		x.req, x.err = readRequirement(ref, r.draft)
		if r.reqs == nil {
			r.reqs = map[uintptr]requirementRead{}
		}
		r.reqs[k] = x
	}
	return x.req, x.err
}

// declaredConnections checks every connection of the context of in at its
// declaration, whatever its level and whether a reference names it or not.
// It returns nil when the context's layers fail: the instance's compile
// reports them. seen is as checkDeclared's.
func declaredConnections(ctx context.Context, in Inputs, seen map[string]error) error {
	layers, platform, err := contextLayers(ctx, in.rules(), in.Context)
	if err != nil {
		return nil //nolint:nilerr // the instance's compile reports it
	}
	return checkDeclared(in, layers, platform, seen)
}

// checkDeclared is declaredConnections once the context's layers are read
// (contextLayers). seen, when not nil, holds the outcome of each
// connection checked so far, by what its check reads (declarationKey): the
// instances of a project check its connections and the platform's once.
func checkDeclared(in Inputs, layers []map[string]any, platform M, seen map[string]error) error {
	conns := M{}
	for _, l := range layers {
		if c, ok := l["connections"].(M); ok {
			conns = value.Merge(conns, c)
		}
	}
	r := resolver{
		rules:     in.rules(),
		contracts: contractCacheOf(in),
		conns:     conns,
		levels:    connectionLevels(in.Context),
		store:     value.Str(value.Dig(platform, "global.okdp.secrets.store")),
		provider:  len(value.AsMap(value.Dig(platform, "global.okdp.secrets.provider"))) > 0,
		release:   in.Release,
		namespace: in.Namespace,
	}
	return r.declared(seen)
}

// CheckConnection checks one connection as CompileAll checks it at its
// declaration. conn is its entry as written in connections, level the
// level of the context it is declared at (0 the platform, 1 a project, 2
// an instance) and store global.okdp.secrets.store.
func CheckConnection(rules *RuleSet, contracts fs.FS, name string, conn any, level uint, namespace, store string) error {
	v, err := value.Normalize(conn)
	if err != nil {
		return fmt.Errorf("%s: %w", value.Key("connections", name), err)
	}
	r := resolver{
		rules:     rulesOr(rules),
		contracts: newContractCache(contracts),
		conns:     M{name: v},
		levels:    map[string]uint{name: level},
		store:     store,
		namespace: namespace,
	}
	return r.declared(nil)
}

// declared checks every connection of the context, in name order, and
// reports every one that fails, each at connections.<name>. seen, when not
// nil, holds the outcome of the connections checked before, by
// declarationKey, and receives those it checks.
func (r *resolver) declared(seen map[string]error) error {
	var errs []error
	for _, name := range slices.Sorted(maps.Keys(r.conns)) {
		key, keyed := r.declarationKey(name)
		err, done := seen[key]
		if !keyed || !done {
			field := value.Key("connections", name)
			err = atField(field, r.declaration(name, field))
		}
		if keyed && seen != nil {
			seen[key] = err
		}
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// declarationKey identifies what the check of the connection name at its
// declaration reads: its entry, its level, the namespace, the secret
// store and its provider, and the contracts. keyed is false when the entry
// cannot be encoded.
func (r *resolver) declarationKey(name string) (key string, keyed bool) {
	b, err := json.Marshal(r.conns[name])
	if err != nil {
		return "", false
	}
	return fmt.Sprintf("%p %q %q %t %d %q %s", r.contracts, r.namespace, r.store, r.provider, r.levels[name], name, b), true
}

// declaration checks the connection name, declared at field, as a
// reference to it would. The transfers it would need are not recorded.
func (r *resolver) declaration(name, field string) error {
	conn, ok := r.conns[name].(M)
	switch {
	case !chart.IsDNSLabel(name):
		return fmt.Errorf("%s: a connection's name is a DNS-1123 label, lowercase letters, digits and \"-\", 63 characters at most", field)
	case !ok:
		return fmt.Errorf("%s: a connection, an object of its contract's fields, got %s", field, value.ShortAt(name, r.conns[name]))
	case value.Str(conn["contract"]) == "":
		return fmt.Errorf("%s: contract is required, the contract of the connection", field)
	}
	c, err := r.contracts.get(value.Str(conn["contract"]))
	if err != nil {
		return fmt.Errorf("%s: %w", field, err)
	}
	d := *r
	d.transfers = nil
	x, err := d.resolveExternal(c, name, field, conn)
	if err != nil {
		return err
	}
	if err := c.validate(x.values, x.where); err != nil {
		return err
	}
	return chart.NoTemplate(x.values, func(p string) string { return fmt.Sprintf("%s: field %q", x.where, p) })
}

// lookup is one connection reference to resolve.
type lookup struct {
	field     string       // its parameter path
	contract  string       // the contract it takes
	name      string       // the connection, named claim or output it names; "" for a claim template
	claim     *ClaimSpec   // its claim template; nil when it names something
	req       *requirement // what it requires beyond the contract; nil for nothing
	provision bool         // it accepts a claim
}

// resolved is what a reference resolves to, before the checks every
// source shares. ref, source, instance and namespace go to the mapping.
type resolved struct {
	values    M
	secretRef any // {name}, nil when none
	// where names the reference in messages:
	//
	//	<field> (connection "<name>")
	//	<field> (claim "<id>" of <provider>)
	//	<field> (output "<name>" of <instance>)
	where     string
	ref       string // the connection's name, the claim's ID or the output's name
	source    string // external, claim or output
	instance  string // the instance it comes from; "" when external
	namespace string // its namespace, when it is not the compiling instance's
	advice    string // what the error of a requirement it does not meet adds
}

// resolve resolves the reference l to the fields of its contract. The
// source is the first that applies: a named claim, when no connection has
// that name; a claim template; an external connection; an output that
// another instance of the namespace publishes under that name. A claim
// resolves to the output its binding gives.
//
// Whatever the source, the result is completed and checked against the
// contract, then against the requirement of the reference. It carries an
// okdp key that tells the mapping where it comes from.
func (r *resolver) resolve(l lookup) (M, error) {
	ref, field, claim, req := l.name, l.field, l.claim, l.req
	c, err := r.contracts.get(l.contract)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", field, err)
	}
	if err := c.requires(req, field); err != nil {
		return nil, err
	}
	var x *resolved
	// A named claim has no connection's name (namedClaims), and a claim
	// template no name.
	named, isNamed := r.claims[ref]
	switch conn, external := r.conns[ref].(M); {
	case isNamed && !l.provision:
		return nil, fmt.Errorf("%s: %q is a claim: the reference takes none (x-okdp-connection-ref's provision)", field, ref)
	case isNamed && named.Contract != c.name:
		return nil, fmt.Errorf("%s: claim %q has contract %q, %q is expected", field, ref, named.Contract, c.name)
	case isNamed:
		x, err = r.resolveClaim(c, ref, field, &named)
	case claim != nil:
		x, err = r.resolveClaim(c, ref, field, claim)
	case external:
		x, err = r.resolveExternal(c, ref, field, conn)
	default:
		x, err = r.resolveOutput(c, ref, field)
	}
	if err != nil {
		return nil, err
	}
	out := x.values
	if err := c.validate(out, x.where); err != nil {
		return nil, err
	}
	if x.secretRef != nil {
		out["secretRef"] = x.secretRef
	}
	if err := req.check(out, x.where, x.advice); err != nil {
		return nil, err
	}
	from := M{"ref": x.ref, "contract": c.name, "source": x.source}
	if x.instance != "" {
		from["instance"] = x.instance
	}
	if x.namespace != "" {
		from["namespace"] = x.namespace
	}
	out["okdp"] = from
	// What the mapping receives holds no template code, whatever layer,
	// output or default it comes from.
	if err := chart.NoTemplate(out, func(p string) string { return fmt.Sprintf("%s: field %q", x.where, p) }); err != nil {
		return nil, err
	}
	if x.instance != "" {
		r.deps = append(r.deps, Dependency{Instance: x.instance, Namespace: x.namespace, Contract: c.name, Field: field, Output: x.ref})
	}
	return out, nil
}

// resolveClaim resolves the claim at field to the output its binding
// gives. When the provider is of another namespace, the credentials are
// read through the platform's secret store into a Secret of the
// instance's own.
func (r *resolver) resolveClaim(c *contract, ref, field string, claim *ClaimSpec) (*resolved, error) {
	contract := c.name
	b, ok := r.bindings[field]
	if !ok {
		return nil, fmt.Errorf("%s: claim (%s): no provider is bound to it: the claims bind when the instances compile together (CompileAll)", field, contract)
	}
	if b.Contract != contract {
		return nil, fmt.Errorf("%s: claim (%s): bound as a %s claim", field, contract, b.Contract)
	}
	provider := path.Join(b.Namespace, b.Release)
	if b.Output == nil {
		r.missing = append(r.missing, outputKey{namespace: b.Namespace, contract: contract, name: b.ID, release: b.Release})
		return nil, fmt.Errorf("%s: claim %q (%s): %s publishes no output for it", field, b.ID, contract, provider)
	}
	x := &resolved{ref: b.ID, source: "claim", instance: b.Release}
	if b.Namespace != r.namespace {
		x.namespace = b.Namespace
	}
	from := fmt.Sprintf("claim %q of %s", b.ID, provider)
	x.where = fmt.Sprintf("%s (%s)", field, from)
	// The claim took the default class: the provider of another class may
	// meet the requirement, and the advice says so.
	if claim.Provider == "" && claim.Class == "" {
		key := provisioningKey(contract)
		class := value.Str(value.AsMap(r.classes[contract])["default"])
		x.advice = fmt.Sprintf("; %s is the provider of the default class %s of %s connections (%s.default): name a class whose provider meets it (class), or change the default", provider, class, contract, key)
	}
	var err error
	if x.values, x.secretRef, err = c.published(*b.Output, field, from); err != nil {
		return nil, err
	}
	if fields := c.secretFields(); x.namespace != "" && x.secretRef != nil && fields != nil {
		if r.store == "" {
			return nil, fmt.Errorf("%s: claim %q (%s): of %s, another namespace: its credentials go through the platform's secret store, global.okdp.secrets.store, which is not set", field, b.ID, contract, provider)
		}
		name := r.rules.Name("claimSecret", "release", r.release, "secret", b.Output.SecretRef.Name)
		r.addTransfer(transfer{secret: name, key: storeKey(b.Namespace, b.ID), contract: contract, fields: fields})
		x.secretRef = M{"name": name}
	}
	return x, nil
}

// resolveExternal resolves a reference to the external connection ref,
// whose entry is conn. Its secretRef is either {name}, a Secret of the
// namespace, or {remoteKey}, credentials in the platform's secret store.
func (r *resolver) resolveExternal(c *contract, ref, field string, conn M) (*resolved, error) {
	x := &resolved{values: M{}, ref: ref, source: "external", where: fmt.Sprintf("%s (connection %q)", field, ref)}
	if value.Str(conn["contract"]) != c.name {
		return nil, fmt.Errorf("%s: connection %q has contract %q, %q is expected", field, ref, value.Str(conn["contract"]), c.name)
	}
	var secretRef M
	x.values, secretRef = connectionFields(conn)
	if err := c.fields(x.values, fmt.Sprintf("%s: connection %q", field, ref)); err != nil {
		return nil, err
	}
	switch _, fromStore := secretRef["remoteKey"]; {
	case fromStore:
		name, err := r.storeSecret(c, ref, field, secretRef)
		if err != nil {
			return nil, err
		}
		x.secretRef = M{"name": name}
	case secretRef != nil:
		x.secretRef = M{"name": secretRef["name"]}
	}
	return x, nil
}

// connectionFields splits the entry of an external connection into its
// contract fields and its secretRef, nil when it has none.
func connectionFields(conn M) (fields, secretRef M) {
	fields = M{}
	for k, v := range conn {
		switch k {
		case "contract", "description":
		case "secretRef":
			secretRef = value.AsMap(v)
		default:
			fields[k] = v
		}
	}
	return fields, secretRef
}

// Connection is an external connection as written at connections.<name>
// in the context. Its secret fields are not in it: secretRef names a
// Secret in the namespace of each instance that references the connection
// (SecretRef) or a key of the platform's secret store (RemoteKey).
type Connection struct {
	Name        string         // its key in connections
	Contract    string         // contract
	Description string         // description, "" when none
	Values      map[string]any // its other keys, the contract's fields, not the secret ones
	SecretRef   string         // secretRef.name, "" when none
	RemoteKey   string         // secretRef.remoteKey, "" when none
}

// ParseConnection reads the connection name from v, its entry as written
// in connections. It checks nothing: the compiles that reference it do.
func ParseConnection(name string, v any) Connection {
	conn := value.AsMap(v)
	fields, secretRef := connectionFields(conn)
	return Connection{
		Name:        name,
		Contract:    value.Str(conn["contract"]),
		Description: value.Str(conn["description"]),
		Values:      fields,
		SecretRef:   value.Str(secretRef["name"]),
		RemoteKey:   value.Str(secretRef["remoteKey"]),
	}
}

// Entry returns the connection as written at connections.<name>. A key of
// Values named contract, description or secretRef is left out.
func (c Connection) Entry() map[string]any {
	out := c.fields()
	out["contract"] = c.Contract
	if c.Description != "" {
		out["description"] = c.Description
	}
	if c.SecretRef != "" || c.RemoteKey != "" {
		ref := M{}
		if c.SecretRef != "" {
			ref["name"] = c.SecretRef
		}
		if c.RemoteKey != "" {
			ref["remoteKey"] = c.RemoteKey
		}
		out["secretRef"] = ref
	}
	return out
}

// MarshalYAML encodes the connection as written (yaml.v3): its own keys
// first, then the contract's fields, sorted.
func (c Connection) MarshalYAML() (any, error) {
	type ref struct {
		Name      string `yaml:"name,omitempty"`
		RemoteKey string `yaml:"remoteKey,omitempty"`
	}
	entry := struct {
		Contract    string `yaml:"contract"`
		Description string `yaml:"description,omitempty"`
		SecretRef   *ref   `yaml:"secretRef,omitempty"`
		Fields      M      `yaml:",inline"`
	}{Contract: c.Contract, Description: c.Description}
	if c.SecretRef != "" || c.RemoteKey != "" {
		entry.SecretRef = &ref{Name: c.SecretRef, RemoteKey: c.RemoteKey}
	}
	entry.Fields = c.fields()
	return entry, nil
}

// fields returns Values without the keys the connection's own fields
// give: contract, description and secretRef.
func (c Connection) fields() M {
	out := M{}
	for k, v := range c.Values {
		if k != "contract" && k != "description" && k != "secretRef" {
			out[k] = v
		}
	}
	return out
}

// storeSecret handles the secretRef {remoteKey} of the external connection
// ref: its credentials are in the platform's secret store under that key,
// one property per secret field of c. It returns the name of the Secret
// of the instance's namespace they are read into, and records the transfer.
//
// The Secret is named <release>.<ref>-credentials. Neither part holds a
// dot, so no other Secret takes that name: the copy of a claim's
// credentials is named <release>-<secret>, and the Secret a provider
// publishes for a claim starts with <namespace>.<...>, while the release
// of a tree is <namespace>-<instance>.
//
// A connection above level 0 names a key under its own namespace,
// <namespace>/<rest>: a project never reads the key of another namespace,
// and the platform's keys are read through the platform's connections
// only. With a store provider (global.okdp.secrets.provider) each
// namespace has its own SecretStore, which no policy lets read platform/:
// no connection may then name a key under it.
func (r *resolver) storeSecret(c *contract, ref, field string, secretRef M) (string, error) {
	where := fmt.Sprintf("%s: connection %q: secretRef", field, ref)
	key, ok := secretRef["remoteKey"].(string)
	fields, level := c.secretFields(), r.levels[ref]
	switch {
	case secretRef["name"] != nil:
		return "", fmt.Errorf("%s: name or remoteKey, not both", where)
	case !ok || key == "":
		// A key of the store names credentials and holds none, so it is
		// printed as the messages below print it. value.Short is used, not
		// ShortAt, which reads remoteKey as an API key.
		return "", fmt.Errorf("%s.remoteKey: the key of the credentials in the platform's secret store, a non-empty string, got %s", where, value.Short(secretRef["remoteKey"]))
	case r.store == "":
		return "", fmt.Errorf("%s.remoteKey: its credentials are in the platform's secret store, global.okdp.secrets.store, which is not set", where)
	case !chart.IsDNSLabel(ref):
		return "", fmt.Errorf("%s.remoteKey: the connection's name is not a DNS-1123 label: its credentials Secret is named after it, <release>.<name>-credentials", where)
	case fields == nil:
		return "", fmt.Errorf("%s.remoteKey: the %s contract has no secret field (x-okdp-secret): there are no credentials to read", where, c.name)
	case !isStorePath(key):
		return "", fmt.Errorf("%s.remoteKey %q: a path of the platform's secret store, segments separated by \"/\", none empty, \".\" or \"..\"", where, key)
	case r.provider && strings.HasPrefix(key, platformKeys+"/"):
		return "", fmt.Errorf("%s.remoteKey %q: no namespace reads %s/ when the platform gives a secret store provider (global.okdp.secrets.provider, a SecretStore per namespace): the platform's credentials are copied under the keys of the namespaces that use them, <namespace>/<key>, read through a connection of the project (level 1)", where, key, platformKeys)
	case level > 0 && !strings.HasPrefix(key, r.namespace+"/"):
		return "", fmt.Errorf("%s.remoteKey %q: a key of the platform's secret store a project may not read: a connection above level 0 (level %d) names a key under %q, its namespace; the platform's keys are read through the platform's connections (level 0)", where, key, level, r.namespace+"/")
	}
	if err := chart.NoTemplate(M{"secretRef": M{"remoteKey": key}}, func(p string) string { return fmt.Sprintf("%s (connection %q): field %q", field, ref, p) }); err != nil {
		return "", err
	}
	name := r.rules.Name("connectionSecret", "release", r.release, "connection", ref)
	r.addTransfer(transfer{secret: name, key: key, contract: c.name, fields: fields})
	return name, nil
}

// isStorePath reports whether key is segments separated by "/", none of
// them empty, "." or "..". A key thus never reaches past its first
// segment, the one a store's policy scopes.
func isStorePath(key string) bool {
	for s := range strings.SplitSeq(key, "/") {
		if s == "" || s == "." || s == ".." {
			return false
		}
	}
	return true
}

// connectionLevels returns, by name, the lowest level of the context that
// gives each connection, because a lower level's connection is taken
// whole. storeSecret scopes a secretRef.remoteKey by it. A layer that
// does not normalize is skipped: valuesLayers has already reported it.
func connectionLevels(layers []ContextLayer) map[string]uint {
	levels := map[string]uint{}
	for _, l := range layers {
		v, err := value.Normalize(l.Values)
		if err != nil {
			continue
		}
		for name := range value.AsMap(value.AsMap(v)["connections"]) {
			if lv, ok := levels[name]; !ok || l.Level < lv {
				levels[name] = l.Level
			}
		}
	}
	return levels
}

// addTransfer records x once per Secret: two references to one connection,
// or to one named claim, read its credentials through one ExternalSecret.
func (r *resolver) addTransfer(x transfer) {
	if !slices.ContainsFunc(r.transfers, func(t transfer) bool { return t.secret == x.secret }) {
		r.transfers = append(r.transfers, x)
	}
}

// resolveOutput resolves a reference to the output ref of the contract c
// that another instance of the namespace publishes.
func (r *resolver) resolveOutput(c *contract, ref, field string) (*resolved, error) {
	o, provider, err := r.output(ref, c.name, field)
	if err != nil {
		return nil, err
	}
	x := &resolved{ref: ref, source: "output", instance: provider}
	from := fmt.Sprintf("output %q of %s", ref, provider)
	x.where = fmt.Sprintf("%s (%s)", field, from)
	if x.values, x.secretRef, err = c.published(o, field, from); err != nil {
		return nil, err
	}
	return x, nil
}

// published returns the values of the output o as a reference gets them,
// a copy, and its secretRef ({name}, nil when it has none). Each value
// must be a field of the contract and not a secret one.
func (c *contract) published(o Output, field, from string) (M, any, error) {
	v, err := value.Normalize(o.Values)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %s: %w", field, from, err)
	}
	out := value.AsMap(v)
	if err := c.fields(out, field+": "+from); err != nil {
		return nil, nil, err
	}
	if o.SecretRef != nil && o.SecretRef.Name != "" {
		return out, M{"name": o.SecretRef.Name}, nil
	}
	return out, nil, nil
}

// output finds the output named ref of the contract among what the other
// instances of the namespace publish, with the release that publishes it.
// When none does, it records the output (missing).
func (r *resolver) output(ref, contract, field string) (Output, string, error) {
	var found Output
	var providers, others []string
	for _, release := range slices.Sorted(maps.Keys(r.published)) {
		if release == r.release {
			continue
		}
		for _, o := range r.published[release].Provides {
			switch {
			case o.Name != ref:
			case o.Contract == contract:
				found, providers = o, append(providers, release)
			case others == nil:
				others = append(others, fmt.Sprintf("%s publishes it with contract %q", release, o.Contract))
			default:
				others = append(others, fmt.Sprintf("%s with contract %q", release, o.Contract))
			}
		}
	}
	// A release counts once: it publishes one output of a contract and name
	// (twice).
	switch providers = slices.Compact(providers); {
	case len(providers) == 1:
		return found, providers[0], nil
	case len(providers) > 1:
		return Output{}, "", fmt.Errorf("%s: %q names %s outputs of several instances of the namespace: %s", field, ref, contract, strings.Join(providers, ", "))
	}
	r.missing = append(r.missing, outputKey{contract: contract, name: ref})
	msg := fmt.Sprintf("%s: %q is not a connection", field, ref)
	if r.published != nil {
		msg += ", nor an output of an instance of the namespace"
		if others != nil {
			msg += fmt.Sprintf(" (%s, %q is expected)", strings.Join(others, ", "), contract)
		}
	}
	return Output{}, "", errors.New(msg)
}

// contract is a contract: the JSON Schema of a kind of connection, read
// from <name>.schema.json.
type contract struct {
	name  string // what references and outputs call it
	label string // x-okdp-contract: its name in the error of a required field
	props M      // properties: the schema of each field
	// secret holds the fields marked x-okdp-secret. They are credentials:
	// in the Secret, never in the values.
	secret   map[string]bool
	required []string      // the required fields, without the secret ones, sorted
	schema   *chart.Schema // validates the values of its connections and outputs
	// claim is x-okdp-claim: the schema of what a claim may request of a
	// provider, with its defaults. Nil when the contract declares none.
	claim *chart.Schema
}

// readContract reads the contract name from <name>.schema.json in fsys.
//
// The validator keeps the numbers as written; the keywords are read from
// a map view where every number is a float64. The secret fields are
// removed from the validator's required list, because they are in the
// credentials Secret and never in the values. The claim schema is read in
// the contract's draft unless it names its own.
func readContract(fsys fs.FS, name string) (*contract, error) {
	file := name + ".schema.json"
	b, err := fs.ReadFile(fsys, file)
	fail := func(cause any) error {
		return fmt.Errorf("the %s contract cannot be read: %s: %v", name, file, cause)
	}
	if err != nil {
		// The PathError is unwrapped: fail already names the file.
		if pe := (*fs.PathError)(nil); errors.As(err, &pe) {
			err = pe.Err
		}
		return nil, fail(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		return nil, fail(err)
	}
	v, err := value.Normalize(doc)
	if err != nil {
		return nil, fail(err)
	}
	m, ok := v.(M)
	if !ok {
		return nil, fail("not a JSON object")
	}
	c := &contract{name: name, label: value.Str(m["x-okdp-contract"]), props: value.AsMap(m["properties"]), secret: map[string]bool{}}
	// x-okdp-internal is no longer supported: a reference names an output
	// its provider publishes.
	if _, ok := m["x-okdp-internal"]; ok {
		return nil, fail("x-okdp-internal: naming conventions are gone, a provider publishes its connections (provides)")
	}
	for k, p := range c.props {
		if value.Truthy(value.AsMap(p)["x-okdp-secret"]) {
			c.secret[k] = true
		}
	}
	d := value.AsMap(doc) // an object, as m
	if required, ok := d["required"].([]any); ok {
		kept := []any{}
		for _, k := range required {
			if f := value.Str(k); !c.secret[f] {
				kept = append(kept, k)
				c.required = append(c.required, f)
			}
		}
		slices.Sort(c.required)
		d["required"] = kept
		if len(kept) == 0 {
			delete(d, "required") // draft-04 refuses an empty list
		}
	}
	if c.schema, err = chart.NewSchema(file, "file:///"+file, d); err != nil {
		return nil, fail(err)
	}
	if claim, set := d["x-okdp-claim"]; set {
		cd, ok := claim.(map[string]any)
		if !ok {
			return nil, fail(fmt.Sprintf("x-okdp-claim: a JSON Schema, an object, got %s", value.ShortAt("x-okdp-claim", m["x-okdp-claim"])))
		}
		cd = maps.Clone(cd)
		if _, ok := cd["$schema"]; !ok && d["$schema"] != nil {
			cd["$schema"] = d["$schema"]
		}
		b, err := json.Marshal(cd)
		if err == nil {
			c.claim, err = chart.ParseSchema("x-okdp-claim", b)
		}
		if err != nil {
			return nil, fail(err)
		}
	}
	return c, nil
}

// request returns what a claim at field requests of a provider of the
// contract: req with the defaults of the claim schema applied, checked
// against that schema. A contract without claim schema accepts no request.
//
//	<field>: claim (<contract>): the request does not meet the contract's claim schema (x-okdp-claim):
//	- at '/size': '5G' does not match pattern '^[0-9]+(Mi|Gi)$'
func (c *contract) request(req M, field string) (M, error) {
	where := fmt.Sprintf("%s: claim (%s)", field, c.name)
	if c.claim == nil {
		if len(req) > 0 {
			return nil, fmt.Errorf("%s: request: the contract declares no claim schema (x-okdp-claim): a claim requests nothing of it", where)
		}
		return nil, nil
	}
	values, v, err := c.claim.Values(value.AsMap(value.Copy(req)))
	if err == nil && v != "" {
		err = fmt.Errorf("the request does not meet the contract's claim schema (x-okdp-claim):\n%s", v)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", where, err)
	}
	return values, nil
}

// requirement is what a connection reference requires of its connection
// beyond the contract: a JSON Schema of its fields and secretRef.
type requirement struct {
	doc    M
	schema *chart.Schema
}

// readRequirement compiles the requirement of ref, in the draft of the
// parameter schema unless it names its own; nil when ref has none.
func readRequirement(ref M, draft any) (*requirement, error) {
	v := ref["require"]
	if v == nil {
		return nil, nil
	}
	doc, ok := v.(M)
	if !ok {
		return nil, fmt.Errorf("require: a JSON Schema, an object, got %s", value.ShortAt("require", v))
	}
	doc = value.AsMap(value.Copy(doc))
	if _, ok := doc["$schema"]; !ok && draft != nil {
		doc["$schema"] = draft
	}
	s, err := chart.NewSchema("require.schema.json", "file:///require.schema.json", doc)
	if err != nil {
		return nil, fmt.Errorf("require: %w", err)
	}
	return &requirement{doc, s}, nil
}

// requirements is part of stage 1. It checks the connection references of
// the parameter schema s, whether the parameters set them or not: the
// requirement of each must compile, the request each fixes of its claims
// must be an object, and none may sit where no compile would read it
// (under a combinator, a definition or additionalProperties). Every
// failure is reported, named by its place in the schema.
func requirements(s M) error {
	var errs []error
	out := schemaRefs(s, func(r ConnectionRef) {
		if _, err := readRequirement(r.ref, s["$schema"]); err != nil {
			errs = append(errs, fmt.Errorf("config.schema.json: %s: x-okdp-connection-ref: %w", r.Schema, err))
		}
		if v, set := r.ref["request"]; set && v != nil {
			if _, ok := v.(M); !ok {
				errs = append(errs, fmt.Errorf("config.schema.json: %s: x-okdp-connection-ref: request: an object, got %s", r.Schema, value.ShortAt("request", v)))
			}
		}
	})
	for _, at := range out {
		errs = append(errs, fmt.Errorf("config.schema.json: %s: x-okdp-connection-ref out of the compile's reach: a reference is a property reached from the root through properties and items only, not under a combinator (allOf, anyOf, oneOf), a definition ($defs, $ref) or additionalProperties", at))
	}
	return errors.Join(errs...)
}

// ConnectionRef is a connection reference of a parameter schema
// (x-okdp-connection-ref), where the compile finds it (ConnectionRefs).
type ConnectionRef struct {
	// Path is its parameter path, with [] for every item: catalogs[].storage.
	Path string
	// Schema is its place in the schema:
	// properties.catalogs.items.properties.storage.
	Schema    string
	Contract  string // the contract of the connection it takes
	Provision bool   // a claim may take its place
	Request   M      // what it fixes of its claims; nil for nothing
	Require   M      // what it requires of the connection, a JSON Schema; nil for nothing
	Required  bool   // its property is in its parent's required
	Property  M      // the whole schema of its property
	ref       M
}

// ConnectionRefs returns the connection references of the parameter schema
// s the compile reads, in key order, or the error of an invalid one.
func ConnectionRefs(s M) ([]ConnectionRef, error) {
	if err := requirements(s); err != nil {
		return nil, err
	}
	var out []ConnectionRef
	schemaRefs(s, func(r ConnectionRef) { out = append(out, r) })
	return out, nil
}

// schemaRefs calls visit on each connection reference of the parameter
// schema s that the compile reads: those reached from the root through
// properties and items, in key order. It returns the places of the
// references found anywhere else in s, sorted.
func schemaRefs(s M, visit func(ConnectionRef)) []string {
	var out []string
	var walk func(sub M, at, path string, required, reach bool)
	walk = func(sub M, at, path string, required, reach bool) {
		if ref, ok := sub["x-okdp-connection-ref"].(M); ok && reach {
			visit(ConnectionRef{
				Path:      path,
				Schema:    at,
				Contract:  value.Str(ref["contract"]),
				Provision: value.Truthy(ref["provision"]),
				Request:   value.AsMap(ref["request"]),
				Require:   value.AsMap(ref["require"]),
				Required:  required,
				Property:  sub,
				ref:       ref,
			})
			reach = false
		} else if _, ok := sub["x-okdp-connection-ref"]; ok {
			out = append(out, cmp.Or(at, "(the root)"))
		}
		need := map[string]bool{}
		for _, k := range value.AsList(sub["required"]) {
			need[value.Str(k)] = true
		}
		for _, k := range slices.Sorted(maps.Keys(sub)) {
			v := sub[k]
			next := strings.TrimPrefix(at+"."+k, ".")
			switch {
			case k == "x-okdp-connection-ref":
			case k == "properties" && reach:
				props := value.AsMap(v)
				for _, p := range slices.Sorted(maps.Keys(props)) {
					walk(value.AsMap(props[p]), next+"."+p, value.Key(path, p), need[p], true)
				}
			case k == "items" && reach:
				if items, ok := v.(M); ok {
					walk(items, next, path+"[]", false, true)
				} else {
					walkAny(v, next, func(m M, at string) { walk(m, at, "", false, false) })
				}
			default:
				walkAny(v, next, func(m M, at string) { walk(m, at, "", false, false) })
			}
		}
	}
	walk(s, "", "", false, true)
	slices.Sort(out)
	return out
}

// walkAny calls f on each object of v, itself or the items of a list.
func walkAny(v any, at string, f func(M, string)) {
	switch t := v.(type) {
	case M:
		f(t, at)
	case []any:
		for i, x := range t {
			walkAny(x, value.Index(at, i), f)
		}
	}
}

// requires checks the fields that req, the requirement of a reference to
// c at field, names in its properties and required. Each must be
// secretRef, or a field of the contract that is not secret: a secret
// field is in the credentials Secret, never in the connection.
func (c *contract) requires(req *requirement, field string) error {
	if req == nil {
		return nil
	}
	names := slices.Collect(maps.Keys(value.AsMap(req.doc["properties"])))
	for _, k := range value.AsList(req.doc["required"]) {
		names = append(names, value.Str(k))
	}
	slices.Sort(names)
	where := field + ": x-okdp-connection-ref: require"
	for _, k := range slices.Compact(names) {
		_, ok := c.props[k]
		switch {
		case k == "secretRef":
		case !ok:
			return fmt.Errorf("%s: field %q is not part of the %s contract", where, k, c.name)
		case c.secret[k]:
			return fmt.Errorf("%s: %q is a secret field, in the credentials Secret, never in the connection: require secretRef", where, k)
		}
	}
	return nil
}

// check validates values, the connection a reference resolves to with its
// secretRef, against the requirement. advice follows the heading.
//
//	<where>: the connection does not meet the package's requirement (x-okdp-connection-ref.require):
//	- at '/engine': value must be 'postgresql'
func (req *requirement) check(values M, where, advice string) error {
	if req == nil {
		return nil
	}
	v, err := req.schema.Validate(values)
	if err == nil && v != "" {
		err = fmt.Errorf("the connection does not meet the package's requirement (x-okdp-connection-ref.require)%s:\n%s", advice, v)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", where, err)
	}
	return nil
}

func (c *contract) secretFields() []string {
	if len(c.secret) == 0 {
		return nil
	}
	return slices.Sorted(maps.Keys(c.secret))
}

// fields checks that each key of values is a field of the contract and
// not a secret one: credentials are in the credentials Secret, never in
// the values.
func (c *contract) fields(values M, where string) error {
	for _, k := range slices.Sorted(maps.Keys(values)) {
		if _, ok := c.props[k]; !ok {
			return fmt.Errorf("%s: field %q is not part of the %s contract", where, k, c.name)
		}
		if c.secret[k] {
			return fmt.Errorf("%s: %q is a secret field, put it in the credentials Secret", where, k)
		}
	}
	return nil
}

// complete sets the contract's defaults in out, then its derived fields
// (x-okdp-derived), and repeats until nothing changes. The values it sets
// are copies, because a contract is read once for every compile that
// names it. It then reports every required field that is not set.
func (c *contract) complete(out M, where string) error {
	names := slices.Sorted(maps.Keys(c.props))
	// Defaults come first, because a derived field may come from a defaulted
	// one. The passes repeat because a conditional default may depend on a
	// field set after it. The loop ends because a field is set only once.
	for changed := true; changed; {
		changed = false
		for _, pass := range []string{"default", "derived"} {
			for _, k := range names {
				// Set fields are kept; secret fields stay in the Secret.
				if _, set := out[k]; set || c.secret[k] {
					continue
				}
				p := value.AsMap(c.props[k])
				derived, isDerived := p["x-okdp-derived"].(M)
				if pass == "derived" && isDerived {
					if src, ok := out[value.Str(derived["from"])]; ok {
						if mv, ok := value.AsMap(derived["map"])[value.Str(src)]; ok {
							out[k], changed = value.Copy(mv), true
						}
					}
				} else if pass == "default" && !isDerived {
					if d, ok := p["default"]; ok {
						// A conditional default applies only when its condition holds.
						cond := value.AsMap(p["x-ui-condition"])
						if len(cond) == 0 || value.Str(out[value.Str(cond["field"])]) == value.Str(cond["value"]) {
							out[k], changed = value.Copy(d), true
						}
					}
				}
			}
		}
	}
	// false is a value: only a missing or empty field fails. A secret field
	// is never set, and it is not in required.
	var errs []error
	for _, f := range c.required {
		if !value.Truthy(out[f]) && out[f] != false {
			errs = append(errs, fmt.Errorf("%s: field %q is required by the %s contract", where, f, c.label))
		}
	}
	return errors.Join(errs...)
}

// validate completes values against the contract (complete), then checks
// them (check).
func (c *contract) validate(values M, where string) error {
	if err := c.complete(values, where); err != nil {
		return err
	}
	return c.check(values, where)
}

// check validates values, the completed fields of a connection or an
// output, against the contract, as Helm reports a chart's values:
//
//	<where>: the fields violate the database contract:
//	- at '/port': got string, want integer
func (c *contract) check(values M, where string) error {
	v, err := c.schema.Validate(values)
	if err == nil && v != "" {
		err = fmt.Errorf("the fields violate the %s contract:\n%s", c.name, v)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", where, err)
	}
	return nil
}
