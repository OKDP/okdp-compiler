// Merge: the final values of each upstream chart, the values file Helm
// renders it with: upstream defaults < base values < computed values <
// instance upstream values files < inline instance upstream values, global
// copied down. The layers are merged as Helm merges its -f files.
//
// The instance's upstream values are read once, before the mapping runs
// (readUpstreamValues): the mapping sees them merged (values.upstream.<chart>,
// context.go), and they are applied with their checks after it
// (finalValues).
package compiler

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"
)

// finalValues: what the upstream chart is rendered with (`helm template
// <source> -f <final values>`). The base values, the computed values and the
// instance's upstream values are merged as Helm merges -f files (MergeMaps:
// maps merge, lists and scalars replace, a null replaces and stays), over
// the chart's own defaults without their nulls. Rendered by Helm, these
// values give exactly what `helm template <source> -f base -f computed -f
// <upstream files> -f <inline upstream>` gives, with Helm 3 or 4: a null
// that deletes a default stays null (Helm deletes the key when it renders),
// a null where there is no default stays (Helm keeps it), and the null
// defaults are left out (Helm 4 drops them, Helm 3 copies them back).
//
// The violations of the chart's schema (its values.schema.json) by these
// values, as Helm sees them when it renders, are returned apart.
//
// charts: the chart cache, the chart's defaults and schema. files: the
// package. ups: the instance's upstream values, by the chart that accepts
// them (readUpstreamValues).
func finalValues(charts *chartCache, files fs.FS, svc *Service, chart string, computed M, values M, ups map[string]*chartUpstream) (M, string, error) {
	spec := svc.Charts[chart]
	upstream, err := charts.get(spec.Source)
	if err != nil {
		return nil, "", err
	}
	defaults := upstream.defaults
	// Upstream chart defaults first, then OKDP's default values for this
	// chart, then what the mapping computed.
	base := M{}
	if spec.Base != "" {
		if base, err = readValues(files, path.Clean(spec.Base)); err != nil {
			return nil, "", err
		}
	}
	out := layered(defaults, base, computed)
	// Last, the instance's upstream values, when the chart accepts them: its
	// files, then its inline values, each checked ("{{", protect, append).
	// The mapping has seen the same values merged; these checks are what the
	// platform enforces whatever the mapping does with them. A chart with
	// upstream.from receives the values of the chart it names, with that
	// chart's protect and append: one set of values for two renders of an
	// upstream chart, refused or appended the same way in both.
	owner := svc.upstreamOf(chart)
	if up := ups[owner]; up != nil {
		if up.err != nil {
			return nil, "", up.err
		}
		rules := svc.Charts[owner]
		for _, l := range up.layers {
			if err := applyUpstream(out, l.values, l.src, rules.Protect, rules.Append); err != nil {
				return nil, "", err
			}
		}
	}
	// Every chart gets the instance's global, as a subchart would.
	out["global"] = layered(asMap(defaults["global"]), asMap(values["global"]), asMap(computed["global"]))
	v, err := checkSchema(spec.Source.id()+"/values.schema.json", upstream.schema, coalesceValues(defaults, out))
	return out, v, err
}

// layered: values layers merged as Helm merges -f files, over chart
// defaults without their nulls: what Helm renders the chart with is the same
// as with the layers given as -f files. No argument is changed.
func layered(defaults M, layers ...M) M {
	out := cleanNil(asMap(deepCopy(defaults)))
	for _, l := range layers {
		out = mergeMaps(out, l)
	}
	return out
}

// chartUpstream: the instance's upstream values of one chart, in the order
// they are applied: its files, then its inline upstream.<chart> values.
//
// They are read before the mapping runs, not only merged after it, so that
// the mapping sees all of them: a rule of its errors section on an upstream
// value (a key no protect path names, a value that depends on the instance)
// would otherwise check the inline values only, and the same value written
// in upstream.<chart>.yaml would bypass it.
type chartUpstream struct {
	layers []upstreamLayer
	// err: a file that cannot be read (missing, not YAML). It is reported
	// when the chart is compiled, as when the files were read after the
	// mapping: a chart the mapping leaves out does not need them.
	err error
}

// upstreamLayer: one source of a chart's upstream values: an instance file,
// or the inline values.
type upstreamLayer struct {
	src    source
	values M
}

// readUpstreamValues: the upstream values of each chart of service.yaml that
// accepts them, by chart: the files its upstream entry names
// (upstream.<chart>.yaml, when it exists, by default), then the inline
// upstream.<chart> of the merged instance values. Empty sources are left
// out, as applyUpstream ignores them.
func readUpstreamValues(in Inputs, svc *Service, values M) map[string]*chartUpstream {
	inline := asMap(values["upstream"])
	out := map[string]*chartUpstream{}
	for chart, spec := range svc.Charts {
		if !spec.Upstream.Enabled {
			continue
		}
		up := &chartUpstream{}
		files, optional := spec.Upstream.Files, spec.Upstream.Files == nil
		if optional {
			files = []string{"upstream." + chart + ".yaml"}
		}
		for _, f := range files {
			u, err := readUpstream(in.Instance, path.Clean(f), optional)
			if err != nil {
				if up.err == nil {
					up.err = err
				}
				continue
			}
			if len(u) > 0 {
				up.layers = append(up.layers, upstreamLayer{source{file: f}, u})
			}
		}
		if u := asMap(inline[chart]); len(u) > 0 {
			up.layers = append(up.layers, upstreamLayer{source{chart: chart}, u})
		}
		out[chart] = up
	}
	return out
}

// view: the chart's upstream values as the mapping sees them
// (values.upstream.<chart>): the sources merged in order as applyUpstream
// merges them into the chart's values, the lists of appendPaths appended,
// so that a rule of the mapping checks what will be applied. Unchecked:
// protect and the "{{" check apply when the chart is compiled.
func (up *chartUpstream) view(appendPaths []string) M {
	v := M{}
	for _, l := range up.layers {
		if err := mergeUpstream(v, l.values, l.src, appendPaths); err != nil {
			// A non-list on an append path fails the chart's compile; the
			// mapping sees it as given.
			mergeMaps(v, l.values)
		}
	}
	return v
}

// readUpstream: an upstream values file of the instance. An optional file
// may be missing, as may the instance files altogether.
func readUpstream(instance fs.FS, name string, optional bool) (M, error) {
	if instance == nil {
		if optional {
			return nil, nil
		}
		return nil, fmt.Errorf("upstream file %s: no instance files (Inputs.Instance)", name)
	}
	u, err := readValues(instance, name)
	if optional && errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return u, err
}

// source: where instance upstream values come from, for the errors: an
// instance file, or the inline upstream.<chart> values.
type source struct{ file, chart string }

// at: the path p of the values of s (formatPath: a key in brackets joins
// without a dot).
func (s source) at(p string) string {
	switch {
	case s.file != "" && p == "":
		return s.file
	case s.file != "":
		return s.file + ": " + p
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
	if b, _ := json.Marshal(user); strings.Contains(string(b), "{{") {
		return fmt.Errorf("%s: a key or value contains \"{{\"", src.at(""))
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

// mergeUpstream: user over values, the lists at appendPaths appended to the
// computed ones instead of replacing them. values is changed only when it
// succeeds; user never is.
func mergeUpstream(values, user M, src source, appendPaths []string) error {
	user = asMap(deepCopy(user))
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
	mergeMaps(values, user)
	return nil
}
