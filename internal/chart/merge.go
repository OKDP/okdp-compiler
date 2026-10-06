// Merge: the final values of each upstream chart, the values file Helm
// renders it with: upstream defaults < OKDP's defaults < computed values <
// instance upstream values, global copied down. The layers are merged as
// Helm merges its -f files.
//
// The instance's upstream values are upstream.<chart> of its values: the
// mapping sees them as written (values.upstream.<chart>), and they are
// applied with their checks after it (FinalValues).
package chart

import (
	"fmt"
	"io/fs"
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/okdp/okdp-compiler/internal/value"
)

// FinalValues: the final values of each chart key of the mapping's output
// (keyValues), which must be a key of specs and hold a map, and the
// upstream chart that renders them (its source). In key order: the first
// error is the same from one compile to the next, also when a refused
// upstream value reaches two keys (upstream.from). The schema violations of
// every key are reported together.
//
// cache: the chart cache, the upstream charts' defaults and schemas: the
// sources of the keys of output, together (newChartCache). pkg:
// the package (its defaults files). values: the instance's merged values (their
// global, and their upstream values).
func FinalValues(cache, pkg fs.FS, specs Specs, output M, values M) (map[string]map[string]any, map[string]Ref, error) {
	out, refs := map[string]map[string]any{}, map[string]Ref{}
	violations := map[string]string{}
	keys := slices.Sorted(maps.Keys(output))
	var sources []Ref
	for _, key := range keys {
		if spec, ok := specs[key]; ok {
			sources = append(sources, spec.Source)
		}
	}
	charts := newChartCache(cache, sources)
	for _, key := range keys {
		if _, ok := specs[key]; !ok {
			return nil, nil, fmt.Errorf("mapping output %q: not a chart of service.yaml", key)
		}
		computed, ok := output[key].(M)
		if !ok {
			return nil, nil, fmt.Errorf("mapping output %q: must be a map of the chart's values, got %s", key, value.Short(output[key]))
		}
		f, bad, err := keyValues(charts, pkg, specs, key, computed, values)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", key, err)
		}
		src := specs[key].Source
		out[key], refs[key] = f, src
		violations[key+" ("+src.id()+")"] = bad
	}
	if err := schemaError("compiled values", violations); err != nil {
		return nil, nil, err
	}
	return out, refs, nil
}

// keyValues: what the upstream chart is rendered with (`helm template
// <source> -f <final values>`). OKDP's defaults, the computed values and the
// instance's upstream values are merged as Helm merges -f files (MergeMaps:
// maps merge, lists and scalars replace, a null replaces and stays), over
// the chart's own defaults without their nulls. Rendered by Helm, these
// values give exactly what `helm template <source> -f defaults -f computed -f
// <upstream>` gives, with Helm 3 or 4: a null
// that deletes a default stays null (Helm deletes the key when it renders),
// a null where there is no default stays (Helm keeps it), and the null
// defaults are left out (Helm 4 drops them, Helm 3 copies them back).
//
// The violations of the chart's schema (its values.schema.json) by these
// values, as Helm sees them when it renders, are returned apart.
//
// charts: the chart cache, the chart's defaults and schema. files: the
// package. values: the instance's merged values.
func keyValues(charts *chartCache, files fs.FS, specs Specs, key string, computed M, values M) (M, string, error) {
	spec := specs[key]
	upstream, err := charts.get(spec.Source)
	if err != nil {
		return nil, "", err
	}
	defaults := upstream.defaults
	// Upstream chart defaults first, then OKDP's defaults for this chart,
	// then what the mapping computed.
	okdpDefaults := M{}
	if spec.Defaults != "" {
		if okdpDefaults, err = readValues(files, path.Clean(spec.Defaults)); err != nil {
			return nil, "", err
		}
	}
	out := layered(defaults, okdpDefaults, computed)
	// Last, the instance's upstream values (upstream.<chart>), when the chart
	// accepts them, checked ("{{", protect, append). The mapping has seen
	// them; these checks are what the platform enforces whatever the mapping
	// does with them. A chart with upstream.from receives the values of the
	// chart it names, with that chart's protect and append: one set of
	// values for two renders of an upstream chart, refused or appended the
	// same way in both.
	if owner, ok := specs.upstreamOf(key); ok {
		rules := specs[owner]
		user := value.AsMap(value.AsMap(values["upstream"])[owner])
		if err := applyUpstream(out, user, source{chart: owner}, rules.Protect, rules.Append); err != nil {
			return nil, "", err
		}
	}
	// Every chart gets the instance's global, as a subchart would.
	out["global"] = layered(value.AsMap(defaults["global"]), value.AsMap(values["global"]), value.AsMap(computed["global"]))
	v, err := checkSchema(upstream.entry+"/values.schema.json", upstream.schema, coalesceValues(defaults, out))
	return out, v, err
}

