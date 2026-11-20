// This file joins each dependency of Chart.yaml with what okdp.yaml says
// of it.

package chart

import (
	"errors"
	"fmt"
	"maps"
	"slices"

	"go.yaml.in/yaml/v3"
)

// Specs holds the dependencies of a package, by key (NewSpecs).
type Specs map[string]Spec

// Spec is one upstream chart of a service. Its key names its values, and
// the upstream chart renders them.
type Spec struct {
	Source   Ref      // the upstream chart, which renders the values
	Defaults string   // a values file of the package with OKDP's defaults for the chart
	Protect  []string // the paths instance upstream values may not set (paths.go)
	Append   []string // the paths of the lists instance upstream values append to
	Upstream bool     // whether the chart accepts instance upstream values (upstream.<key>)
}

// Upstream is the upstream section of okdp.yaml: the dependencies that
// accept instance upstream values. All accepts them for every dependency,
// with nothing protected or appended. An absent section, or false, accepts
// none.
type Upstream struct {
	All  bool
	Keys map[string]UpstreamKey
}

// UpstreamKey is one dependency's entry of upstream: true or {} accepts
// its upstream values, false does not, and {protect, append} accepts them
// with these paths.
type UpstreamKey struct {
	Accept  bool
	Protect []string
	Append  []string
}

// UnmarshalYAML decodes upstream: a boolean, or a map by dependency key.
// An entry may hold protect and append only: a misspelt protect would
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
		k, ok, err := upstreamEntry(&v)
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

// upstreamEntry decodes an entry; ok is false when n is no form of
// UpstreamKey.
func upstreamEntry(n *yaml.Node) (k UpstreamKey, ok bool, err error) {
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

// NewSpecs builds the Specs of meta, a checked Chart.yaml: one per
// dependency, by key. It adds the defaults and upstream sections of
// okdp.yaml, which may name keys of dependencies only, then checks the
// result (Specs.Check).
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

// Check refuses a malformed protect or append path (paths.go). It runs
// when the package is loaded and before every compile: the path would
// otherwise fail only when an instance sets a value under it, or never,
// which would leave a value unprotected.
func (s Specs) Check() error {
	for _, key := range slices.Sorted(maps.Keys(s)) {
		spec := s[key]
		for _, l := range []struct {
			name  string
			paths []string
		}{{"protect", spec.Protect}, {"append", spec.Append}} {
			for i, p := range l.paths {
				if _, err := ParsePath(p); err != nil {
					return fmt.Errorf("okdp.yaml: upstream.%s.%s[%d] %q: %w", key, l.name, i, p, err)
				}
			}
		}
	}
	return nil
}
