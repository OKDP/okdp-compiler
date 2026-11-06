// Connections: the references declared in the parameter schema
// (x-okdp-connection-ref), resolved to an external connection, to a named
// claim of the namespace, to an output another instance of the namespace
// publishes, or to the output of a claim's provider, completed against
// their contract, then checked against what the package requires of them
// (require); the contracts, read at each use (readContract), and their
// checks, which the outputs the instance provides go through too.

package okdp

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/okdp/okdp-compiler/internal/chart"
	"github.com/okdp/okdp-compiler/internal/value"
)

// resolver: resolves connection references against the contracts, the
// external connections of the instance, what the other instances of the
// namespace publish and the claims' bindings, recording those that point
// to an instance.
type resolver struct {
	contracts fs.FS                // Inputs.Contracts
	conns     M                    // the external connections
	levels    map[string]uint      // the level of the context each is given at, by name (connectionLevels)
	claims    map[string]ClaimSpec // the named claims of the context (namedClaims)
	classes   M                    // global.okdp.provisioning, by contract: the default class of a claim that names none
	published map[string]Info      // Inputs.Published, by release; nil when unknown
	bindings  map[string]Binding   // Inputs.Bindings, by parameter path
	store     string               // global.okdp.secrets.store: the store the credentials of a claim of another namespace go through, and those of a connection's secretRef.remoteKey are in
	provider  bool                 // global.okdp.secrets.provider is set: a SecretStore of each namespace, which reads no platform/ key
	transfers []transfer           // those credentials, in order, each Secret once (addTransfer)
	release   string               // Inputs.Release: never its own provider
	namespace string
	draft     any          // the parameter schema's $schema: the draft of the requirements that name none
	deps      []Dependency // every reference to an instance resolved, in order
	errs      []error      // every reference that does not resolve, in order (walk)
	missing   []outputKey  // the outputs references name that no instance of published publishes, in order (output)
}

// outputKey: an output, by namespace, "" the instance's own, contract and
// name; release, the instance of that namespace that publishes it when a
// claim's provider is known, "" for a name any instance of it may publish.
type outputKey struct{ namespace, contract, name, release string }

// missingOutputs: the error of a compile whose references name outputs no
// instance of Inputs.Published publishes (outputs, in order), err the
// compile's error as it is, which may say more: what CompileAll waits for
// before compiling the instance again.
type missingOutputs struct {
	outputs []outputKey
	err     error
}

func (e *missingOutputs) Error() string { return e.err.Error() }
func (e *missingOutputs) Unwrap() error { return e.err }

// Dependency: an instance that a connection reference of the parameters
// points to, not an external connection: the instance of the namespace
// that publishes the output the reference names (Inputs.Published), or the
// provider a claim is bound to, maybe of another namespace
// (Inputs.Bindings). The instance must
// exist and publish an output of the contract by that name. Its fields are
// all set, but Namespace, which is set for a provider of another namespace
// only. What a deployer orders the releases with, read from
// Result.Dependencies: the deployment graph checks them, and makes the
// release wait for the instance (graph.New: Flux dependsOn, Argo CD sync
// waves).
type Dependency struct {
	Instance  string `json:"instance,omitempty" yaml:"instance,omitempty"`   // the referenced instance, or the one that publishes the referenced output (its release name)
	Namespace string `json:"namespace,omitempty" yaml:"namespace,omitempty"` // the namespace of Instance when it is not the instance's: a claim bound to a provider of another namespace
	Contract  string `json:"contract,omitempty" yaml:"contract,omitempty"`   // the contract it must provide
	Field     string `json:"field,omitempty" yaml:"field,omitempty"`         // the parameter that references it, e.g. backends[0].database
	Output    string `json:"output,omitempty" yaml:"output,omitempty"`       // the output the reference resolved to, by name, a claim's ID
}

// refs: v, at path, of the schema s, each value carrying an
// x-okdp-connection-ref (ref) replaced by what visit gives for it, in path
// order (keys sorted, items in order), at its parameter path (value.Key,
// value.Index: a key with a dot in brackets) and under its key, an item's
// its list's, the key of a message's value.ShortAt. The keys the schema
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
		// A list: the same, with the item schema.
		for i, x := range t {
			t[i] = refs(value.AsMap(s["items"]), x, value.Index(path, i), key, visit)
		}
	}
	return v
}

