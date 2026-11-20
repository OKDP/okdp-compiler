// This file computes the final values of each upstream chart: the values
// file Helm renders it with. Three layers are merged as Helm merges its -f
// files, and the instance's global is copied into each chart's values:
//
//	OKDP's defaults < computed values < instance upstream values
//
// The chart's own defaults are not a layer: Helm coalesces the values over
// them when it renders the chart. The compiler reads no upstream chart: a
// host checks the final values against the chart's defaults and schema
// (okdp-gitops), with the lists an instance appended to (Final.Appended).
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
	"slices"

	"github.com/okdp/okdp-compiler/chart"
	"github.com/okdp/okdp-compiler/value"
)

// Final is what FinalValues makes of a compile.
type Final struct {
	// Values holds the final values of each chart key.
	Values map[string]map[string]any
	// Charts names the upstream chart that renders each key.
	Charts map[string]Ref
	// Appended gives, by key, the paths of the lists of the instance's
	// upstream values that were appended to the package's (append of
	// okdp.yaml), each once, nil when none was. Helm replaces the chart's
	// default list with the final one, so a host warns when the final list
	// lacks items of the chart's.
	Appended map[string][][]string
}

// FinalValues returns the final values of each chart key of the mapping's
// output, the upstream chart that renders each key, and the lists the
// instance appended to. Every key of output must be a key of specs. The
// keys are handled in key order, so the first error is the same from one
// compile to the next.
//
//   - file is the values file the output is read from, for the errors.
//   - values holds the instance's merged values (global, upstream).
//   - check is the caller's UpstreamCheck; nil refuses nothing.
func FinalValues(pkg fs.FS, specs Specs, file string, output M, values M, check UpstreamCheck) (Final, error) {
	out := Final{Values: map[string]map[string]any{}, Charts: map[string]Ref{}}
	for _, key := range slices.Sorted(maps.Keys(output)) {
		spec, ok := specs[key]
		if !ok {
			return Final{}, fmt.Errorf("%s: %s: not a dependency of Chart.yaml", file, key)
		}
		f, appended, err := keyValues(pkg, key, spec, value.AsMap(output[key]), values, check)
		if err != nil {
			return Final{}, fmt.Errorf("%s: %w", key, err)
		}
		out.Values[key], out.Charts[key] = f, spec.Source
		if len(appended) > 0 {
			if out.Appended == nil {
				out.Appended = map[string][][]string{}
			}
			out.Appended[key] = appended
		}
	}
	return out, nil
}

// keyValues returns what the upstream chart is rendered with: OKDP's
// defaults, the computed values and the instance's upstream values, merged
// as Helm merges -f files (maps merge, lists and scalars replace, a null
// replaces and stays). It also returns the paths of the instance's lists
// that were appended (Final.Appended).
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
