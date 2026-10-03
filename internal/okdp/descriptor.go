package okdp

import (
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"

	sigsyaml "sigs.k8s.io/yaml"

	"github.com/okdp/okdp-compiler/internal/value"
)

// descriptor: the descriptor ConfigMap <release>-okdp of an instance, as
// okdp.descriptor renders it today: service, version, url, usage, and the
// connections the instance provides (outputs.yaml), which the console lists
// and other instances reference by name. Built from the mapping's `instance`
// and `provides` sections; every output is checked, its shape (checkOutput),
// then against its contract (okdp.contract.provide), in the order of
// provides, its fields in key order: the first error is reported. Then, as
// okdp.descriptor renders them, the credentials Secret of every output given
// a `secret` ({generate, stringData} or {stringData}), named after its
// secretRef (default <name>-<contract>-credentials).
func descriptor(in Inputs, ctx M, instance M, provides []any) (M, []any, error) {
	svc := ctxService(ctx)
	name, version := svc.Name, svc.Version
	suffix := value.Str(value.Dig(value.AsMap(ctx["global"]), "okdp.ingress.suffix"))
	// No connections: only to compute what internal references resolve to.
	r := resolver{in: in, conns: M{}, suffix: suffix}
	published := []any{}
	var credentials []any
	contracts := map[string]bool{}
	seen := map[string]bool{}
	for i, x := range provides {
		p, err := checkOutput(i, x)
		if err != nil {
			return nil, nil, err
		}
		contract := value.Str(p["contract"])
		// An output is named after the release unless the mapping names it.
		out := value.Str(p["name"])
		if out == "" {
			out = in.Release
		}
		where := fmt.Sprintf("provides %q (%s)", out, contract)
		if seen[contract+"/"+out] {
			return nil, nil, fmt.Errorf("two %s outputs are named %q", contract, out)
		}
		seen[contract+"/"+out] = true
		schema, err := readJSON(in.Contracts, contract+".schema.json")
		if err != nil {
			return nil, nil, fmt.Errorf("%s: unknown contract", where)
		}
		// Only contract fields, and no secret: those go to the credentials
		// Secret. In key order, as every check of an output: the first error
		// is the same from one compile to the next.
		props := value.AsMap(schema["properties"])
		values := value.AsMap(value.Copy(p["values"]))
		for _, k := range slices.Sorted(maps.Keys(values)) {
			ps, ok := props[k]
			if !ok {
				return nil, nil, fmt.Errorf("%s: field %q is not part of the %s contract", where, k, contract)
			}
			if value.Truthy(value.AsMap(ps)["x-okdp-secret"]) {
				return nil, nil, fmt.Errorf("%s: %q is a secret field, put it in the credentials Secret", where, k)
			}
		}
		if err := complete(schema, values, where); err != nil {
			return nil, nil, err
		}
		// Published under the release name, the values must be what an internal
		// reference to this instance resolves to (x-okdp-internal).
		if conv, ok := schema["x-okdp-internal"].(M); ok && out == in.Release {
			expected, err := r.resolve(out, contract, where)
			if err != nil {
				return nil, nil, err
			}
			for _, k := range slices.Sorted(maps.Keys(conv)) {
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
		published = append(published, entry)
		contracts[contract] = true
		// The credentials Secret, when the mapping describes it.
		if value.Truthy(secret) {
			objs, err := credentialsSecret(ctx, contract, out, value.Str(value.Dig(entry, "secretRef.name")), secret)
			if err != nil {
				return nil, nil, err
			}
			credentials = append(credentials, objs...)
		}
	}
	outputs := "[]\n"
	if len(published) > 0 {
		b, err := sigsyaml.Marshal(published) // Helm's toYaml
		if err != nil {
			return nil, nil, err
		}
		outputs = string(b)
	}
	// The labels of OKDP's objects, plus one per provided contract.
	labels := labels(svc, in.Release, "")
	names := make([]string, 0, len(contracts))
	for c := range contracts {
		names = append(names, c)
	}
	sort.Strings(names)
	for _, c := range names {
		labels["okdp.io/provides-"+c] = "true"
	}
	return M{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": M{
			"name":      trunc(in.Release+"-okdp", 63),
			"namespace": in.Namespace,
			"labels":    labels,
		},
		"data": M{
			"service":      name,
			"version":      version,
			"url":          strings.TrimSpace(value.Str(instance["url"])),
			"usage":        strings.TrimSpace(value.Str(instance["usage"])) + "\n",
			"outputs.yaml": outputs,
		},
	}, credentials, nil
}

// outputMaps: the keys of a provides item that are maps when set, and what
// each holds.
var outputMaps = []struct{ key, want string }{
	{"values", "a map of contract fields"},
	{"secretRef", "a map (name)"},
	{"secret", "a map (generate, stringData)"},
}

// checkOutput: the i-th item of provides, a map whose values, secretRef and
// secret are maps when set (null is not set).
func checkOutput(i int, x any) (M, error) {
	where := fmt.Sprintf("provides[%d]", i)
	p, ok := x.(M)
	if !ok {
		return nil, fmt.Errorf("%s: must be a map (contract, name, values, secretRef, secret), got %s", where, value.Short(x))
	}
	for _, f := range outputMaps {
		if _, err := shaped[M](where+": "+f.key, f.want, p[f.key]); err != nil {
			return nil, err
		}
	}
	return p, nil
}

// trunc: s cut to n bytes, as Helm's trunc.
func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