// walk: v, the parameters, of the schema s, with every reference replaced
// by the resolved connection (refs, reference). The error of a reference
// that does not resolve is recorded (errs).
func (r *resolver) walk(s M, v any) any { return refs(s, v, "", "", r.reference) }

// reference: v, the value of a reference ref at path, under key, resolved.
// Null or empty means not set; a string is a connection's name, or a named
// claim's; an object a claim template (parseClaim), when the reference
// takes them (provision). Another value would reach the mapping
// unresolved, past the contract: refused, its value printed as a
// sensitive key's is (value.ShortAt). What it resolves to meets its
// requirement, require (readRequirement). An error is the field's
// (FieldError).
func (r *resolver) reference(ref M, v any, path, key string) any {
	if v == nil || v == "" {
		return v
	}
	contract, provision := value.Str(ref["contract"]), value.Truthy(ref["provision"])
	// Checked with the parameter schema (requirements): it compiles.
	req, err := readRequirement(ref, r.draft)
	if err != nil {
		r.errs = append(r.errs, atField(path, fmt.Errorf("%s: x-okdp-connection-ref: %w", path, err)))
		return v
	}
	var c M
	switch t := v.(type) {
	case string:
		c, err = r.resolve(t, contract, path, nil, req, provision)
	case M:
		if !provision {
			err = fmt.Errorf("%s: a connection reference must be a string, got %s", path, value.ShortAt(key, v))
			break
		}
		var claim *ClaimSpec
		if claim, err = parseClaim(t, path, false); err == nil {
			c, err = r.resolve("", contract, path, claim, req, true)
		}
	default:
		err = fmt.Errorf("%s: a connection reference must be a string, got %s", path, value.ShortAt(key, v))
	}
	if err != nil {
		r.errs = append(r.errs, atField(path, err))
		return v
	}
	return c
}

// declaredConnections: every connection of the context of in, of every
// level, referenced or not, checked at its declaration (declared), with
// its contracts, the store of its platform's settings: what CompileAll
// checks of each instance before it binds the claims. nil when the
// context's layers fail: the compile reports them (contextLayers).
func declaredConnections(in Inputs) error {
	layers, platform, err := contextLayers(in.Context)
	if err != nil {
		return nil //nolint:nilerr // the instance's compile reports it
	}
	conns := M{}
	for _, l := range layers {
		if c, ok := l["connections"].(M); ok {
			conns = value.Merge(conns, c)
		}
	}
	r := resolver{contracts: in.Contracts, conns: conns, levels: connectionLevels(in.Context), store: value.Str(value.Dig(platform, "global.okdp.secrets.store")), provider: len(value.AsMap(value.Dig(platform, "global.okdp.secrets.provider"))) > 0, release: in.Release, namespace: in.Namespace}
	return r.declared()
}

// CheckConnection: the connection name, conn as written in connections,
// declared at level of the context (0 the platform's, 1 a project's, 2
// an instance's) of namespace, checked as CompileAll checks it at its
// declaration (declared), store the platform's secret store
// (global.okdp.secrets.store), contracts the contracts: the error at
// connections.<name>, nil when it holds.
func CheckConnection(contracts fs.FS, name string, conn any, level uint, namespace, store string) error {
	v, err := value.Normalize(conn)
	if err != nil {
		return fmt.Errorf("%s: %w", value.Key("connections", name), err)
	}
	r := resolver{contracts: contracts, conns: M{name: v}, levels: map[string]uint{name: level}, store: store, namespace: namespace}
	return r.declared()
}

// declared: every connection of the context (conns), referenced or not,
// checked at its declaration as a reference to it would check it, in
// name order: its name a DNS-1123 label, an object of a contract,
// readable; then as resolveExternal, complete, check and resolve check
// it, its fields, its secretRef, its required fields, its types, "{{".
// Every one that fails, at once, each the connection's (FieldError at
// connections.<name>); nil when they all hold.
func (r *resolver) declared() error {
	var errs []error
	for _, name := range slices.Sorted(maps.Keys(r.conns)) {
		field := value.Key("connections", name)
		errs = append(errs, atField(field, r.declaration(name, field)))
	}
	return errors.Join(errs...)
}

