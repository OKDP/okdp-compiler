package okdp

import (
	_ "embed"
	"fmt"
	"io/fs"
	"maps"
	"slices"

	"github.com/okdp/okdp-compiler/internal/compile"
	"github.com/okdp/okdp-compiler/internal/value"
)

// descriptorTemplate: the descriptor ConfigMap (the mapping language).
//
//go:embed descriptor.cel.yaml
var descriptorTemplate []byte

// descriptor: the descriptor ConfigMap <release>-okdp of the instance inst
// (descriptorTemplate, evaluated in the compile's session), from the
// mapping's `instance` (info) and `provides` sections, their shapes checked
// (runMapping). Every output is named after the release unless the mapping
// names it, and checked against its contract, from contracts
// (Inputs.Contracts), in the order of provides, its fields in key order
// (contract.fields): the first error is reported, every required field not
// set at once (contract.complete), every violation of the contract's types
// at once (contract.check). An output given a `secret` ({generate,
// stringData} or {stringData}) gets its credentials Secret, named after its
// secretRef (default <name>-<contract>-credentials). Then two outputs of
// one contract and name fail (the template's errors).
func descriptor(session *compile.Session, inst *instance, contracts fs.FS, info M, provides []any) (M, []M, error) {
	// No connections: only to compute what internal references resolve to
	// (what it records is not read).
	r := resolver{contracts: contracts, conns: M{}, namespace: inst.namespace, suffix: inst.suffix}
	outputs := []any{}
	var credentials []M
	for _, x := range provides {
		p := value.AsMap(x)
		contract := value.Str(p["contract"])
		out := value.Str(p["name"])
		if out == "" {
			out = inst.release
		}
		where := fmt.Sprintf("provides %q (%s)", out, contract)
		c, err := readContract(contracts, contract)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", where, err)
		}
		// Only contract fields, and no secret: those go to the credentials
		// Secret. In key order, as every check of an output: the first error
		// is the same from one compile to the next.
		values := value.AsMap(value.Copy(p["values"]))
		if err := c.fields(values, where); err != nil {
			return nil, nil, err
		}
		if err := c.complete(values, where); err != nil {
			return nil, nil, err
		}
		if err := c.check(values, where); err != nil {
			return nil, nil, err
		}
		// Published under the release name, the values must be what an internal
		// reference to this instance resolves to (x-okdp-internal).
		if c.internal != nil && out == inst.release {
			expected, err := r.resolve(out, contract, where)
			if err != nil {
				return nil, nil, err
			}
			for _, k := range slices.Sorted(maps.Keys(c.internal)) {
				if value.Str(values[k]) != value.Str(expected[k]) {
					return nil, nil, fmt.Errorf("%s: %s is %q but internal references to %q resolve it to %q",
						where, k, value.Str(values[k]), out, value.Str(expected[k]))
				}
			}
		}
		entry := M{"name": out, "contract": contract, "values": values}
		secret := value.AsMap(p["secret"])
		if ref := value.AsMap(p["secretRef"]); len(ref) > 0 {
			entry["secretRef"] = M{"name": ref["name"]}
		} else if value.Truthy(secret) {
			entry["secretRef"] = M{"name": out + "-" + contract + "-credentials"}
		}
		outputs = append(outputs, entry)
		// The credentials Secret, when the mapping describes it.
		if value.Truthy(secret) {
			objs, err := credentialsSecret(session, inst, contract, out, value.Str(value.Dig(entry, "secretRef.name")), secret)
			if err != nil {
				return nil, nil, err
			}
			for _, o := range objs {
				credentials = append(credentials, value.AsMap(o))
			}
		}
	}
	scope := maps.Clone(inst.scope)
	scope["instance"], scope["outputs"] = info, outputs
	desc, err := evalTemplate(session, "descriptor.cel.yaml", descriptorTemplate, scope)
	return desc, credentials, err
}

// credentialsSecret: the credentials Secret of an output published with a
// `secret`: {generate: [keys], stringData} as a generated Secret
// (generatedSecret, in the compile's session), {stringData} as a plain
// Secret; both labelled okdp.io/credentials-<contract>, in the namespace of
// the instance inst.
func credentialsSecret(session *compile.Session, inst *instance, contract, output, name string, secret M) ([]any, error) {
	label := "okdp.io/credentials-" + contract
	if value.Truthy(secret["generate"]) {
		return generatedSecret(session, inst, M{
			"name":       name,
			"keys":       secret["generate"],
			"stringData": value.AsMap(secret["stringData"]),
			"labels":     M{label: "true"},
		})
	}
	if !value.Truthy(secret["stringData"]) {
		return nil, fmt.Errorf("%s: output %q: secret needs stringData or generate", inst.svc.Name, output)
	}
	l := labels(inst.svc.Name, inst.release, "", false)
	l[label] = "true"
	return []any{M{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata":   M{"name": name, "namespace": inst.namespace, "labels": l},
		"type":       "Opaque",
		"stringData": value.Copy(secret["stringData"]),
	}}, nil
}
