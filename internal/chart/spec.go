// Spec: the charts section of service.yaml: each chart key, the upstream
// chart that renders its values, its base values, and the instance upstream
// values it accepts, with their protect and append paths.
package chart

import (
	"fmt"
	"maps"
	"slices"

	"gopkg.in/yaml.v3"
)

// Specs: the charts of service.yaml, by key.
type Specs map[string]Spec

// Spec: one upstream chart of a service, by key: the key names its
// values (the mapping's output, the final values), the upstream chart
// renders them.
type Spec struct {
	Source   Ref      `yaml:"source"`   // the upstream chart: its defaults and schema (the chart cache), and what renders the values
	Base     string   `yaml:"base"`     // a values file of the package, base/<key>.yaml by convention (OKDP's default values)
	Protect  []string `yaml:"protect"`  // instance upstream values: protected paths (paths.go: dotted keys, ["literal.key"], ["prefix*"])
	Append   []string `yaml:"append"`   // instance upstream values: appended lists (same paths)
	Upstream Upstream `yaml:"upstream"` // instance upstream values accepted, and their files
}

// Upstream: the user-defined values a chart accepts. true reads
// upstream.<chart>.yaml of the instance when it exists; a path, or a list of
// paths, reads these files instead (they must exist), merged in order. The
// instance's inline upstream.<chart> values come after the files.
//
// {from: <chart>} gives the chart the upstream values of another chart of
// service.yaml, with that chart's protect and append: for two renders of
// one upstream chart (two keys of the same source) that an instance sets
// with one set of values (polaris-admin's bootstrap and principals Jobs). The
// mapping used to merge them into the second render by hand, which could
// not see the instance files, so the files had to be refused.
type Upstream struct {
	Enabled bool
	Files   []string // nil: upstream.<chart>.yaml, if it exists
	From    string   // the chart whose upstream values this one receives (Enabled is false)
}

// upstreamOf: the chart whose instance upstream values the chart key
// receives: itself when it accepts them, the chart its upstream.from names,
// or "" when it receives none.
func (s Specs) upstreamOf(key string) string {
	switch spec := s[key]; {
	case spec.Upstream.From != "":
		return spec.Upstream.From
	case spec.Upstream.Enabled:
		return key
	}
	return ""
}

// Check: what the charts of service.yaml may not hold, refused when it is
// loaded and before every compile: a chart without its source, a protect or
// append path that does not follow the grammar of paths.go (a malformed path
// would otherwise surface only when an instance sets a value under it, or
// never, leaving a value unprotected), an upstream.from that does not name a
// chart accepting upstream values of its own.
func (s Specs) Check() error {
	for _, key := range slices.Sorted(maps.Keys(s)) {
		spec := s[key]
		if err := spec.Source.Check("service.yaml: charts." + key + ".source"); err != nil {
			return err
		}
		if from := spec.Upstream.From; from != "" {
			target, ok := s[from]
			switch {
			case from == key:
				return fmt.Errorf("service.yaml: charts.%s.upstream.from: the chart itself (upstream: true accepts its own values)", key)
			case !ok:
				return fmt.Errorf("service.yaml: charts.%s.upstream.from %q: not a chart of service.yaml", key, from)
			case !target.Upstream.Enabled:
				return fmt.Errorf("service.yaml: charts.%s.upstream.from %q: that chart accepts no upstream values of its own (upstream: true, or files)", key, from)
			// One set of values, one set of rules: what the instance may set
			// is decided once, on the chart that owns the values.
			case len(spec.Protect) > 0 || len(spec.Append) > 0:
				return fmt.Errorf("service.yaml: charts.%s: protect and append come with upstream.from, from charts.%s: list them there", key, from)
			}
		}
		for _, l := range []struct {
			name  string
			paths []string
		}{{"protect", spec.Protect}, {"append", spec.Append}} {
			for i, p := range l.paths {
				if _, err := parsePath(p); err != nil {
					return fmt.Errorf("service.yaml: charts.%s.%s[%d] %q: %w", key, l.name, i, p, err)
				}
			}
		}
	}
	return nil
}

// UnmarshalYAML: true or false, a path or a list of paths, or {from:
// <chart>}.
func (u *Upstream) UnmarshalYAML(n *yaml.Node) error {
	switch {
	case n.Kind == yaml.MappingNode:
		if len(n.Content) != 2 || n.Content[0].Value != "from" || n.Content[1].Kind != yaml.ScalarNode || n.Content[1].Tag != "!!str" || n.Content[1].Value == "" {
			break
		}
		u.From = n.Content[1].Value
		return nil
	case n.Kind == yaml.ScalarNode && n.Tag == "!!null":
		return nil
	case n.Kind == yaml.ScalarNode && n.Tag == "!!bool":
		return n.Decode(&u.Enabled)
	case n.Kind == yaml.ScalarNode && n.Tag == "!!str" && n.Value != "":
		u.Enabled, u.Files = true, []string{n.Value}
		return nil
	case n.Kind == yaml.SequenceNode:
		files := []string{}
		if err := n.Decode(&files); err != nil {
			return err
		}
		if slices.Contains(files, "") {
			break
		}
		u.Enabled, u.Files = true, files
		return nil
	}
	return fmt.Errorf("line %d: upstream must be true, false, a path, a list of paths or {from: <chart>}", n.Line)
}