// declaration: the connection name of the context, at field, checked
// (declared), the transfers it would need not recorded.
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
	c, err := readContract(r.contracts, value.Str(conn["contract"]))
	if err != nil {
		return fmt.Errorf("%s: %w", field, err)
	}
	d := *r
	d.transfers = nil
	x, err := d.resolveExternal(c, name, field, conn)
	if err != nil {
		return err
	}
	if err := c.complete(x.values, x.where); err != nil {
		return err
	}
	if err := c.check(x.values, x.where); err != nil {
		return err
	}
	return chart.NoTemplate(x.values, func(p string) string { return fmt.Sprintf("%s: field %q", x.where, p) })
}

// resolved: what a reference resolves to before the checks every source
// shares: the values, the secretRef of its connection, where it is for the
// messages, and what the mapping is told of its source (okdp).
type resolved struct {
	values    M
	secretRef any    // {name}, nil when none
	where     string // the reference in the messages: <field> (connection "<name>"), <field> (claim ... of <provider>), <field> (output ... of <instance>)
	ref       string // the connection's, the claim's ID or the output's name
	source    string // external, claim or output
	instance  string // the instance it comes from, "" when external
	namespace string // its namespace when it is not the compiling instance's
	advice    string // what a requirement it does not meet may add
}

// resolve: a reference to its contract fields. An entry of connections is
// external. An entry of claims, a named claim, is a claim, of the
// contract, when the reference takes claims (provision). Any other name is
// an output named ref of the contract that another instance of the
// namespace publishes (Published, the compiling release left out:
// output). A claim (claim, ref empty, or a named claim) resolves to the
// output its binding gives (Inputs.Bindings), as an output does. The first
// error is reported: the contract, then the fields the requirement req
// names (contract.requires), then the named claim (its contract, the
// reference's provision), then the binding (its contract), then the output (several instances, none),
// the fields of an external connection or an output in alphabetical order
// (contract.fields), then an external connection's secretRef.remoteKey
// (storeSecret), then the required fields, then the violations of the
// contract, all of them (contract.check), then those of the requirement,
// all of them (requirement.check), then "{{" in what it resolves to
// (chart.NoTemplate). A reference to an instance that resolves is
// recorded (deps), field naming it, ref the output.
func (r *resolver) resolve(ref, contract, field string, claim *ClaimSpec, req *requirement, provision bool) (M, error) {
	c, err := readContract(r.contracts, contract)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", field, err)
	}
	if err := c.requires(req, field); err != nil {
		return nil, err
	}
	var x *resolved
	named, isNamed := r.claims[ref]
	switch conn, external := r.conns[ref].(M); {
	case isNamed && claim == nil && r.conns[ref] == nil:
		x, err = r.resolveNamed(c, ref, field, named, provision)
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
	if err := c.complete(out, x.where); err != nil {
		return nil, err
	}
	if err := c.check(out, x.where); err != nil {
		return nil, err
	}
	if x.secretRef != nil {
		out["secretRef"] = x.secretRef
	}
	if err := req.check(out, x.where, x.advice); err != nil {
		return nil, err
	}
	// Where it comes from, for the mapping (e.g. error messages): the
	// instance too, unless external.
	from := M{"ref": x.ref, "contract": c.name, "source": x.source}
	if x.instance != "" {
		from["instance"] = x.instance
	}
	if x.namespace != "" {
		from["namespace"] = x.namespace
	}
	out["okdp"] = from
	// No template code in what the mapping receives, whatever layer,
	// output or default it comes from.
	if err := chart.NoTemplate(out, func(p string) string { return fmt.Sprintf("%s: field %q", x.where, p) }); err != nil {
		return nil, err
	}
	if x.instance != "" {
		r.deps = append(r.deps, Dependency{Instance: x.instance, Namespace: x.namespace, Contract: c.name, Field: field, Output: x.ref})
	}
	return out, nil
}

// resolveNamed: the named claim ref, declared as spec, a claim of the
// contract c, when the reference takes claims (provision).
func (r *resolver) resolveNamed(c *contract, ref, field string, spec ClaimSpec, provision bool) (*resolved, error) {
	switch {
	case !provision:
		return nil, fmt.Errorf("%s: %q is a claim: the reference takes none (x-okdp-connection-ref's provision)", field, ref)
	case spec.Contract != c.name:
		return nil, fmt.Errorf("%s: claim %q has contract %q, %q is expected", field, ref, spec.Contract, c.name)
	}
	return r.resolveClaim(c, ref, field, &spec)
}

