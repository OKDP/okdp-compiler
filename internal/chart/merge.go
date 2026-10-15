// Merge: the final values of each upstream chart, the values file Helm
// renders it with: OKDP's defaults < computed values < instance upstream
// values, global copied down. The layers are merged as Helm merges its -f
// files. The chart's own defaults are not among them: Helm coalesces the
// values over them when it renders the chart. A check reads them, with the
// chart's schema, from the chart cache (checkKey).
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
	"reflect"
	"slices"
	"strings"

	"github.com/okdp/okdp-compiler/internal/value"
)

// FinalValues: the final values of each chart key of the mapping's output
// (keyValues), which must be a key of specs (its values a map:
// mapping.schema.json says so), and the
// upstream chart that renders them (its source). In key order: the first
// error is the same from one compile to the next.
//
// cache: the chart cache, the upstream charts' defaults and schemas, each in
// its repository's directory (chartCache); nil, no chart is read. With one,
// the final values of each key are checked against its upstream chart
// (checkKey): the schema violations of every key are reported together,
// and the warnings are returned, in key order. The final values are the
// same with or without it. pkg: the package (its defaults files). file: the
// values file the output is read from, for the errors of its keys. values:
// the instance's merged values (their global, and their upstream values).
func FinalValues(cache, pkg fs.FS, specs Specs, file string, output M, values M) (map[string]map[string]any, map[string]Ref, []string, error) {
	out, refs := map[string]map[string]any{}, map[string]Ref{}
	violations := map[string]string{}
	var warnings []string
	var charts *chartCache
	if cache != nil {
		charts = &chartCache{fsys: cache, charts: map[Ref]*upstreamChart{}}
	}
	for _, key := range slices.Sorted(maps.Keys(output)) {
		spec, ok := specs[key]
		if !ok {
			return nil, nil, nil, fmt.Errorf("%s: %s: not a dependency of Chart.yaml", file, key)
		}
		f, appended, err := keyValues(pkg, key, spec, value.AsMap(output[key]), values)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("%s: %w", key, err)
		}
		out[key], refs[key] = f, spec.Source
		if charts == nil {
			continue
		}
		u, err := charts.get(spec.Source)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("%s: %w", key, err)
		}
		bad, w, err := checkKey(u, key, spec.Source, f, appended)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("%s: %w", key, err)
		}
		violations[key+" ("+spec.Source.id()+")"] = bad
		warnings = append(warnings, w...)
	}
	if err := schemaError("compiled values", violations); err != nil {
		return nil, nil, nil, err
	}
	return out, refs, warnings, nil
}

// keyValues: what the upstream chart is rendered with (`helm template
// <source> -f <final values>`): OKDP's defaults, the computed values and
// the instance's upstream values, merged as Helm merges -f files
// (MergeMaps: maps merge, lists and scalars replace, a null replaces and
// stays). Helm renders the chart with them as with these layers given as
// -f files: it coalesces them over the chart's defaults, where a null
// deletes a default and stays where there is none.
//
// The paths of the instance's lists appended to (append) are returned with
// them, for checkKey. files: the package. values: the instance's merged
// values.
func keyValues(files fs.FS, key string, spec Spec, computed M, values M) (M, [][]string, error) {
	// OKDP's defaults for this chart, then what the mapping computed.
	okdpDefaults := M{}
	if spec.Defaults != "" {
		var err error
		if okdpDefaults, err = readValues(files, path.Clean(spec.Defaults)); err != nil {
			return nil, nil, err
		}
	}
	out := merged(okdpDefaults, computed)
	// Last, the instance's upstream values (upstream.<chart>), when the chart
	// accepts them, checked ("{{", protect, append). The mapping has seen
	// them; these checks are what the platform enforces whatever the mapping
	// does with them.
	var appended [][]string
	if spec.Upstream {
		user := value.AsMap(value.AsMap(values["upstream"])[key])
		var err error
		if appended, err = applyUpstream(out, user, source{chart: key}, spec.Protect, spec.Append); err != nil {
			return nil, nil, err
		}
	}
	// Every chart gets the instance's global, as a subchart would; none
	// when it is empty: Helm gives every chart one.
	delete(out, "global")
	if g := merged(value.AsMap(values["global"]), value.AsMap(computed["global"])); len(g) > 0 {
		out["global"] = g
	}
	return out, appended, nil
}

// merged: values layers merged as Helm merges -f files, into a new map. No
// argument is changed.
func merged(layers ...M) M {
	out := M{}
	for _, l := range layers {
		out = value.Merge(out, l)
	}
	return out
}

