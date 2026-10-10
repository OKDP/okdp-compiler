package compiler

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"sort"
	"strings"

	sigsyaml "sigs.k8s.io/yaml"

	"github.com/okdp/okdp-compiler/internal/value"
)

// descriptor: the descriptor ConfigMap <release>-okdp (cut as
// okdpFullname) of the instance inst: service, version, url, usage, and the connections the instance provides
// (outputs.yaml), which the console lists and other instances reference by
// name. Built from the mapping's `instance` (info) and `provides` sections,
// their shapes checked (runMapping); every output is checked against its
// contract, from contracts, the compile's, in the
// order of provides, its fields in key order (contract.fields): the first
// error is reported, every required field not set at once
// (contract.complete), every violation of the contract's types at once
// (contract.check). Then the credentials Secret of every output
// given a `secret` ({generate, stringData} or {stringData}), named after
// its secretRef (default <name>-<contract>-credentials).
func descriptor(inst *instance, contracts *contractCache, info M, provides []any) (M, []M, error) {
	// No connections: only to compute what internal references resolve to
	// (what it records is not read).
	r := resolver{contracts: contracts, conns: M{}, namespace: inst.namespace, suffix: inst.suffix}
	published := []any{}
	var credentials []M
	provided := map[string]bool{}
	seen := map[string]bool{}
	for _, x := range provides {
		p := value.AsMap(x)
		contract := value.Str(p["contract"])
		// An output is named after the release unless the mapping names it.
		out := value.Str(p["name"])
		if out == "" {
			out = inst.release
		}
		where := fmt.Sprintf("provides %q (%s)", out, contract)
		if seen[contract+"/"+out] {
			return nil, nil, fmt.Errorf("two %s outputs are named %q", contract, out)
		}
		seen[contract+"/"+out] = true
		c, err := contracts.get(contract)
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil, fmt.Errorf("%s: unknown contract", where)
		}
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
		published = append(published, entry)
		provided[contract] = true
		// The credentials Secret, when the mapping describes it.
		if value.Truthy(secret) {
			objs, err := credentialsSecret(inst, contract, out, value.Str(value.Dig(entry, "secretRef.name")), secret)
			if err != nil {
				return nil, nil, err
			}
			for _, o := range objs {
				credentials = append(credentials, value.AsMap(o))
			}
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
	labels := labels(inst.svc, inst.release, "")
	names := make([]string, 0, len(provided))
	for c := range provided {
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
			"name":      fullname(inst.release, "okdp"),
			"namespace": inst.namespace,
			"labels":    labels,
		},
		"data": M{
			"service":      inst.svc.Name,
			"version":      inst.svc.Version,
			"url":          strings.TrimSpace(value.Str(info["url"])),
			"usage":        strings.TrimSpace(value.Str(info["usage"])) + "\n",
			"outputs.yaml": outputs,
		},
	}, credentials, nil
}