// resolveClaim: the claim at field, as written (claim), to the output its
// binding gives, its credentials a copy of the instance's own when the
// provider is of another namespace.
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
		r.missing = append(r.missing, outputKey{b.Namespace, contract, b.ID, b.Release})
		return nil, fmt.Errorf("%s: claim %q (%s): %s publishes no output for it", field, b.ID, contract, provider)
	}
	x := &resolved{ref: b.ID, source: "claim", instance: b.Release}
	if b.Namespace != r.namespace {
		x.namespace = b.Namespace
	}
	from := fmt.Sprintf("claim %q of %s", b.ID, provider)
	x.where = fmt.Sprintf("%s (%s)", field, from)
	// The default class: another one's provider may meet the
	// requirement.
	if claim.Provider == "" && claim.Class == "" {
		key := provisioningKey(contract)
		class := value.Str(value.AsMap(r.classes[contract])["default"])
		x.advice = fmt.Sprintf("; %s is the provider of the default class %s of %s connections (%s.default): name a class whose provider meets it (class), or change the default", provider, class, contract, key)
	}
	var err error
	if x.values, x.secretRef, err = c.published(*b.Output, field, from); err != nil {
		return nil, err
	}
	// Credentials of another namespace: a copy of the instance's own.
	if fields := c.secretFields(); x.namespace != "" && x.secretRef != nil && fields != nil {
		if r.store == "" {
			return nil, fmt.Errorf("%s: claim %q (%s): of %s, another namespace: its credentials go through the platform's secret store, global.okdp.secrets.store, which is not set", field, b.ID, contract, provider)
		}
		name := r.release + "-" + b.Output.SecretRef.Name
		r.addTransfer(transfer{secret: name, key: storeKey(b.Namespace, b.ID), contract: contract, fields: fields})
		x.secretRef = M{"name": name}
	}
	return x, nil
}

// resolveExternal: the external connection ref, conn, of the contract c,
// its fields checked (contract.fields), then its secretRef: {name}, or
// {remoteKey}, its credentials in the platform's secret store
// (storeSecret).
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

// connectionFields: the fields of the contract that the external
// connection conn holds, its keys but contract, description and
// secretRef, and its secretRef, nil when it has none: credentials are
// only referenced, never inlined.
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

// Connection: an external connection, connections.<name> of the context
// (the platform's, a project's, an instance's), as written: the contract
// it is of, a description, the contract's fields, and where its secret
// fields are (secretRef): the Secret of the namespace of each instance
// that references it (SecretRef, its name), or a key of the platform's
// secret store, global.okdp.secrets.store (RemoteKey).
type Connection struct {
	Name        string         // its key in connections
	Contract    string         // contract
	Description string         // description, "" when none
	Values      map[string]any // its other keys, the contract's fields, not the secret ones
	SecretRef   string         // secretRef.name, "" when none
	RemoteKey   string         // secretRef.remoteKey, "" when none
}

// ParseConnection: the connection name, v, as written in connections; what
// it holds is checked by the compile of each instance that references it
// (resolveExternal).
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

// Entry: the connection as written in connections.<name>, values: its
// contract, its description and its secretRef when set, then the
// contract's fields of Values, but those keys.
func (c Connection) Entry() map[string]any {
	out := M{}
	for k, v := range c.Values {
		if k != "contract" && k != "description" && k != "secretRef" {
			out[k] = v
		}
	}
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

// MarshalYAML: the connection as written in connections.<name>, its own
// keys first, contract, description and secretRef, then the contract's
// fields, sorted, for a YAML encoder of the yaml.v3 family.
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
	entry.Fields = M{}
	for k, v := range c.Values {
		if k != "contract" && k != "description" && k != "secretRef" {
			entry.Fields[k] = v
		}
	}
	return entry, nil
}

