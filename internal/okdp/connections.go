// Connections: the references declared in the parameter schema
// (x-okdp-connection-ref), resolved to an external connection or to an
// output another instance of the namespace publishes, and completed
// against their contract; the contracts, read at each use (readContract),
// and their checks, which the outputs the instance provides go through
// too.

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
// external connections of the instance and what the other instances of
// the namespace publish, recording those that point to an instance.
type resolver struct {
	contracts fs.FS           // Inputs.Contracts
	conns     M               // the external connections
	published map[string]Info // Inputs.Published, by release; nil when unknown
	release   string          // Inputs.Release: never its own provider
	namespace string
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
	Instance string `json:"instance,omitempty" yaml:"instance,omitempty"` // the referenced instance, or the one that publishes the referenced output (its release name)
	Contract string `json:"contract,omitempty" yaml:"contract,omitempty"` // the contract it must provide
	Field    string `json:"field,omitempty" yaml:"field,omitempty"`       // the parameter that references it, e.g. backends[0].database
	Output   string `json:"output,omitempty" yaml:"output,omitempty"`     // the output the reference resolved to, by name
}

// walk: v, at path, of the schema s, with every string carrying an
// x-okdp-connection-ref replaced by the resolved connection, in path order
// (keys sorted, items in order). Null or empty means not set; another value
// than a string would reach the mapping unresolved, past the contract. The
// error of a reference that does not resolve is recorded (errs), at its
// parameter path (value.Key, value.Index: a key with a dot in brackets).
func (r *resolver) walk(s M, v any, path string) any {
	if ref, ok := s["x-okdp-connection-ref"].(M); ok {
		name, isString := v.(string)
		if v != nil && !isString {
			r.errs = append(r.errs, fmt.Errorf("%s: a connection reference must be a string, got %s", path, value.Short(v)))
		} else if name != "" {
			c, err := r.resolve(name, value.Str(ref["contract"]), path)
			if err == nil {
				return c
			}
			r.errs = append(r.errs, err)
		}
		return v
	}
	switch t := v.(type) {
	case M:
		// Only the keys the schema declares: the others are left as they are.
		props := value.AsMap(s["properties"])
		for _, k := range slices.Sorted(maps.Keys(props)) {
			if x, ok := t[k]; ok {
				t[k] = r.walk(value.AsMap(props[k]), x, value.Key(path, k))
			}
		}
	case []any:
		// A list: the same, with the item schema.
		for i, x := range t {
			t[i] = r.walk(value.AsMap(s["items"]), x, value.Index(path, i))
		}
	}
	return v
}

// resolve: a reference to its contract fields. An entry of connections is
// external. Any other name is an output named ref of the contract that
// another instance of the namespace publishes (Published, the compiling
// release left out: output). The first
// error is reported: the contract, then the output (several instances,
// none), the fields of an external connection or an output in
// alphabetical order (contract.fields), then the required fields, then the
// violations of the contract, all of them (contract.check), then "{{" in
// what it resolves to (chart.NoTemplate). A reference to an instance that
// resolves is recorded (deps), field naming it, ref the output.
func (r *resolver) resolve(ref, contract, field string) (M, error) {
	c, err := readContract(r.contracts, contract)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", field, err)
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
