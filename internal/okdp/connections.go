// Connections: the references declared in the parameter schema
// (x-okdp-connection-ref), resolved to an external connection or to an
// instance of the namespace, and completed against their contract; the
// contracts, each read once per compile (contractCache), and their checks,
// which the outputs of the descriptor go through too.
package okdp

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"

	"github.com/okdp/okdp-compiler/internal/chart"
	"github.com/okdp/okdp-compiler/internal/helmfn"
	"github.com/okdp/okdp-compiler/internal/value"
)

// resolver: resolves connection references against the contracts and the
// external connections of the instance, recording the internal ones.
type resolver struct {
	contracts *contractCache // the compile's, shared with the descriptor
	conns     M
	namespace string
	suffix    string
	deps      []Dependency // every internal reference resolved, in order
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

// resolveAll: every connection reference of the parameters params, declared
// in the parameter schema, resolved in place (walk). The references that do
// not resolve are reported together, one error each, one per line, sorted
// by parameter path (string order, as Result.Dependencies), as the
// violations of the parameter schema are.
func (r *resolver) resolveAll(schema, params M) error {
	failed := map[string]error{}
	r.walk(schema, params, "", failed)
	errs := make([]error, 0, len(failed))
	for _, p := range slices.Sorted(maps.Keys(failed)) {
		errs = append(errs, failed[p])
	}
	return errors.Join(errs...)
}

// walk follows the parameter schema and replaces every string carrying an
// x-okdp-connection-ref with the resolved connection (okdp.connection), in
// path order (keys sorted, items in order); the error of a reference that
// does not resolve, or is neither a string nor null, is recorded in failed,
// by parameter path (value.Key, value.Index: a key with a dot in brackets).
func (r *resolver) walk(schema M, v any, path string, failed map[string]error) {
	// visit: x, at p, of the schema s; set replaces it with its connection.
	visit := func(s M, x any, p string, set func(M)) {
		ref, ok := s["x-okdp-connection-ref"].(M)
		if !ok {
			r.walk(s, x, p, failed)
			return
		}
		// A reference: replaced by the connection. Null or empty means not
		// set; another value would reach the mapping unresolved, past the
		// contract.
		name, isString := x.(string)
		if x != nil && !isString {
			failed[p] = fmt.Errorf("%s: a connection reference must be a string, got %s", p, value.Short(x))
			return
		}
		if name != "" {
			c, err := r.resolve(name, value.Str(ref["contract"]), p)
			if err != nil {
				failed[p] = err
				return
			}
			set(c)
		}
	}
	switch t := v.(type) {
	case M:
		// Only the keys the schema declares: the others are left as they are.
		props := value.AsMap(schema["properties"])
		for _, k := range slices.Sorted(maps.Keys(props)) {
			x, present := t[k]
			if !present {
				continue
			}
			visit(value.AsMap(props[k]), x, value.Key(path, k), func(c M) { t[k] = c })
		}
	case []any:
		// A list: the same, with the item schema.
		items := value.AsMap(schema["items"])
		for i, x := range t {
			visit(items, x, value.Index(path, i), func(c M) { t[i] = c })
		}
	}
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
	c, err := r.contracts.get(contract)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%s: unknown contract %q", field, contract)
	}
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
		if !helmfn.IsRelease(ref) {
			return nil, fmt.Errorf("%s: %q is not a connection, nor a release name: lowercase letters, digits and \"-\", a letter or digit at each end, %d characters at most", field, ref, helmfn.ReleaseMax)
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

// contract: a contract, the JSON Schema of a kind of connection, as a
// compile reads it once (contractCache): the keywords its checks read
// (fields, complete) and resolve reads, and its validator (check).
type contract struct {
	name     string             // the name references and outputs give, that of its file <name>.schema.json
	label    string             // x-okdp-contract: its name in the error of a required field
	props    M                  // properties: the schema of each field
	secret   map[string]bool    // the fields marked x-okdp-secret: credentials, in the Secret, never in the values
	required []string           // the required fields, secret ones left out, sorted
	internal M                  // x-okdp-internal: the fields of an instance reached by its name, templates; nil without a convention
	schema   *jsonschema.Schema // the validator of the values of its connections and outputs
}

// contractCache: the contracts of one compile, each read once from fsys
// (Inputs.Contracts), or the error of its read: every reference and every
// output that names it gets the same contract, or the same error.
type contractCache struct {
	fsys   fs.FS
	byName map[string]*contract
	errs   map[string]error
}

// newContractCache: the contracts of fsys, none read yet.
func newContractCache(fsys fs.FS) *contractCache {
	return &contractCache{fsys: fsys, byName: map[string]*contract{}, errs: map[string]error{}}
}

// get: the contract name, read the first time it is asked for
// (readContract): an error wrapping fs.ErrNotExist when there is no such
// contract.
func (cs *contractCache) get(name string) (*contract, error) {
	c, read := cs.byName[name]
	if !read {
		var err error
		c, err = readContract(cs.fsys, name)
		cs.byName[name], cs.errs[name] = c, err
	}
	return c, cs.errs[name]
}

// readContract: the contract name, the file <name>.schema.json of fsys,
// decoded once (jsonschema.UnmarshalJSON): the validator keeps the numbers
// as written, the keywords are read from its map view (value.Normalize:
// every number a float64). The validator is the schema compiled as a
// chart's (chart.CompileSchema, x-* keywords ignored), its secret fields
// left out of required, as they are in the credentials Secret, never in the
// values. An error wrapping fs.ErrNotExist when there is no such file;
// otherwise one naming the file and the cause when it cannot be read, is
// not a JSON object or not a valid JSON Schema.
func readContract(fsys fs.FS, name string) (*contract, error) {
	file := name + ".schema.json"
	b, err := fs.ReadFile(fsys, file)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	// Not wrapped: a contract that cannot be read is not unknown.
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
// against the contract's validator: every violation, one line each, sorted
// by field then message:
//
//	<where>: field "port" violates the database contract: got string, want integer
//
// The field is a path in the values (tls.mode, catalogs[1]); a violation by
// the values as a whole (minProperties) reads "the fields violate the
// <contract> contract". Every keyword that fails is a violation, those
// under a $ref or an allOf included; a failed anyOf or oneOf is one.
func (c *contract) check(values M, where string) error {
	err := c.schema.Validate(any(values))
	if err == nil {
		return nil
	}
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return fmt.Errorf("%s: %w", where, err)
	}
	type violation struct{ field, msg string }
	var found []violation
	var visit func(e *jsonschema.ValidationError)
	visit = func(e *jsonschema.ValidationError) {
		switch e.ErrorKind.(type) {
		case *kind.Schema, *kind.Group, *kind.Reference, *kind.AllOf:
			if len(e.Causes) > 0 {
				for _, cause := range e.Causes {
					visit(cause)
				}
				return
			}
		}
		// The message alone: the error of the keyword, at no location.
		msg := (&jsonschema.ValidationError{ErrorKind: e.ErrorKind}).Error()
		found = append(found, violation{fieldPath(values, e.InstanceLocation), strings.TrimPrefix(msg, "at '': ")})
	}
	visit(ve)
	slices.SortFunc(found, func(a, b violation) int {
		return cmp.Or(strings.Compare(a.field, b.field), strings.Compare(a.msg, b.msg))
	})
	lines := make([]string, len(found))
	for i, v := range found {
		if v.field == "" {
			lines[i] = fmt.Sprintf("%s: the fields violate the %s contract: %s", where, c.name, v.msg)
		} else {
			lines[i] = fmt.Sprintf("%s: field %q violates the %s contract: %s", where, v.field, c.name, v.msg)
		}
	}
	return errors.New(strings.Join(lines, "\n"))
}

// fieldPath: the location loc in v, the tokens of a JSON pointer, as a path
// (value.Key, value.Index): tls.mode, catalogs[1]. A token where v holds a
// list is the index of one of its items: the validator reports what it was
// given.
func fieldPath(v any, loc []string) string {
	p := ""
	for _, tok := range loc {
		if l, ok := v.([]any); ok {
			if i, err := strconv.Atoi(tok); err == nil && i >= 0 && i < len(l) {
				p, v = value.Index(p, i), l[i]
				continue
			}
		}
		p, v = value.Key(p, tok), value.AsMap(v)[tok]
	}
	return p
}
