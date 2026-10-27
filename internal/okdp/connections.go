// Connections: the references declared in the parameter schema
// (x-okdp-connection-ref), resolved to an external connection or to an
// output another instance of the namespace publishes, completed against
// their contract, then checked against what the package requires of them
// (require); the contracts, read at each use (readContract), and their
// checks, which the outputs the instance provides go through too.

package okdp

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/okdp/okdp-compiler/internal/chart"
	"github.com/okdp/okdp-compiler/internal/value"
)

// resolver: resolves connection references against the contracts, the
// external connections of the instance, what the other instances of the
// namespace publish, recording those that point to an instance.
type resolver struct {
	contracts fs.FS              // Inputs.Contracts
	conns     M                  // the external connections
	published map[string]Info    // Inputs.Published, by release; nil when unknown
	release   string             // Inputs.Release: never its own provider
	namespace string
	draft     any          // the parameter schema's $schema: the draft of the requirements that name none
	deps      []Dependency // every reference to an instance resolved, in order
	errs      []error      // every reference that does not resolve, in order (walk)
	missing   []outputKey  // the outputs references name that no instance of published publishes, in order (output)
}

// outputKey: an output, by contract and name.
type outputKey struct{ contract, name string }

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
// that publishes the output the reference names (Inputs.Published). The
// instance must
// exist and publish an output of the contract by that name. Its fields are
// always set. What a deployer orders the releases with, read from
// Result.Dependencies: the deployment graph checks them, and makes the
// release wait for the instance (graph.New: Flux dependsOn, Argo CD sync
// waves).
type Dependency struct {
	Instance  string `json:"instance,omitempty" yaml:"instance,omitempty"`   // the referenced instance, or the one that publishes the referenced output (its release name)
	Contract  string `json:"contract,omitempty" yaml:"contract,omitempty"`   // the contract it must provide
	Field     string `json:"field,omitempty" yaml:"field,omitempty"`         // the parameter that references it, e.g. backends[0].database
	Output    string `json:"output,omitempty" yaml:"output,omitempty"`       // the output the reference resolved to, by name
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
// empty means not set; a string is a connection's name. Another value
// would reach the mapping unresolved, past the contract. What it resolves
// to meets its requirement, require (readRequirement).
func (r *resolver) reference(ref M, v any, path string) any {
	if v == nil || v == "" {
		return v
	}
	contract := value.Str(ref["contract"])
	// Checked with the parameter schema (requirements): it compiles.
	req, err := readRequirement(ref, r.draft)
	if err != nil {
		r.errs = append(r.errs, fmt.Errorf("%s: x-okdp-connection-ref: %w", path, err))
		return v
	}
	var c M
	switch t := v.(type) {
	case string:
		c, err = r.resolve(t, contract, path, req)
	default:
		err = fmt.Errorf("%s: a connection reference must be a string, got %s", path, value.Short(v))
	}
	if err != nil {
		r.errs = append(r.errs, err)
		return v
	}
	return c
}

// resolve: a reference to its contract fields. An entry of connections is
// external. Any other name is an output named ref of the contract that
// another instance of the namespace publishes (Published, the compiling
// release left out: output). The first
// error is reported: the contract, then the fields the requirement req
// names (contract.requires), then the output (several instances, none),
// the fields of an external connection or an output in alphabetical order
// (contract.fields), then the required fields, then the violations of the
// contract, all of them (contract.check), then those of the requirement,
// all of them (requirement.check), then "{{" in what it resolves to
// (chart.NoTemplate). A reference to an instance that resolves is
// recorded (deps), field naming it, ref the output.
func (r *resolver) resolve(ref, contract, field string, req *requirement) (M, error) {
	c, err := readContract(r.contracts, contract)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", field, err)
	}
	if err := c.requires(req, field); err != nil {
		return nil, err
	}
	out, instance := M{}, ""
	var source string
	var secretRef any
	where := fmt.Sprintf("%s (connection %q)", field, ref)
	if conn, ok := r.conns[ref].(M); ok {
		source = "external"
		if value.Str(conn["contract"]) != contract {
			return nil, fmt.Errorf("%s: connection %q has contract %q, %q is expected", field, ref, value.Str(conn["contract"]), contract)
		}
		for k, x := range conn {
			switch k {
			case "contract", "description":
			// Credentials are only referenced, never inlined.
			case "secretRef":
				secretRef = M{"name": value.AsMap(x)["name"]}
			default:
				out[k] = x
			}
		}
		if err := c.fields(out, fmt.Sprintf("%s: connection %q", field, ref)); err != nil {
			return nil, err
		}
	} else {
		// An output: its values as the context's layers give a connection,
		// in a copy, every number a float64 whatever decoded them.
		o, provider, err := r.output(ref, contract, field)
		if err != nil {
			return nil, err
		}
		source, instance = "output", provider
		from := fmt.Sprintf("output %q of %s", ref, provider)
		where = fmt.Sprintf("%s (%s)", field, from)
		if out, secretRef, err = c.published(o, field, from); err != nil {
			return nil, err
		}
	}
	if err := c.complete(out, where); err != nil {
		return nil, err
	}
	if err := c.check(out, where); err != nil {
		return nil, err
	}
	if secretRef != nil {
		out["secretRef"] = secretRef
	}
	if err := req.check(out, where); err != nil {
		return nil, err
	}
	// Where it comes from, for the mapping (e.g. error messages): the
	// instance too, unless external.
	from := M{"ref": ref, "contract": contract, "source": source}
	if instance != "" {
		from["instance"] = instance
	}
	out["okdp"] = from
	// No template code in what the mapping receives, whatever layer,
	// output or default it comes from.
	if err := chart.NoTemplate(out, func(p string) string { return fmt.Sprintf("%s: field %q", where, p) }); err != nil {
		return nil, err
	}
	if instance != "" {
		r.deps = append(r.deps, Dependency{Instance: instance, Contract: contract, Field: field, Output: ref})
	}
	return out, nil
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
	r.missing = append(r.missing, outputKey{contract, ref})
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
// complete) and resolve reads, and its validator (check).
type contract struct {
	name     string             // the name references and outputs give, that of its file <name>.schema.json
	label    string             // x-okdp-contract: its name in the error of a required field
	props    M                  // properties: the schema of each field
	secret   map[string]bool    // the fields marked x-okdp-secret: credentials, in the Secret, never in the values
	required []string           // the required fields, secret ones left out, sorted
	schema   *jsonschema.Schema // the validator of the values of its connections and outputs
}

// readContract: the contract name, the file <name>.schema.json of fsys
// (Inputs.Contracts), read by each reference and each output that names it,
// decoded once (jsonschema.UnmarshalJSON): the validator keeps the numbers
// as written, the keywords are read from its map view (value.Normalize:
// every number a float64). The validator is the schema compiled as a
// chart's (chart.CompileSchema, x-* keywords ignored), its secret fields
// left out of required, as they are in the credentials Secret, never in the
// values. An error naming the file and the cause when it is missing, cannot
// be read, is not a JSON object or not a valid JSON Schema.
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
	return c, nil
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
// the parameter schema s (readRequirement), whether the parameters set it
// or not, found as refs finds the references, through properties and
// items: every one that fails, one line each, in key order, named by its
// place in the schema (properties.catalogs.items.properties.database).
func requirements(s M) error {
	var errs []error
	var walk func(sub M, at string)
	walk = func(sub M, at string) {
		if ref, ok := sub["x-okdp-connection-ref"].(M); ok {
			if _, err := readRequirement(ref, s["$schema"]); err != nil {
				errs = append(errs, fmt.Errorf("config.schema.json: %s: x-okdp-connection-ref: %w", at, err))
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
// line each with its path, as contract.check reports them; nil req, none.
//
//	<where>: the connection does not meet the package's requirement (x-okdp-connection-ref.require):
//	- at '/engine': value must be 'postgresql'
func (req *requirement) check(values M, where string) error {
	if req == nil {
		return nil
	}
	v, err := chart.Validate("require.schema.json", req.schema, values)
	if err == nil && v != "" {
		err = fmt.Errorf("the connection does not meet the package's requirement (x-okdp-connection-ref.require):\n%s", v)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", where, err)
	}
	return nil
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
