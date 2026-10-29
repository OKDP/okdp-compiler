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
	claims    map[string]claimSpec // the named claims of the context (namedClaims)
	classes   M                    // global.okdp.provisioning, by contract: the default class of a claim that names none
	published map[string]Info      // Inputs.Published, by release; nil when unknown
	bindings  map[string]Binding   // Inputs.Bindings, by parameter path
	store     string               // global.okdp.secrets.store: the ClusterSecretStore the credentials of a claim of another namespace go through
	transfers []transfer           // those credentials, in order
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
// value.Index: a key with a dot in brackets). The keys the schema does not
// declare are left as they are.
func refs(s M, v any, path string, visit func(ref M, v any, path string) any) any {
	if ref, ok := s["x-okdp-connection-ref"].(M); ok {
		return visit(ref, v, path)
	}
	switch t := v.(type) {
	case M:
		props := value.AsMap(s["properties"])
		for _, k := range slices.Sorted(maps.Keys(props)) {
			if x, ok := t[k]; ok {
				t[k] = refs(value.AsMap(props[k]), x, value.Key(path, k), visit)
			}
		}
	case []any:
		// A list: the same, with the item schema.
		for i, x := range t {
			t[i] = refs(value.AsMap(s["items"]), x, value.Index(path, i), visit)
		}
	}
	return v
}

// walk: v, at path, of the schema s, with every reference replaced by the
// resolved connection (refs, reference). The error of a reference that
// does not resolve is recorded (errs).
func (r *resolver) walk(s M, v any, path string) any { return refs(s, v, path, r.reference) }

// reference: v, the value of a reference ref at path, resolved. Null or
// empty means not set; a string is a connection's name, or a named
// claim's; an object a claim template (parseClaim), when the reference
// takes them (provision). Another value
// would reach the mapping unresolved, past the contract. What it resolves
// to meets its requirement, require (readRequirement).
func (r *resolver) reference(ref M, v any, path string) any {
	if v == nil || v == "" {
		return v
	}
	contract, provision := value.Str(ref["contract"]), value.Truthy(ref["provision"])
	// Checked with the parameter schema (requirements): it compiles.
	req, err := readRequirement(ref, r.draft)
	if err != nil {
		r.errs = append(r.errs, fmt.Errorf("%s: x-okdp-connection-ref: %w", path, err))
		return v
	}
	var c M
	switch t := v.(type) {
	case string:
		c, err = r.resolve(t, contract, path, nil, req, provision)
	case M:
		if !provision {
			err = fmt.Errorf("%s: a connection reference must be a string, got %s", path, value.Short(v))
			break
		}
		var claim *claimSpec
		if claim, err = parseClaim(t, path, false); err == nil {
			c, err = r.resolve("", contract, path, claim, req, true)
		}
	default:
		err = fmt.Errorf("%s: a connection reference must be a string, got %s", path, value.Short(v))
	}
	if err != nil {
		r.errs = append(r.errs, err)
		return v
	}
	return c
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
// (contract.fields), then the required fields, then the violations of the
// contract, all of them (contract.check), then those of the requirement,
// all of them (requirement.check), then "{{" in what it resolves to
// (chart.NoTemplate). A reference to an instance that resolves is
// recorded (deps), field naming it, ref the output.
func (r *resolver) resolve(ref, contract, field string, claim *claimSpec, req *requirement, provision bool) (M, error) {
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
func (r *resolver) resolveNamed(c *contract, ref, field string, spec claimSpec, provision bool) (*resolved, error) {
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
func (r *resolver) resolveClaim(c *contract, ref, field string, claim *claimSpec) (*resolved, error) {
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
		r.transfers = append(r.transfers, transfer{secret: name, key: storeKey(b.ID), contract: contract, fields: fields})
		x.secretRef = M{"name": name}
	}
	return x, nil
}

// resolveExternal: the external connection ref, conn, of the contract c.
func (r *resolver) resolveExternal(c *contract, ref, field string, conn M) (*resolved, error) {
	x := &resolved{values: M{}, ref: ref, source: "external", where: fmt.Sprintf("%s (connection %q)", field, ref)}
	if value.Str(conn["contract"]) != c.name {
		return nil, fmt.Errorf("%s: connection %q has contract %q, %q is expected", field, ref, value.Str(conn["contract"]), c.name)
	}
	for k, v := range conn {
		switch k {
		case "contract", "description":
		// Credentials are only referenced, never inlined.
		case "secretRef":
			x.secretRef = M{"name": value.AsMap(v)["name"]}
		default:
			x.values[k] = v
		}
	}
	if err := c.fields(x.values, fmt.Sprintf("%s: connection %q", field, ref)); err != nil {
		return nil, err
	}
	return x, nil
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
			return nil, fail(fmt.Sprintf("x-okdp-claim: a JSON Schema, an object, got %s", value.Short(m["x-okdp-claim"])))
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
		return nil, fmt.Errorf("require: a JSON Schema, an object, got %s", value.Short(v))
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
// properties and items: every one that fails, one line each, in key order,
// named by its place in the schema
// (properties.catalogs.items.properties.database).
func requirements(s M) error {
	var errs []error
	var walk func(sub M, at string)
	walk = func(sub M, at string) {
		if ref, ok := sub["x-okdp-connection-ref"].(M); ok {
			if _, err := readRequirement(ref, s["$schema"]); err != nil {
				errs = append(errs, fmt.Errorf("config.schema.json: %s: x-okdp-connection-ref: %w", at, err))
			}
			if r, set := ref["request"]; set && r != nil {
				if _, ok := r.(M); !ok {
					errs = append(errs, fmt.Errorf("config.schema.json: %s: x-okdp-connection-ref: request: an object, got %s", at, value.Short(r)))
				}
			}
			return
		}
		props := value.AsMap(sub["properties"])
		for _, k := range slices.Sorted(maps.Keys(props)) {
			walk(value.AsMap(props[k]), strings.TrimPrefix(at+".properties."+k, "."))
		}
		if items, ok := sub["items"].(M); ok {
			walk(items, strings.TrimPrefix(at+".items", "."))
		}
	}
	walk(s, "")
	return errors.Join(errs...)
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
