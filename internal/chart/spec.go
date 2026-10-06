// Spec: the charts section of service.yaml: each chart key, the upstream
// chart that renders its values, OKDP's defaults for it, and the instance upstream
// values it accepts, with their protect and append paths.
package chart

import (
	"fmt"
	"maps"
	"regexp"
	"slices"

	"go.yaml.in/yaml/v3"
)

// Specs: the charts of service.yaml, by key.
type Specs map[string]Spec

// Spec: one upstream chart of a service, by key: the key names its
// values (the mapping's output, the final values), the upstream chart
// renders them.
type Spec struct {
	Source   Ref      `yaml:"source"`   // the upstream chart: its defaults and schema (the chart cache), and what renders the values
	Defaults string   `yaml:"defaults"` // a values file of the package, <key>.defaults.yaml by convention: OKDP's defaults for the chart
	Protect  []string `yaml:"protect"`  // instance upstream values: protected paths (paths.go: dotted keys, ["literal.key"], ["prefix*"])
	Append   []string `yaml:"append"`   // instance upstream values: appended lists (same paths)
	Upstream Upstream `yaml:"upstream"` // instance upstream values accepted
}

// Upstream: the user-defined values a chart accepts. true: the instance's
// upstream.<chart> values.
//
// {from: <chart>} gives the chart the upstream values of another chart of
// service.yaml, with that chart's protect and append: for two renders of
// one upstream chart (two keys of the same source) that an instance sets
// with one set of values (polaris-admin's bootstrap and principals Jobs).
type Upstream struct {
	Enabled bool
	From    string // the chart whose upstream values this one receives (Enabled is false)
}

// upstreamOf: the chart whose instance upstream values the chart key
// receives: itself when it accepts them, the chart its upstream.from names;
// false when it receives none.
func (s Specs) upstreamOf(key string) (string, bool) {
	switch spec := s[key]; {
	case spec.Upstream.From != "":
		return spec.Upstream.From, true
	case spec.Upstream.Enabled:
		return key, true
	}
	return "", false
}

// chartKey: a chart key, a DNS label (RFC 1123): 63 characters at most.
var chartKey = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)

// Check: what the charts of service.yaml may not hold, refused when it is
// loaded and before every compile: a key that is not a DNS label (it names
// files, which ../x would place out of their directory, and the umbrella's
// dependency alias), a chart without its source, a protect or append path
// that does not follow the grammar of paths.go (a malformed path would
// otherwise surface only when an instance sets a value under it, or never,
// leaving a value unprotected), an upstream.from that does not name a chart
// accepting upstream values of its own.
func (s Specs) Check() error {
	for _, key := range slices.Sorted(maps.Keys(s)) {
		spec := s[key]
		if !chartKey.MatchString(key) {
			return fmt.Errorf("service.yaml: charts: key %q: a DNS label (lowercase letters, digits and -, a letter or digit at each end, 63 characters at most): the key names files (<key>.values.yaml) and the umbrella's dependency alias", key)
		}
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
				return fmt.Errorf("service.yaml: charts.%s.upstream.from %q: that chart accepts no upstream values of its own (upstream: true)", key, from)
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

// yaml11Bools: the plain scalars YAML 1.1 reads as booleans and YAML 1.2
// as strings, true and false aside.
var yaml11Bools = []string{"y", "Y", "yes", "Yes", "YES", "n", "N", "no", "No", "NO", "on", "On", "ON", "off", "Off", "OFF"}

// UnmarshalYAML: true or false, or {from: <chart>}. service.yaml is YAML
// 1.2, where a plain no or off is a string: refused with what to write
// instead.
func (u *Upstream) UnmarshalYAML(n *yaml.Node) error {
	switch {
	case n.Kind == yaml.ScalarNode && n.Tag == "!!str" && n.Style == 0 && slices.Contains(yaml11Bools, n.Value):
		return fmt.Errorf("line %d: upstream %s: write true or false (service.yaml is read as YAML 1.2, where %s is not a boolean)", n.Line, n.Value, n.Value)
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
	}
	return fmt.Errorf("line %d: upstream must be true, false or {from: <chart>}", n.Line)
}
