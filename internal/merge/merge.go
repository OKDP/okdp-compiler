// This file computes the final values of each upstream chart: the values
// file Helm renders it with. Three layers are merged as Helm merges its -f
// files, and the instance's global is copied into each chart's values:
//
//	OKDP's defaults < computed values < instance upstream values
//
// The chart's own defaults are not a layer: Helm coalesces the values over
// them when it renders the chart. A check reads them, with the chart's
// schema, from the chart cache.
//
// The instance's upstream values are upstream.<chart> of its values. The
// mapping sees them as written. They are applied after the mapping, behind
// two checks: the package's (protect) and the caller's (UpstreamCheck).
package merge

import (
	"fmt"
	"io/fs"
	"maps"
	"path"
	"reflect"
	"slices"

	"github.com/okdp/okdp-compiler/chart"
	"github.com/okdp/okdp-compiler/value"
)

// FinalValues returns the final values of each chart key of the mapping's
// output, and the upstream chart that renders each key. Every key of
// output must be a key of specs. The keys are handled in key order, so the
// first error is the same from one compile to the next.
//
//   - charts is the chart cache; nil reads no chart. With a cache, the
//     final values of each key are checked against its upstream chart: the
//     schema violations of every key are reported together, and the
//     warnings are returned in key order. The final values are the same
//     with or without a cache.
//   - unfetched holds the charts a chart source could not fetch
//     (Inputs.ChartFailures). Their keys skip the check against their
//     chart only.
//   - file is the values file the output is read from, for the errors.
//   - values holds the instance's merged values (global, upstream).
//   - check is the caller's UpstreamCheck; nil refuses nothing.
func FinalValues(charts *ChartCache, unfetched map[Ref]error, pkg fs.FS, specs Specs, file string, output M, values M, check UpstreamCheck) (map[string]map[string]any, map[string]Ref, []string, error) {
	out, refs := map[string]map[string]any{}, map[string]Ref{}
	violations := map[string]string{}
	var warnings []string
	for _, key := range slices.Sorted(maps.Keys(output)) {
		spec, ok := specs[key]
		if !ok {
			return nil, nil, nil, fmt.Errorf("%s: %s: not a dependency of Chart.yaml", file, key)
		}
		f, appended, err := keyValues(pkg, key, spec, value.AsMap(output[key]), values, check)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("%s: %w", key, err)
		}
		out[key], refs[key] = f, spec.Source
		if charts == nil || unfetched[spec.Source] != nil {
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
		violations[key+" ("+spec.Source.ID()+")"] = bad
		warnings = append(warnings, w...)
	}
	if err := chart.SchemaError("compiled values", violations); err != nil {
		return nil, nil, nil, err
	}
	return out, refs, warnings, nil
}

// keyValues returns what the upstream chart is rendered with: OKDP's
// defaults, the computed values and the instance's upstream values, merged
// as Helm merges -f files (maps merge, lists and scalars replace, a null
// replaces and stays). It also returns the paths of the instance's lists
// that were appended, for checkKey.
func keyValues(files fs.FS, key string, spec Spec, computed M, values M, check UpstreamCheck) (M, [][]string, error) {
	// First OKDP's defaults for this chart, then what the mapping computed.
	okdpDefaults := M{}
	if spec.Defaults != "" {
		var err error
		if okdpDefaults, err = chart.ReadValues(files, path.Clean(spec.Defaults)); err != nil {
			return nil, nil, err
		}
	}
	out := merged(okdpDefaults, computed)
	// Last come the instance's upstream values, when the chart accepts
	// them. The mapping has already seen them; the checks here are what the
	// platform enforces whatever the mapping does with them.
	var appended [][]string
	if spec.Upstream {
		user := value.AsMap(value.AsMap(values["upstream"])[key])
		var err error
		if appended, err = applyUpstream(out, user, source{chart: key}, spec.Protect, spec.Append, check); err != nil {
			return nil, nil, err
		}
	}
	// Every chart gets the instance's global, as a subchart would. An empty
	// global is left out: Helm gives every chart one.
	delete(out, "global")
	if g := merged(value.AsMap(values["global"]), value.AsMap(computed["global"])); len(g) > 0 {
		out["global"] = g
	}
	return out, appended, nil
}

// merged merges values layers into a new map, as Helm merges -f files.
func merged(layers ...M) M {
	out := M{}
	for _, l := range layers {
		out = value.Merge(out, l)
	}
	return out
}

// checkKey checks the final values of a key against its upstream chart u.
// It returns the violations of the chart's schema ("" when the values
// hold), and a warning for each appended list where the chart's defaults
// hold items the final list lacks: Helm replaces the chart's list, and
// append extends the package's list only.
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
		warnings = append(warnings, fmt.Sprintf("%s: appended to the package's list, not to the default list of %s, which Helm replaces: the final list lacks %s; restate them in the package's values of %s (its defaults or values file) to keep them", source{chart: key}.at(chart.FormatPath(p)), src.ID(), items, key))
	}
	// The chart is checked as a subchart of the umbrella: Helm gives it a
	// global, empty when the values have none.
	values := chart.Coalesce(u.defaults, final)
	if _, ok := values["global"]; !ok {
		values["global"] = M{}
	}
	v, err := u.validate(values)
	return v, warnings, err
}

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

// source names where instance upstream values come from, for the errors.
type source struct{ chart string }

// at names the path p of the values of s. A path that starts with a key in
// brackets (FormatPath) is joined without a dot.
func (s source) at(p string) string {
	switch {
	case p == "":
		return "upstream." + s.chart
	case p[0] == '[':
		return "upstream." + s.chart + p
	}
	return "upstream." + s.chart + "." + p
}

// applyUpstream applies instance upstream values over the computed ones,
// after the "{{" and protect checks, then check (nil for none). It returns
// the paths of the lists that were appended.
func applyUpstream(values, user M, src source, protect, appendPaths []string, check UpstreamCheck) ([][]string, error) {
	if len(user) == 0 {
		return nil, nil
	}
	// No templates: the values reach the chart as data.
	if err := chart.NoTemplate(user, src.at); err != nil {
		return nil, err
	}
	// Refuse a value at a protected path, or a non-map given for a parent
	// of it. The message names the key the value was found at, and the path
	// that protects it.
	for _, p := range append([]string{"global"}, protect...) {
		segs, err := chart.ParsePath(p)
		if err != nil {
			return nil, fmt.Errorf("protect %q: %w", p, err)
		}
		if at := protectedAt(user, segs); at != nil {
			return nil, fmt.Errorf("%s: %s is set by the platform and cannot be changed", src.at(chart.FormatPath(at)), p)
		}
	}
	if check != nil {
		if err := check(user, values, src.at); err != nil {
			return nil, err
		}
	}
	return mergeUpstream(values, user, src, appendPaths)
}

// mergeUpstream merges user over values, except the lists at appendPaths,
// which are appended to the computed lists. It returns the paths of these
// lists, each once. values is changed only on success; user never is.
func mergeUpstream(values, user M, src source, appendPaths []string) ([][]string, error) {
	user = value.AsMap(value.Copy(user))
	var done [][]string
	for _, p := range appendPaths {
		segs, err := chart.ParsePath(p)
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