// layered: values layers merged as Helm merges -f files, over chart
// defaults without their nulls: what Helm renders the chart with is the same
// as with the layers given as -f files. No argument is changed.
func layered(defaults M, layers ...M) M {
	out := cleanNil(value.AsMap(value.Copy(defaults)))
	for _, l := range layers {
		out = value.Merge(out, l)
	}
	return out
}

// source: where instance upstream values come from, for the errors: the
// upstream.<chart> values of the instance.
type source struct{ chart string }

// at: the path p of the values of s (formatPath: a key in brackets joins
// without a dot).
func (s source) at(p string) string {
	switch {
	case p == "":
		return "upstream." + s.chart
	case p[0] == '[':
		return "upstream." + s.chart + p
	}
	return "upstream." + s.chart + "." + p
}

// applyUpstream: one source of instance upstream values over the computed
// ones, after the {{ and protect checks.
func applyUpstream(values, user M, src source, protect, appendPaths []string) error {
	if len(user) == 0 {
		return nil
	}
	// No templates: the values reach the chart as data.
	if err := NoTemplate(user, src.at); err != nil {
		return err
	}
	// Refuse any protected path, or a parent of it given as a non-map. A
	// path may name a key containing dots, or a family of keys, in brackets
	// (paths.go): the message names the key the value was found at, and the
	// path that protects it.
	for _, p := range append([]string{"global"}, protect...) {
		segs, err := parsePath(p)
		if err != nil {
			return fmt.Errorf("protect %q: %w", p, err)
		}
		if at := protectedAt(user, segs); at != nil {
			return fmt.Errorf("%s: %s is set by the platform and cannot be changed", src.at(formatPath(at)), p)
		}
	}
	return mergeUpstream(values, user, src, appendPaths)
}

// NoTemplate: no key or string of v, at any depth, contains "{{". Values
// reach a chart as data, and some charts pass them to tpl, where template
// code could call lookup and read the cluster's Secrets. The first one
// found, in path order (keys sorted, items in order), fails, at(p) naming
// where: p, its path in v (formatPath, [i] for an item). A value of a type
// values do not hold (package value) fails too: it could not be checked.
func NoTemplate(v any, at func(p string) string) error {
	return noTemplate(v, "", at)
}

// noTemplate: NoTemplate on v, found at the path p.
func noTemplate(v any, p string, at func(string) string) error {
	switch t := v.(type) {
	case nil, bool, float64:
	case string:
		if strings.Contains(t, "{{") {
			return fmt.Errorf("%s: the value contains \"{{\"", at(p))
		}
	case M:
		for _, k := range slices.Sorted(maps.Keys(t)) {
			kp := keyPath(p, k)
			if strings.Contains(k, "{{") {
				return fmt.Errorf("%s: the key contains \"{{\"", at(kp))
			}
			if err := noTemplate(t[k], kp, at); err != nil {
				return err
			}
		}
	case []any:
		for i, x := range t {
			if err := noTemplate(x, fmt.Sprintf("%s[%d]", p, i), at); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("%s: cannot be checked for \"{{\": %T is not a values type", at(p), v)
	}
	return nil
}

// mergeUpstream: user over values, the lists at appendPaths appended to the
// computed ones instead of replacing them. values is changed only when it
// succeeds; user never is.
func mergeUpstream(values, user M, src source, appendPaths []string) error {
	user = value.AsMap(value.Copy(user))
	// Listed paths are appended to the computed lists instead of replacing
	// them; a path may name keys in brackets, globs included (paths.go).
	done := map[string]bool{}
	for _, p := range appendPaths {
		segs, err := parsePath(p)
		if err != nil {
			return fmt.Errorf("append %q: %w", p, err)
		}
		if err := appendLists(user, values, segs, nil, src, done); err != nil {
			return err
		}
	}
	// The rest replaces the computed values.
	value.Merge(values, user)
	return nil
}
