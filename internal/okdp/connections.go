// Connections: the references declared in the parameter schema
// (x-okdp-connection-ref), resolved to an external connection or to an
// instance of the namespace, and completed against their contract.
package okdp

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"sort"
	"strings"

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
// does not resolve is recorded in failed, by parameter path.
func (r resolver) walk(schema M, v any, path string, failed map[string]error) {
	// visit: x, at p, of the schema s; set replaces it with its connection.
	visit := func(s M, x any, p string, set func(M)) {
		ref, ok := s["x-okdp-connection-ref"].(M)
		if !ok {
			r.walk(s, x, p, failed)
			return
		}
		// A reference: replaced by the connection. Empty means not set.
		if name, ok := x.(string); ok && name != "" {
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
// external; any other name is an instance of the namespace (internal). The
// first error is reported: the contract, then the fields of an external
// connection in alphabetical order, then the required fields.
func (r resolver) resolve(ref, contract, field string) (M, error) {
	schema, err := readJSON(r.in.Contracts, contract+".schema.json")
	if err != nil {
		return nil, fmt.Errorf("%s: unknown contract %q", field, contract)
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
	if err := complete(schema, out, fmt.Sprintf("%s (connection %q)", field, ref)); err != nil {
		return nil, err
	}
	if secretRef != nil {
		out["secretRef"] = secretRef
	}
	// Where it comes from, for the mapping (e.g. error messages).
	out["okdp"] = M{"ref": ref, "contract": contract, "source": source}
	if source == "internal" && r.deps != nil {
		*r.deps = append(*r.deps, Dependency{Instance: ref, Contract: contract, Field: field})
	}
	return out, nil
}

// complete: a contract's defaults, then its derived fields, then the
// required check (okdp.contract.complete).
func complete(schema, out M, where string) error {
	props := value.AsMap(schema["properties"])
	contract := value.Str(schema["x-okdp-contract"])
	names := make([]string, 0, len(props))
	for k := range props {
		names = append(names, k)
	}
	sort.Strings(names)
	// Defaults first: a derived field may come from a defaulted one.
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
						out[k] = mv
					}
				}
			} else if pass == "default" && !isDerived {
				if d, ok := p["default"]; ok {
					// A conditional default applies only when its condition holds.
					cond := value.AsMap(p["x-ui-condition"])
					if len(cond) == 0 || value.Str(out[value.Str(cond["field"])]) == value.Str(cond["value"]) {
						out[k] = d
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

// readJSON: a JSON file of fsys (a contract, the parameter schema), as a
// map.
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
