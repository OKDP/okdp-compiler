// Connections: the references declared in the parameter schema
// (x-okdp-connection-ref), resolved to an external connection or to an
// instance of the namespace, and completed against their contract.
package okdp

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"

	"github.com/okdp/okdp-compiler/internal/chart"
	"github.com/okdp/okdp-compiler/internal/value"
)

// resolver: resolves connection references against the contracts and the
// external connections of the instance.
type resolver struct {
	in     Inputs
	conns  M
	suffix string
	deps   *[]Dependency // when set, every internal reference is recorded
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
func (r resolver) resolveAll(schema, params M) error {
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
// by parameter path.
func (r resolver) walk(schema M, v any, path string, failed map[string]error) {
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
			p := k
			if path != "" {
				p = path + "." + k
			}
			visit(value.AsMap(props[k]), x, p, func(c M) { t[k] = c })
		}
	case []any:
		// A list: the same, with the item schema.
		items := value.AsMap(schema["items"])
		for i, x := range t {
			visit(items, x, fmt.Sprintf("%s[%d]", path, i), func(c M) { t[i] = c })
		}
	}
}

// resolve: a reference to its contract fields. An entry of connections is
// external; any other name is an instance of the namespace (internal), a
// release name. The first error is reported: the contract, then the name of
// an internal reference, the fields of an external connection in
// alphabetical order, then the required fields, then the violations of the
// contract by an external connection, all of them (checkContract), then
// "{{" in the connection (chart.NoTemplate).
func (r resolver) resolve(ref, contract, field string) (M, error) {
	schema, sch, err := readContract(r.in.Contracts, contract)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%s: unknown contract %q", field, contract)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", field, err)
	}
	props := value.AsMap(schema["properties"])
	out, source := M{}, "internal"
	var secretRef any
	if c, ok := r.conns[ref].(M); ok {
		source = "external"
		if value.Str(c["contract"]) != contract {
			return nil, fmt.Errorf("%s: connection %q has contract %q, %q is expected", field, ref, value.Str(c["contract"]), contract)
		}
		for _, k := range slices.Sorted(maps.Keys(c)) {
			x := c[k]
			switch k {
			case "contract", "description":
			// Credentials are only referenced, never inlined.
			case "secretRef":
				secretRef = M{"name": value.AsMap(x)["name"]}
			default:
				p, known := props[k]
				if !known {
					return nil, fmt.Errorf("%s: connection %q: field %q is not part of the %s contract", field, ref, k, contract)
				}
				if value.Truthy(value.AsMap(p)["x-okdp-secret"]) {
					return nil, fmt.Errorf("%s: connection %q: %q is a secret field", field, ref, k)
				}
				out[k] = x
			}
		}
	} else {
		// An instance: its fields follow the contract's naming convention.
		conv, ok := schema["x-okdp-internal"].(M)
		if !ok {
			return nil, fmt.Errorf("%s: %q is not a connection, and the %s contract has no internal naming convention", field, ref, contract)
		}
		// Its name goes into hosts, Secret names and the dependency.
		if !isRelease(ref) {
			return nil, fmt.Errorf("%s: %q is not a connection, nor a release name: lowercase letters, digits and \"-\", a letter or digit at each end, %d characters at most", field, ref, releaseMax)
		}
		for k, tpl := range conv {
			out[k] = strings.NewReplacer("${ref}", ref, "${namespace}", r.in.Namespace, "${suffix}", r.suffix).Replace(value.Str(tpl))
		}
		// Its credentials Secret, when the contract has secret fields.
		for _, p := range props {
			if value.Truthy(value.AsMap(p)["x-okdp-secret"]) {
				secretRef = M{"name": fmt.Sprintf("%s-%s-credentials", ref, contract)}
				break
			}
		}
	}
	where := fmt.Sprintf("%s (connection %q)", field, ref)
	if err := complete(schema, out, where); err != nil {
		return nil, err
	}
	// An instance's fields follow from the contract; a connection's are
	// written by hand.
	if source == "external" {
		if err := checkContract(sch, out, contract, where); err != nil {
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
	if source == "internal" && r.deps != nil {
		*r.deps = append(*r.deps, Dependency{Instance: ref, Contract: contract, Field: field})
	}
	return out, nil
}

// complete: a contract's defaults, then its derived fields, again until
// nothing changes, then the required check (okdp.contract.complete).
func complete(schema, out M, where string) error {
	props := value.AsMap(schema["properties"])
	contract := value.Str(schema["x-okdp-contract"])
	names := make([]string, 0, len(props))
	for k := range props {
		names = append(names, k)
	}
	sort.Strings(names)
	// Defaults first: a derived field may come from a defaulted one. Until
	// nothing changes: a conditional default may depend on a field set
	// after it, one that sorts after it or a derived one. A field is set
	// once: the loop ends.
	for changed := true; changed; {
		changed = false
		for _, pass := range []string{"default", "derived"} {
			for _, k := range names {
				p := value.AsMap(props[k])
				// Set fields are kept; secret fields stay in the Secret.
				if _, set := out[k]; set || value.Truthy(p["x-okdp-secret"]) {
					continue
				}
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
	// set: it is in the credentials Secret.
	required, _ := schema["required"].([]any)
	for _, k := range required {
		if value.Truthy(value.AsMap(props[value.Str(k)])["x-okdp-secret"]) {
			continue
		}
		if !value.Truthy(out[value.Str(k)]) && out[value.Str(k)] != false {
			return fmt.Errorf("%s: field %q is required by the %s contract", where, k, contract)
		}
	}
	return nil
}

// readJSON: a JSON file of fsys (the parameter schema), as a map.
func readJSON(fsys fs.FS, name string) (M, error) {
	b, err := fs.ReadFile(fsys, name)
	if err != nil {
		return nil, err
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return value.AsMap(v), nil
}

// readContract: the contract name, the file <name>.schema.json of fsys: its
// schema, for the keywords complete and resolve read, and the validator of
// the values of its connections and outputs (checkContract): the schema
// compiled as a chart's (chart.CompileSchema, x-* keywords ignored), its
// secret fields left out of required, as they are in the credentials
// Secret, never in the values. An error wrapping fs.ErrNotExist when there
// is no such file; otherwise one naming the file and the cause when it
// cannot be read, is not a JSON object or not a valid JSON Schema.
func readContract(fsys fs.FS, name string) (M, *jsonschema.Schema, error) {
	file := name + ".schema.json"
	b, err := fs.ReadFile(fsys, file)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, err
	}
	fail := func(cause any) error {
		return fmt.Errorf("the %s contract cannot be read: %s: %v", name, file, cause)
	}
	if err != nil {
		// The file is named once.
		if pe := (*fs.PathError)(nil); errors.As(err, &pe) {
			err = pe.Err
		}
		return nil, nil, fail(err)
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, nil, fail(err)
	}
	schema, ok := v.(M)
	if !ok {
		return nil, nil, fail("not a JSON object")
	}
	// Read again for the validator, which keeps the numbers as written.
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		return nil, nil, fail(err)
	}
	d := value.AsMap(doc) // an object, as schema
	if required, ok := d["required"].([]any); ok {
		props := value.AsMap(schema["properties"])
		kept := []any{}
		for _, k := range required {
			if !value.Truthy(value.AsMap(props[value.Str(k)])["x-okdp-secret"]) {
				kept = append(kept, k)
			}
		}
		d["required"] = kept
		if len(kept) == 0 {
			delete(d, "required") // draft-04 refuses an empty list
		}
	}
	sch, err := chart.CompileSchema("file:///"+file, d)
	if err != nil {
		return nil, nil, fail(err)
	}
	return schema, sch, nil
}

// checkContract: values, the fields of a connection or an output once
// completed, against the validator of their contract (readContract): every
// violation, one line each, sorted by field then message:
//
//	<where>: field "port" violates the database contract: got string, want integer
//
// The field is a path in the values (tls.mode, catalogs[1]); a violation by
// the values as a whole (minProperties) reads "the fields violate the
// <contract> contract". Every keyword that fails is a violation, those
// under a $ref or an allOf included; a failed anyOf or oneOf is one.
func checkContract(sch *jsonschema.Schema, values M, contract, where string) error {
	err := sch.Validate(any(values))
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
				for _, c := range e.Causes {
					visit(c)
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
			lines[i] = fmt.Sprintf("%s: the fields violate the %s contract: %s", where, contract, v.msg)
		} else {
			lines[i] = fmt.Sprintf("%s: field %q violates the %s contract: %s", where, v.field, contract, v.msg)
		}
	}
	return errors.New(strings.Join(lines, "\n"))
}

// fieldPath: the location loc in v, as a path: tls.mode, catalogs[1].
func fieldPath(v any, loc []string) string {
	var sb strings.Builder
	for _, tok := range loc {
		if l, ok := v.([]any); ok {
			fmt.Fprintf(&sb, "[%s]", tok)
			v = nil
			if i, err := strconv.Atoi(tok); err == nil && i >= 0 && i < len(l) {
				v = l[i]
			}
			continue
		}
		if sb.Len() > 0 {
			sb.WriteByte('.')
		}
		sb.WriteString(tok)
		v = value.AsMap(v)[tok]
	}
	return sb.String()
}
