// Merge: the final values of each upstream chart, as okdp.vendor.render
// computes them (upstream defaults < base values < computed values <
// instance upstream values files < inline instance upstream values, global
// copied down).
package compiler

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
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
	// Then OKDP's default values for this chart.
	if spec.Base != "" {
		f, err := readYAML(in.Chart, path.Clean(spec.Base))
		if err != nil {
			return nil, err
		}
		out = mergeOverwrite(out, asMap(f))
	}
	// Then what the mapping computed.
	out = mergeOverwrite(out, asMap(deepCopy(computed)))
	// Last, the instance's upstream values, when the chart accepts them:
	// its files, then its inline values.
	if spec.Upstream.Enabled {
		files, optional := spec.Upstream.Files, spec.Upstream.Files == nil
		if optional {
			files = []string{"upstream." + chart + ".yaml"}
		}
		for _, f := range files {
			u, err := readUpstream(in.Instance, path.Clean(f), optional)
			if err != nil {
				return nil, err
			}
			if err := applyUpstream(out, asMap(u), source{file: f}, spec.Protect, spec.Append); err != nil {
				return nil, err
			}
		}
		if err := applyUpstream(out, asMap(asMap(values["upstream"])[chart]), source{chart: chart}, spec.Protect, spec.Append); err != nil {
			return nil, err
		}
	}
	// Every chart gets the instance's global, as a subchart would.
	g := mergeOverwrite(asMap(deepCopy(defaults["global"])), asMap(deepCopy(values["global"])))
	g = mergeOverwrite(g, asMap(computed["global"]))
	out["global"] = g
	return out, nil
}

// readUpstream: an upstream values file of the instance. An optional file
// may be missing, as may the instance files altogether.
func readUpstream(instance fs.FS, name string, optional bool) (any, error) {
	if instance == nil {
		if optional {
			return nil, nil
		}
		return nil, fmt.Errorf("upstream file %s: no instance files (Inputs.Instance)", name)
	}
	u, err := readYAML(instance, name)
	if optional && errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return u, err
}

// source: where instance upstream values come from, for the errors: an
// instance file, or the inline upstream.<chart> values.
type source struct{ file, chart string }

// at: the path p of the values of s.
func (s source) at(p string) string {
	switch {
	case s.file != "" && p == "":
		return s.file
	case s.file != "":
		return s.file + ": " + p
	case p == "":
		return "upstream." + s.chart
	}
	return "upstream." + s.chart + "." + p
}

// applyUpstream: okdp.vendor.upstream (instance values over the computed ones).
func applyUpstream(values, user M, src source, protect, appendPaths []string) error {
	if len(user) == 0 {
		return nil
	}
	// No templates: the values reach the chart as data.
	if b, _ := json.Marshal(user); strings.Contains(string(b), "{{") {
		return fmt.Errorf("%s: a key or value contains \"{{\"", src.at(""))
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
				return fmt.Errorf("%s: %s is set by the platform and cannot be changed", src.at(strings.Join(parts[:i+1], ".")), p)
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
				return fmt.Errorf("%s must be a list", src.at(p))
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
