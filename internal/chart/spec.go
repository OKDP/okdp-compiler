// Spec: each dependency of a package's Chart.yaml by key, the upstream
// chart that renders its values, with okdp.yaml's defaults and upstream:
// OKDP's defaults for it, and the instance upstream values it accepts, with
// their protect and append paths.
package chart

import (
	"errors"
	"fmt"
	"maps"
	"slices"

	"go.yaml.in/yaml/v3"
)

// Specs: the dependencies of a package, by key (NewSpecs).
type Specs map[string]Spec

// Spec: one upstream chart of a service, by key: the key names its
// values (the values file, the final values), the upstream chart renders
// them.
type Spec struct {
	Source   Ref      // the upstream chart: its defaults and schema (the chart cache), and what renders the values
	Defaults string   // a values file of the package, <key>.defaults.yaml by convention: OKDP's defaults for the chart
	Protect  []string // instance upstream values: protected paths (paths.go: dotted keys, ["literal.key"], ["prefix*"])
	Append   []string // instance upstream values: appended lists (same paths)
	Upstream bool     // instance upstream values accepted: upstream.<key> of the instance's values
}

// Upstream: okdp.yaml's upstream, the dependencies that accept instance
// upstream values: All (true: every dependency, nothing protected or
// appended), or by dependency key (Keys). Absent or false: none.
type Upstream struct {
	All  bool
	Keys map[string]UpstreamKey
}

// UpstreamKey: a dependency's entry of upstream: true or {} accepts its
// upstream values, false does not; {protect, append} accepts them, with
// these paths.
type UpstreamKey struct {
	Accept  bool
	Protect []string
	Append  []string
}

// UnmarshalYAML: upstream, a boolean (a YAML 1.1 one, off or yes, read as
// the one it spells) or a map by dependency key of booleans and
// {protect, append}, those two keys only: a misspelt protect would
// otherwise leave its paths unprotected.
func (u *Upstream) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode && n.Decode(&u.All) == nil {
		return nil
	}
	if n.Kind != yaml.MappingNode {
		return errors.New("upstream: true, false or a map by dependency")
	}
	var keys map[string]yaml.Node // a key written twice fails
	if err := n.Decode(&keys); err != nil {
		return err
	}
	u.Keys = map[string]UpstreamKey{}
	for _, key := range slices.Sorted(maps.Keys(keys)) {
		v := keys[key]
		k, ok, err := upstreamKey(&v)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("upstream.%s: true, false or {protect, append}", key)
		}
		u.Keys[key] = k
	}
	return nil
}

// upstreamKey: the entry n of upstream; ok false when it has none of the
// forms of UpstreamKey.
func upstreamKey(n *yaml.Node) (k UpstreamKey, ok bool, err error) {
	switch n.Kind {
	case yaml.ScalarNode:
		ok = n.ShortTag() != "!!null" && n.Decode(&k.Accept) == nil
	case yaml.MappingNode:
		var fields map[string]yaml.Node // a key written twice fails
		if err := n.Decode(&fields); err != nil {
			return k, false, err
		}
		for f := range fields {
			if f != "protect" && f != "append" {
				return k, false, nil
			}
		}
		var paths struct{ Protect, Append []string }
		err = n.Decode(&paths)
		k, ok = UpstreamKey{Accept: true, Protect: paths.Protect, Append: paths.Append}, true
	}
	return k, ok, err
}

// NewSpecs: the dependencies of meta, a checked Chart.yaml, by key, their
// source the chart each names, with okdp.yaml's defaults and upstream
// (keys of dependencies only), then checked (Specs.Check).
func NewSpecs(meta Metadata, defaults map[string]string, upstream Upstream) (Specs, error) {
	s := Specs{}
	for _, d := range meta.Dependencies {
		s[d.Key()] = Spec{Source: d.Ref(), Defaults: defaults[d.Key()], Upstream: upstream.All}
	}
	for _, key := range slices.Sorted(maps.Keys(defaults)) {
		if _, ok := s[key]; !ok {
			return nil, fmt.Errorf("okdp.yaml: defaults.%s: not a dependency of Chart.yaml", key)
		}
	}
	for _, key := range slices.Sorted(maps.Keys(upstream.Keys)) {
		spec, ok := s[key]
		if !ok {
			return nil, fmt.Errorf("okdp.yaml: upstream.%s: not a dependency of Chart.yaml", key)
		}
		u := upstream.Keys[key]
		spec.Upstream, spec.Protect, spec.Append = u.Accept, u.Protect, u.Append
		s[key] = spec
	}
	return s, s.Check()
}

// Check: a protect or append path that does not follow the grammar of
// paths.go, refused when the package is loaded and before every compile: a
// malformed path would otherwise surface only when an instance sets a
// value under it, or never, leaving a value unprotected.
func (s Specs) Check() error {
	for _, key := range slices.Sorted(maps.Keys(s)) {
		spec := s[key]
		for _, l := range []struct {
			name  string
			paths []string
		}{{"protect", spec.Protect}, {"append", spec.Append}} {
			for i, p := range l.paths {
				if _, err := parsePath(p); err != nil {
					return fmt.Errorf("okdp.yaml: upstream.%s.%s[%d] %q: %w", key, l.name, i, p, err)
				}
			}
		}
	}
	return nil
}