// checkKey: the final values of a key (final) against its upstream chart
// u (source): the violations of its values.schema.json by the values
// coalesced over its defaults, as Helm sees them when it renders (Helm's
// report, "" when they hold); and a warning for each list of the instance
// appended to (appended, the paths) where the chart's defaults hold a list
// some item of which the final list lacks: Helm replaces the chart's list,
// and append extends the package's only.
func checkKey(u *upstreamChart, key string, src Ref, final M, appended [][]string) (string, []string, error) {
	var warnings []string
	for _, p := range appended {
		defaults, _ := valueAt(u.defaults, p).([]any)
		list, _ := valueAt(final, p).([]any)
		lost := 0
		for _, item := range defaults {
			if !slices.ContainsFunc(list, func(x any) bool { return reflect.DeepEqual(x, item) }) {
				lost++
			}
		}
		if lost == 0 {
			continue
		}
		items := fmt.Sprintf("%d of its %d items", lost, len(defaults))
		if lost == len(defaults) {
			items = "all its items"
		}
		warnings = append(warnings, fmt.Sprintf("%s: appended to the package's list, not to the default list of %s, which Helm replaces: the final list lacks %s; restate them in the package's values of %s (its defaults or values file) to keep them", source{chart: key}.at(formatPath(p)), src.id(), items, key))
	}
	// Checked as the umbrella's subchart: Helm gives it a global, empty
	// when the values have none.
	values := coalesceValues(u.defaults, final)
	if _, ok := values["global"]; !ok {
		values["global"] = M{}
	}
	v, err := checkSchema(u.entry+"/values.schema.json", u.schema, values)
	return v, warnings, err
}

// valueAt: the value at the keys of v, nil when there is none.
func valueAt(v any, keys []string) any {
	for _, k := range keys {
		m, ok := v.(M)
		if !ok {
			return nil
		}
		v = m[k]
	}
	return v
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
// ones, after the {{ and protect checks; the paths of the lists appended
// to (mergeUpstream).
func applyUpstream(values, user M, src source, protect, appendPaths []string) ([][]string, error) {
	if len(user) == 0 {
		return nil, nil
	}
	// No templates: the values reach the chart as data.
	if err := NoTemplate(user, src.at); err != nil {
		return nil, err
	}
	// Refuse any protected path, or a parent of it given as a non-map. A
	// path may name a key containing dots, or a family of keys, in brackets
	// (paths.go): the message names the key the value was found at, and the
	// path that protects it.
	for _, p := range append([]string{"global"}, protect...) {
		segs, err := parsePath(p)
		if err != nil {
			return nil, fmt.Errorf("protect %q: %w", p, err)
		}
		if at := protectedAt(user, segs); at != nil {
			return nil, fmt.Errorf("%s: %s is set by the platform and cannot be changed", src.at(formatPath(at)), p)
		}
	}
	return mergeUpstream(values, user, src, appendPaths)
}

// NoTemplate: no key or string of v, at any depth, contains "{{". Values
// reach a chart as data, and some charts pass them to tpl, where template
// code could call lookup and read the cluster's Secrets. The first one
// found, in path order (keys sorted, items in order), fails, at(p) naming
// where: p, its path in v (value.Key, value.Index). A value of a type
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
			kp := value.Key(p, k)
			if strings.Contains(k, "{{") {
				return fmt.Errorf("%s: the key contains \"{{\"", at(kp))
			}
			if err := noTemplate(t[k], kp, at); err != nil {
				return err
			}
		}
	case []any:
		for i, x := range t {
			if err := noTemplate(x, value.Index(p, i), at); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("%s: cannot be checked for \"{{\": %T is not a values type", at(p), v)
	}
	return nil
}

// Overlap: the first path, in path order (keys sorted), at which layer sets
// a value base sets, "" when there is none: a key of both whose values are
// not both maps (a null, a list, a map over a scalar), or whose path whole
// names, a map then taken as one value. The path is written as NoTemplate
// writes it (formatPath).
func Overlap(base, layer M, whole func(keys []string) bool) string {
	return overlap(base, layer, nil, whole)
}

// overlap: Overlap below the keys at.
func overlap(base, layer M, at []string, whole func([]string) bool) string {
	for _, k := range slices.Sorted(maps.Keys(layer)) {
		b, ok := base[k]
		if !ok {
			continue
		}
		p := append(slices.Clip(at), k)
		bm, bok := b.(M)
		lm, lok := layer[k].(M)
		if !bok || !lok || whole(p) {
			return formatPath(p)
		}
		if s := overlap(bm, lm, p, whole); s != "" {
			return s
		}
	}
	return ""
}

// mergeUpstream: user over values, the lists at appendPaths appended to the
// computed ones instead of replacing them; the paths of these lists, in
// the order of appendPaths, each once. values is changed only when it
// succeeds; user never is.
func mergeUpstream(values, user M, src source, appendPaths []string) ([][]string, error) {
	user = value.AsMap(value.Copy(user))
	// Listed paths are appended to the computed lists instead of replacing
	// them; a path may name keys in brackets, globs included (paths.go).
	var done [][]string
	for _, p := range appendPaths {
		segs, err := parsePath(p)
		if err != nil {
			return nil, fmt.Errorf("append %q: %w", p, err)
		}
		if err := appendLists(user, values, segs, nil, src, &done); err != nil {
			return nil, err
		}
	}
	// The rest replaces the computed values.
	value.Merge(values, user)
	return done, nil
}