// storeSecret: the Secret of the instance's namespace the credentials of
// the external connection ref, of the contract c, are read into from the
// platform's secret store, under the key of its secretRef, {remoteKey}:
// <release>.<ref>-credentials, the release and the connection's name,
// DNS-1123 labels, joined by a dot, which neither holds, so that no other
// connection's Secret takes the name, nor a claim's: <release>-<secret>
// across namespaces; within one, the provider's
// <id>-<contract>-credentials, its ID <namespace>.<...>, and a tree's
// release is <namespace>-<instance>. Recorded once (addTransfer), each
// secret field of the contract a property of the key. The key is a path
// of the store (isStorePath); a connection above level 0, a project's or
// an instance's, names one under its namespace, <namespace>/<rest>,
// where its own PushSecrets or an administrator put it (storeKey): the
// platform's keys are read through the platform's connections, and
// another namespace's never. The first that fails, in order: remoteKey
// with a name; remoteKey not a string, or empty; "{{" in it; no store;
// the connection's name not a DNS-1123 label; a contract without secret
// field; a key that is no path; a key outside the namespace; a key under
// platform/ when the platform gives the store's provider (a SecretStore of
// each namespace, which no policy lets read platform/: the platform's
// credentials are copied under the keys of the namespaces that use them).
func (r *resolver) storeSecret(c *contract, ref, field string, secretRef M) (string, error) {
	where := fmt.Sprintf("%s: connection %q: secretRef", field, ref)
	key, ok := secretRef["remoteKey"].(string)
	fields := c.secretFields()
	switch {
	case secretRef["name"] != nil:
		return "", fmt.Errorf("%s: name or remoteKey, not both", where)
	case !ok || key == "":
		// A key of the store names credentials, it holds none: printed as
		// the messages below print it (value.Short, not ShortAt, which
		// reads remoteKey as an API key).
		return "", fmt.Errorf("%s.remoteKey: the key of the credentials in the platform's secret store, a non-empty string, got %s", where, value.Short(secretRef["remoteKey"]))
	}
	if err := chart.NoTemplate(M{"secretRef": M{"remoteKey": key}}, func(p string) string { return fmt.Sprintf("%s (connection %q): field %q", field, ref, p) }); err != nil {
		return "", err
	}
	level := r.levels[ref]
	switch {
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
	name := r.release + "." + ref + "-credentials"
	r.addTransfer(transfer{secret: name, key: key, contract: c.name, fields: fields})
	return name, nil
}

// isStorePath: key is a path of the platform's secret store, as the
// compiler writes them (storeKey): segments separated by "/", none empty,
// "." or "..", so that no key reaches past its first segment, the one a
// store's policy scopes.
func isStorePath(key string) bool {
	for s := range strings.SplitSeq(key, "/") {
		if s == "" || s == "." || s == ".." {
			return false
		}
	}
	return true
}

// connectionLevels: the level of the context each connection is given at
// (Inputs.Context), by name: the lowest level that gives it, a lower
// level's connection being taken whole (contextLayers), which the
// resolver scopes a secretRef.remoteKey by (storeSecret). A layer that
// values cannot hold gives none: the layers failed before (valuesLayers).
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

// addTransfer: x recorded, unless a transfer of its Secret is already: two
// references to one connection, or to one named claim, read its
// credentials into one Secret, by one ExternalSecret.
func (r *resolver) addTransfer(x transfer) {
	if !slices.ContainsFunc(r.transfers, func(t transfer) bool { return t.secret == x.secret }) {
		r.transfers = append(r.transfers, x)
	}
}

// resolveOutput: the output ref of the contract c that another instance
// of the namespace publishes, its values as the context's layers give a
// connection, in a copy, every number a float64 whatever decoded them.
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

// published: the values of an output o, as a reference gets them: a copy,
// read as values are (every number a float64), each field of the contract
// and not a secret one, from naming it in messages; and its secretRef,
// {name}, nil when it has none.
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

// output: the output named ref of the contract that an instance of
// Published publishes, the compiling release left out, and that instance. Several instances fail, named in
// release order; none fails, saying, with Published, which instances
// publish an output of that name of another contract, in release order,
// and records the output (missing).
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
	// One release, one output of a contract and name (twice).
	switch providers = slices.Compact(providers); {
	case len(providers) == 1:
		return found, providers[0], nil
	case len(providers) > 1:
		return Output{}, "", fmt.Errorf("%s: %q names %s outputs of several instances of the namespace: %s", field, ref, contract, strings.Join(providers, ", "))
	}
	r.missing = append(r.missing, outputKey{"", contract, ref, ""})
	msg := fmt.Sprintf("%s: %q is not a connection", field, ref)
	if r.published != nil {
		msg += ", nor an output of an instance of the namespace"
		if others != nil {
			msg += fmt.Sprintf(" (%s, %q is expected)", strings.Join(others, ", "), contract)
		}
	}
	return Output{}, "", errors.New(msg)
}

