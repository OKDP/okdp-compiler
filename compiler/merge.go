// Merge: the final values of each upstream chart, as okdp.vendor.render
// computes them (upstream defaults < fixed values < computed values <
// instance upstream values, global copied down).
package compiler

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"
)

// finalValues: what the upstream chart receives, as okdp.vendor.render
// computes it (and writes to the values ConfigMap).
func finalValues(in Inputs, svc *Service, chart string, computed M, values M) (M, error) {
	d, err := readYAML(in.Chart, path.Join("vendor", chart, "values.yaml"))
	if err != nil {
		return nil, err
	}
	// Upstream chart defaults first.
	defaults := asMap(d)
	out := asMap(deepCopy(defaults))
	spec := svc.Charts[chart]
	// Then OKDP's fixed values for this chart.
	if spec.Fixed != "" {
		f, err := readYAML(in.Chart, path.Clean(spec.Fixed))
		if err != nil {
			return nil, err
		}
		out = mergeOverwrite(out, asMap(f))
	}
	// Then what the mapping computed.
	out = mergeOverwrite(out, asMap(deepCopy(computed)))
	// Last, the instance's upstream values, when the chart accepts them.
	if spec.Upstream != nil && *spec.Upstream {
		if err := applyUpstream(out, asMap(asMap(values["upstream"])[chart]), chart, spec.Protect, spec.Append); err != nil {
			return nil, err
		}
	}
	// Every chart gets the instance's global, as a subchart would.
	g := mergeOverwrite(asMap(deepCopy(defaults["global"])), asMap(deepCopy(values["global"])))
	g = mergeOverwrite(g, asMap(computed["global"]))
	out["global"] = g
	return out, nil
}

// applyUpstream: okdp.vendor.upstream (instance values over the computed ones).
func applyUpstream(values, user M, chart string, protect, appendPaths []string) error {
	if len(user) == 0 {
		return nil
	}
	// No templates: the values reach the chart as data.
	if b, _ := json.Marshal(user); strings.Contains(string(b), "{{") {
		return fmt.Errorf("upstream.%s: a key or value contains \"{{\"", chart)
	}
	user = asMap(deepCopy(user))
	// Refuse any protected path, or a parent of it given as a non-map.
	for _, p := range append([]string{"global"}, protect...) {
		parts := strings.Split(p, ".")
		var node any = user
		for i, part := range parts {
			m, ok := node.(M)
			if !ok {
				break
			}
			x, ok := m[part]
			if !ok {
				break
			}
			if _, isMap := x.(M); i == len(parts)-1 || !isMap {
				return fmt.Errorf("upstream.%s.%s: %s is set by the platform and cannot be changed", chart, strings.Join(parts[:i+1], "."), p)
			}
			node = x
		}
	}
	// Listed paths are appended to the computed lists instead of replacing them.
	for _, p := range appendPaths {
		parts := strings.Split(p, ".")
		u, v := user, any(values)
		found := true
		for _, part := range parts[:len(parts)-1] {
			um, ok := u[part].(M)
			if !ok {
				found = false
				break
			}
			u = um
			if vm, ok := v.(M); ok {
				v = vm[part]
			}
		}
		last := parts[len(parts)-1]
		if add, ok := u[last]; found && ok {
			addList, ok := add.([]any)
			if !ok {
				return fmt.Errorf("upstream.%s.%s must be a list", chart, p)
			}
			var base []any
			if vm, ok := v.(M); ok {
				if cl, ok := vm[last].([]any); ok {
					base = cl
				}
			}
			u[last] = append(append([]any{}, base...), addList...)
		}
	}
	// The rest replaces the computed values.
	mergeOverwrite(values, user)
	return nil
}
