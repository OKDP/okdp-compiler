// Connections: the references declared in the parameter schema
// (x-okdp-connection-ref), resolved to an external connection or to an
// instance of the namespace, and completed against their contract; the
// contracts, read at each use (readContract), and their checks, which the
// outputs the instance provides go through too.

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

// resolver: resolves connection references against the contracts and the
// external connections of the instance, recording the internal ones.
type resolver struct {
	contracts fs.FS // Inputs.Contracts
	conns     M
	namespace string
	suffix    string
	deps      []Dependency // every internal reference resolved, in order
	errs      []error      // every reference that does not resolve, in order (walk)
}

// Dependency: an instance of the same namespace that a connection reference
// of the parameters points to (an internal reference: the name of an
// instance, not of an external connection). The instance must exist and
// provide the contract; a deployment can wait for it (Flux dependsOn, Argo
// sync waves).
type Dependency struct {
	Instance string `json:"instance" yaml:"instance"` // the referenced instance (its release name)
	Contract string `json:"contract" yaml:"contract"` // the contract it must provide
	Field    string `json:"field" yaml:"field"`       // the parameter that references it, e.g. backends[0].database
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
// external; any other name is an instance of the namespace (internal), a
// release name. The first error is reported: the contract, then the name of
// an internal reference, the fields of an external connection in
// alphabetical order (contract.fields), then the required fields, then the
// violations of the contract by an external connection, all of them
// (contract.check), then "{{" in the connection (chart.NoTemplate). An
// internal reference that resolves is recorded (deps), field naming it.
func (r *resolver) resolve(ref, contract, field string) (M, error) {
	c, err := readContract(r.contracts, contract)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", field, err)
	}
	out, source := M{}, "internal"
	var secretRef any
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
		// An instance: its fields follow the contract's naming convention.
		if c.internal == nil {
			return nil, fmt.Errorf("%s: %q is not a connection, and the %s contract has no internal naming convention", field, ref, contract)
		}
		// Its name goes into hosts, Secret names and the dependency.
		if !chart.IsRelease(ref) {
			return nil, fmt.Errorf("%s: %q is not a connection, nor a release name: lowercase letters, digits and \"-\", a letter or digit at each end, %d characters at most", field, ref, chart.ReleaseMax)
		}
		for k, tpl := range c.internal {
			out[k] = strings.NewReplacer("${ref}", ref, "${namespace}", r.namespace, "${suffix}", r.suffix).Replace(value.Str(tpl))
		}
		// Its credentials Secret, when the contract has secret fields.
		if len(c.secret) > 0 {
			secretRef = M{"name": fmt.Sprintf("%s-%s-credentials", ref, contract)}
		}
	}
	where := fmt.Sprintf("%s (connection %q)", field, ref)
	if err := c.complete(out, where); err != nil {
		return nil, err
	}
	// An instance's fields follow from the contract; a connection's are
	// written by hand.
	if source == "external" {
		if err := c.check(out, where); err != nil {
			return nil, err
		}
	}
	if secretRef != nil {
		out["secretRef"] = secretRef
	}
	// Where it comes from, for the mapping (e.g. error messages).
	out["okdp"] = M{"ref": ref, "contract": contract, "source": source}
	// No template code in what the mapping receives, whatever layer or
	// default it comes from.
	if err := chart.NoTemplate(out, func(p string) string { return fmt.Sprintf("%s: field %q", where, p) }); err != nil {
		return nil, err
	}
	if source == "internal" {
		r.deps = append(r.deps, Dependency{Instance: ref, Contract: contract, Field: field})
	}
	return out, nil
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
	internal M                  // x-okdp-internal: the fields of an instance reached by its name, templates; nil without a convention
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
	c.internal, _ = m["x-okdp-internal"].(M)
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