// contract: a contract, the JSON Schema of a kind of connection, as each
// use reads it (readContract): the keywords its checks read (fields,
// complete) and resolve reads, and its validator (check); its claim
// schema, what a claim may request of a provider (request).
type contract struct {
	name     string             // the name references and outputs give, that of its file <name>.schema.json
	label    string             // x-okdp-contract: its name in the error of a required field
	props    M                  // properties: the schema of each field
	secret   map[string]bool    // the fields marked x-okdp-secret: credentials, in the Secret, never in the values
	required []string           // the required fields, secret ones left out, sorted
	schema   *jsonschema.Schema // the validator of the values of its connections and outputs
	claim    *chart.Schema      // x-okdp-claim: the schema of a claim's request, its defaults; nil when it declares none
}

// readContract: the contract name, the file <name>.schema.json of fsys
// (Inputs.Contracts), read by each reference and each output that names it,
// decoded once (jsonschema.UnmarshalJSON): the validator keeps the numbers
// as written, the keywords are read from its map view (value.Normalize:
// every number a float64). The validator is the schema compiled as a
// chart's (chart.CompileSchema, x-* keywords ignored), its secret fields
// left out of required, as they are in the credentials Secret, never in the
// values. Its claim schema, x-okdp-claim, is read as a values schema
// (chart.ParseSchema), in the contract's draft unless it names its own. An
// error naming the file and the cause when it is missing, cannot be read,
// is not a JSON object or not a valid JSON Schema, or its claim schema is
// not.
func readContract(fsys fs.FS, name string) (*contract, error) {
	file := name + ".schema.json"
	b, err := fs.ReadFile(fsys, file)
	fail := func(cause any) error {
		return fmt.Errorf("the %s contract cannot be read: %s: %v", name, file, cause)
	}
	if err != nil {
		// The file is named once.
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
	// Removed: a reference names an output its provider publishes.
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
	if c.schema, err = chart.CompileSchema("file:///"+file, d); err != nil {
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

// request: req, what a claim at field requests of a provider of the
// contract, its claim schema's defaults applied (chart.Schema.Values: as
// the parameters' are), checked against it: every violation, one line
// each with its path. A contract without claim schema takes no request:
// nothing, or an empty one.
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

// requirement: what a connection reference requires of the connection it
// resolves to, beyond its contract (x-okdp-connection-ref's require): a
// JSON Schema of its fields and secretRef, as written (doc) and compiled
// (schema).
type requirement struct {
	doc    M
	schema *jsonschema.Schema
}

// readRequirement: the requirement of the reference ref, compiled as a
// contract is (chart.CompileSchema), in the draft of the parameter schema
// (draft, its $schema) unless it names its own; nil when it has none, or
// null. One that is not an object or not a valid JSON Schema fails.
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
	s, err := chart.CompileSchema("file:///require.schema.json", doc)
	if err != nil {
		return nil, fmt.Errorf("require: %w", err)
	}
	return &requirement{doc, s}, nil
}

// requirements: stage 1, the requirement of each connection reference of
// the parameter schema s (readRequirement) and the request it fixes of its
// claims, an object (x-okdp-connection-ref's request), whether the
// parameters set it or not, found as refs finds the references, through
// properties and items (schemaRefs): every one that fails, one line each,
// in key order, named by its place in the schema
// (properties.catalogs.items.properties.database); then each reference
// the walk does not reach, under a combinator, a definition or
// additionalProperties, which no compile would read.
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

// ConnectionRef: a connection reference of a parameter schema
// (x-okdp-connection-ref), where the compile finds it (ConnectionRefs).
type ConnectionRef struct {
	Path      string // its parameter path, value.Key's, [] for every item of a list: catalogs[].storage
	Schema    string // its place in the schema: properties.catalogs.items.properties.storage
	Contract  string // the contract of the connection it takes
	Provision bool   // a claim may take its place, a claim template or a named claim
	Request   M      // what it fixes of its claims, nil for nothing
	Require   M      // what it requires of the connection, a JSON Schema, nil for nothing
	Required  bool   // its property is in its parent's required
	Property  M      // the schema of its property, whole: its description, its default
	ref       M
}

// ConnectionRefs: the connection references of the parameter schema s,
// as the compile reads them (schemaRefs), in key order; the error of
// requirements when one fails, or when s holds one the compile does not
// reach.
func ConnectionRefs(s M) ([]ConnectionRef, error) {
	if err := requirements(s); err != nil {
		return nil, err
	}
	var out []ConnectionRef
	schemaRefs(s, func(r ConnectionRef) { out = append(out, r) })
	return out, nil
}

// schemaRefs: visit each connection reference of the parameter schema s
// the compile reads (refs), reached from the root through properties and
// items (an object), in key order: at its parameter path, its place in
// the schema. The places of those it does not reach, anywhere else in s,
// sorted.
func schemaRefs(s M, visit func(ConnectionRef)) []string {
	var out []string
	var walk func(sub M, at, path string, required, reach bool)
	walk = func(sub M, at, path string, required, reach bool) {
		if ref, ok := sub["x-okdp-connection-ref"].(M); ok && reach {
			visit(ConnectionRef{Path: path, Schema: at, Contract: value.Str(ref["contract"]), Provision: value.Truthy(ref["provision"]), Request: value.AsMap(ref["request"]), Require: value.AsMap(ref["require"]), Required: required, Property: sub, ref: ref})
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

// walkAny: f on each object of v, a schema's value, at its place: v
// itself, or each item of a list.
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

// requires: nil when req, the requirement of a reference of c at field,
// names the fields the connection has, those of its properties and
// required: each a field of the contract, not a secret one, which is in
// the credentials Secret, never in the connection; or secretRef. The
// first that is not, in alphabetical order, fails. nil when req is nil.
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

// check: values, the connection a reference resolves to, completed, its
// secretRef included, against the requirement req: every violation, one
// line each with its path, as contract.check reports them, advice after
// the heading; nil req, none.
//
//	<where>: the connection does not meet the package's requirement (x-okdp-connection-ref.require):
//	- at '/engine': value must be 'postgresql'
func (req *requirement) check(values M, where, advice string) error {
	if req == nil {
		return nil
	}
	v, err := chart.Validate("require.schema.json", req.schema, values)
	if err == nil && v != "" {
		err = fmt.Errorf("the connection does not meet the package's requirement (x-okdp-connection-ref.require)%s:\n%s", advice, v)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", where, err)
	}
	return nil
}

// secretFields: the secret fields of the contract, sorted; nil when none.
func (c *contract) secretFields() []string {
	if len(c.secret) == 0 {
		return nil
	}
	return slices.Sorted(maps.Keys(c.secret))
}

// fields: values, the fields of an external connection or of an output,
// in key order, each a field of the contract and not a secret one: the
// credentials are in the credentials Secret, never in the values. The
// first that is not is reported, under where.
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

// complete: the contract's defaults set in out, then its derived fields,
// again until nothing changes, then the required check: every required
// field not set, one line each, in alphabetical order, as the violations of
// check.
func (c *contract) complete(out M, where string) error {
	names := slices.Sorted(maps.Keys(c.props))
	// Defaults first: a derived field may come from a defaulted one. Until
	// nothing changes: a conditional default may depend on a field set
	// after it, one that sorts after it or a derived one. A field is set
	// once: the loop ends.
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
							out[k], changed = mv, true
						}
					}
				} else if pass == "default" && !isDerived {
					if d, ok := p["default"]; ok {
						// A conditional default applies only when its condition holds.
						cond := value.AsMap(p["x-ui-condition"])
						if len(cond) == 0 || value.Str(out[value.Str(cond["field"])]) == value.Str(cond["value"]) {
							out[k], changed = d, true
						}
					}
				}
			}
		}
	}
	// false is a value: only missing or empty fails. A secret field is never
	// set: it is not in required.
	var lines []string
	for _, f := range c.required {
		if !value.Truthy(out[f]) && out[f] != false {
			lines = append(lines, fmt.Sprintf("%s: field %q is required by the %s contract", where, f, c.label))
		}
	}
	if len(lines) > 0 {
		return errors.New(strings.Join(lines, "\n"))
	}
	return nil
}

// check: values, the fields of a connection or an output once completed,
// against the contract's validator: every violation, one line each with its
// path, as Helm prints those of a chart's values (chart.Validate):
//
//	<where>: the fields violate the database contract:
//	- at '/port': got string, want integer
func (c *contract) check(values M, where string) error {
	v, err := chart.Validate(c.name+".schema.json", c.schema, values)
	if err == nil && v != "" {
		err = fmt.Errorf("the fields violate the %s contract:\n%s", c.name, v)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", where, err)
	}
	return nil
}
