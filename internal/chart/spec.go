// Spec: the charts section of service.yaml: each chart key, the upstream
// chart that renders its values, OKDP's defaults for it, and the instance upstream
// values it accepts, with their protect and append paths.
package chart

import (
	"fmt"
	"maps"
	"slices"

	"github.com/okdp/okdp-compiler/internal/helmfn"
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
	Upstream bool     `yaml:"upstream"` // instance upstream values accepted: upstream.<key> of the instance's values
}

// Check: what the charts of service.yaml may not hold, refused when it is
// loaded and before every compile: a key that is not a DNS label (it names
// files, which ../x would place out of their directory, and the umbrella's
// dependency alias), a chart without its source, a protect or append path
// that does not follow the grammar of paths.go (a malformed path would
// otherwise surface only when an instance sets a value under it, or never,
// leaving a value unprotected).
func (s Specs) Check() error {
	for _, key := range slices.Sorted(maps.Keys(s)) {
		spec := s[key]
		if !helmfn.IsDNSLabel(key) {
			return fmt.Errorf("service.yaml: charts: key %q: a DNS label (lowercase letters, digits and -, a letter or digit at each end, 63 characters at most): the key names the umbrella's dependency alias", key)
		}
		if err := spec.Source.Check("service.yaml: charts." + key + ".source"); err != nil {
			return err
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
