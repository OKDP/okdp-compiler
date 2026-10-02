// Umbrella: a compile as one chart for the whole instance, an umbrella over
// the upstream charts of its keys: one Helm release per instance, under the
// release name its values were compiled for.
package fsio

import (
	"fmt"
	"maps"
	"regexp"
	"slices"

	"github.com/okdp/okdp-compiler/internal/chart"
	"github.com/okdp/okdp-compiler/internal/okdp"
)

// UmbrellaValues: the umbrella's values file, given to Helm as user values
// (helm -f, Argo CD valueFiles, Flux valuesFrom). As the chart's own
// values.yaml they would be defaults, and Helm drops the nulls of defaults:
// a null that deletes an upstream default would be lost.
const UmbrellaValues = "instance.values.yaml"

// alias: a key Helm accepts as a dependency alias.
var alias = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// umbrellaChart: the Chart.yaml of an umbrella, in Helm's key order.
type umbrellaChart struct {
	APIVersion   string               `yaml:"apiVersion"`
	Name         string               `yaml:"name"`
	Version      string               `yaml:"version"`
	AppVersion   string               `yaml:"appVersion,omitempty"`
	Dependencies []umbrellaDependency `yaml:"dependencies"`
}

// umbrellaDependency: the upstream chart of a key.
type umbrellaDependency struct {
	Name       string `yaml:"name"`
	Alias      string `yaml:"alias,omitempty"`
	Repository string `yaml:"repository"`
	Version    string `yaml:"version"`
}

// Umbrella: Chart.yaml, the chart of the instance, named and versioned after
// service.yaml, with one dependency per compiled key: its upstream chart,
// aliased to the key when the key is not the chart's name (Helm then names
// the subchart after the key: its .Chart.Name is the key); and
// UmbrellaValues, the final values of each key under the key, each with its
// own global. Indented by indent spaces (2 to 9).
func Umbrella(def *okdp.Definition, res *okdp.Result, indent int) (map[string][]byte, error) {
	svc := def.Service
	if !chart.IsVersion(svc.Version) {
		return nil, fmt.Errorf("service.yaml: version %q: the version of the umbrella chart, SemVer (1.2.3)", svc.Version)
	}
	c := umbrellaChart{APIVersion: "v2", Name: svc.Name, Version: svc.Version, AppVersion: svc.AppVersion}
	values := map[string]any{}
	for _, key := range slices.Sorted(maps.Keys(res.Values)) {
		ref := res.Charts[key]
		d := umbrellaDependency{Name: ref.Chart, Repository: ref.Repository, Version: ref.Version}
		if key != ref.Chart {
			if !alias.MatchString(key) {
				return nil, fmt.Errorf("chart %q: an umbrella dependency alias is letters, digits, - and _", key)
			}
			d.Alias = key
		}
		c.Dependencies = append(c.Dependencies, d)
		values[key] = res.Values[key]
	}
	chartFile, err := marshal(c, indent)
	if err != nil {
		return nil, err
	}
	valuesFile, err := marshal(values, indent)
	if err != nil {
		return nil, err
	}
	return map[string][]byte{"Chart.yaml": chartFile, UmbrellaValues: valuesFile}, nil
}
