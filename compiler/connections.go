// Connections: the references declared in the parameter schema
// (x-okdp-connection-ref), resolved to an external connection or to an
// instance of the namespace, and completed against their contract.
package compiler

import (
	"fmt"
	"sort"
	"strings"
)

// resolver: resolves connection references against the contracts and the
// external connections of the instance.
type resolver struct {
	in     Inputs
	conns  M
	suffix string
}

// walk follows the parameter schema and replaces every string carrying an
// x-okdp-connection-ref with the resolved connection (okdp.connection).
func (r resolver) walk(schema M, v any, path string) error {
	switch t := v.(type) {
	case M:
		// Only the keys the schema declares: the others are left as they are.
		props := asMap(schema["properties"])
		for k, ps := range props {
			x, present := t[k]
			if !present {
				continue
			}
			p := k
			if path != "" {
				p = path + "." + k
			}
			psm := asMap(ps)
			// A reference: replaced by the connection. Empty means not set.
			if ref, ok := psm["x-okdp-connection-ref"].(M); ok {
				if s, ok := x.(string); ok && s != "" {
					c, err := r.resolve(s, str(ref["contract"]), p)
					if err != nil {
						return err
					}
					t[k] = c
				}
				continue
			}
			if err := r.walk(psm, x, p); err != nil {
				return err
			}
		}
	case []any:
		// A list: the same, with the item schema.
		items := asMap(schema["items"])
		for i, x := range t {
			p := fmt.Sprintf("%s[%d]", path, i)
			if ref, ok := items["x-okdp-connection-ref"].(M); ok {
				if s, ok := x.(string); ok && s != "" {
					c, err := r.resolve(s, str(ref["contract"]), p)
					if err != nil {
						return err
					}
					t[i] = c
				}
				continue
			}
			if err := r.walk(items, x, p); err != nil {
				return err
			}
		}
	}
	return nil
}

// resolve: a reference to its contract fields. An entry of connections is
// external; any other name is an instance of the namespace (internal).
func (r resolver) resolve(ref, contract, field string) (M, error) {
	schema, err := readJSON(r.in.Contracts, contract+".schema.json")
	if err != nil {
		return nil, fmt.Errorf("%s: unknown contract %q", field, contract)
	}
	props := asMap(schema["properties"])
	out, source := M{}, "internal"
	var secretRef any
	if c, ok := r.conns[ref].(M); ok {
		source = "external"
		if str(c["contract"]) != contract {
			return nil, fmt.Errorf("%s: connection %q has contract %q, %q is expected", field, ref, str(c["contract"]), contract)
		}
		for k, x := range c {
			switch k {
			case "contract", "description":
			// Credentials are only referenced, never inlined.
			case "secretRef":
				secretRef = M{"name": asMap(x)["name"]}
			default:
				p, known := props[k]
				if !known {
					return nil, fmt.Errorf("%s: connection %q: field %q is not part of the %s contract", field, ref, k, contract)
				}
				if truthy(asMap(p)["x-okdp-secret"]) {
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
			out[k] = strings.NewReplacer("${ref}", ref, "${namespace}", r.in.Namespace, "${suffix}", r.suffix).Replace(str(tpl))
		}
		// Its credentials Secret, when the contract has secret fields.
		for _, p := range props {
			if truthy(asMap(p)["x-okdp-secret"]) {
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
	return out, nil
}

// complete: a contract's defaults, then its derived fields, then the
// required check (okdp.contract.complete).
func complete(schema, out M, where string) error {
	props := asMap(schema["properties"])
	contract := str(schema["x-okdp-contract"])
	names := make([]string, 0, len(props))
	for k := range props {
		names = append(names, k)
	}
	sort.Strings(names)
	// Defaults first: a derived field may come from a defaulted one.
	for _, pass := range []string{"default", "derived"} {
		for _, k := range names {
			p := asMap(props[k])
			// Set fields are kept; secret fields stay in the Secret.
			if _, set := out[k]; set || truthy(p["x-okdp-secret"]) {
				continue
			}
			derived, isDerived := p["x-okdp-derived"].(M)
			if pass == "derived" && isDerived {
				if src, ok := out[str(derived["from"])]; ok {
					if mv, ok := asMap(derived["map"])[str(src)]; ok {
						out[k] = mv
					}
				}
			} else if pass == "default" && !isDerived {
				if d, ok := p["default"]; ok {
					// A conditional default applies only when its condition holds.
					cond := asMap(p["x-ui-condition"])
					if len(cond) == 0 || str(out[str(cond["field"])]) == str(cond["value"]) {
						out[k] = d
					}
				}
			}
		}
	}
	// false is a value: only missing or empty fails.
	required, _ := schema["required"].([]any)
	for _, k := range required {
		if !truthy(out[str(k)]) && out[str(k)] != false {
			return fmt.Errorf("%s: field %q is required by the %s contract", where, k, contract)
		}
	}
	return nil
}
