package compiler

import (
	"fmt"
	"sort"
	"strings"

	sigsyaml "sigs.k8s.io/yaml"
)

// descriptor: the descriptor ConfigMap <release>-okdp of an instance, as
// okdp.descriptor renders it today: service, version, url, usage, and the
// connections the instance provides (outputs.yaml), which the console lists
// and other instances reference by name. Built from the mapping's `instance`
// and `provides` sections; every output is checked against its contract
// (okdp.contract.provide).
func descriptor(in Inputs, ctx M, instance M, provides []any) (M, error) {
	c, err := readYAML(in.Chart, "Chart.yaml")
	if err != nil {
		return nil, err
	}
	chart := asMap(c)
	name, version := str(chart["name"]), str(chart["version"])
	suffix := str(dig(asMap(ctx["global"]), "okdp.ingress.suffix"))
	// No connections: only to compute what internal references resolve to.
	r := resolver{in: in, conns: M{}, suffix: suffix}
	published := []any{}
	contracts := map[string]bool{}
	seen := map[string]bool{}
	for _, x := range provides {
		p := asMap(x)
		contract := str(p["contract"])
		// An output is named after the release unless the mapping names it.
		out := str(p["name"])
		if out == "" {
			out = in.Release
		}
		where := fmt.Sprintf("provides %q (%s)", out, contract)
		if seen[contract+"/"+out] {
			return nil, fmt.Errorf("two %s outputs are named %q", contract, out)
		}
		seen[contract+"/"+out] = true
		schema, err := readJSON(in.Contracts, contract+".schema.json")
		if err != nil {
			return nil, fmt.Errorf("%s: unknown contract", where)
		}
		// Only contract fields, and no secret: those go to the credentials Secret.
		props := asMap(schema["properties"])
		values := asMap(deepCopy(p["values"]))
		for k := range values {
			ps, ok := props[k]
			if !ok {
				return nil, fmt.Errorf("%s: field %q is not part of the %s contract", where, k, contract)
			}
			if truthy(asMap(ps)["x-okdp-secret"]) {
				return nil, fmt.Errorf("%s: %q is a secret field, put it in the credentials Secret", where, k)
			}
		}
		if err := complete(schema, values, where); err != nil {
			return nil, err
		}
		// Published under the release name, the values must be what an internal
		// reference to this instance resolves to (x-okdp-internal).
		if conv, ok := schema["x-okdp-internal"].(M); ok && out == in.Release {
			expected, err := r.resolve(out, contract, where)
			if err != nil {
				return nil, err
			}
			for k := range conv {
				if str(values[k]) != str(expected[k]) {
					return nil, fmt.Errorf("%s: %s is %q but internal references to %q resolve it to %q",
						where, k, str(values[k]), out, str(expected[k]))
				}
			}
		}
		entry := M{"name": out, "contract": contract, "values": values}
		if ref := asMap(p["secretRef"]); len(ref) > 0 {
			entry["secretRef"] = M{"name": ref["name"]}
		}
		published = append(published, entry)
		contracts[contract] = true
	}
	outputs := "[]\n"
	if len(published) > 0 {
		b, err := sigsyaml.Marshal(published) // Helm's toYaml
		if err != nil {
			return nil, err
		}
		outputs = string(b)
	}
	// Helm and OKDP labels, plus one per provided contract.
	helmChart := strings.TrimSuffix(trunc(strings.ReplaceAll(name+"-"+version, "+", "_"), 63), "-")
	labels := M{
		"helm.sh/chart":                helmChart,
		"app.kubernetes.io/name":       name,
		"app.kubernetes.io/instance":   in.Release,
		"app.kubernetes.io/managed-by": "Helm",
		"app.kubernetes.io/part-of":    "okdp",
		"okdp.io/instance":             in.Release,
		"okdp.io/service":              name,
	}
	if v := str(chart["appVersion"]); v != "" {
		labels["app.kubernetes.io/version"] = v
	}
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
			"url":          strings.TrimSpace(str(instance["url"])),
			"usage":        strings.TrimSpace(str(instance["usage"])) + "\n",
			"outputs.yaml": outputs,
		},
	}, nil
}

// trunc: s cut to n bytes, as Helm's trunc.
func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
