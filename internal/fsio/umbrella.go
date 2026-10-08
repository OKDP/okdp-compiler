// Umbrella: a compile as one chart for the whole instance, an umbrella over
// the upstream charts of its keys: one Helm release per instance, under the
// release name its values were compiled for.
package fsio

import (
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/okdp/okdp-compiler/internal/chart"
	"github.com/okdp/okdp-compiler/internal/okdp"
)

// UmbrellaValues: the umbrella's values file, given to Helm as user values
// (helm -f, Argo CD valueFiles, Flux valuesFrom). As the chart's own
// values.yaml they would be defaults, and Helm drops the nulls of defaults:
// a null that deletes an upstream default would be lost.
const UmbrellaValues = "instance.values.yaml"

// umbrellaChartFile: the umbrella's Chart.yaml.
const umbrellaChartFile = "Chart.yaml"

// umbrellaChart: the Chart.yaml of an umbrella, in Helm's key order.
type umbrellaChart struct {
	APIVersion   string               `yaml:"apiVersion"`
	Name         string               `yaml:"name"`
	Version      string               `yaml:"version"`
	Dependencies []umbrellaDependency `yaml:"dependencies"`
}

// umbrellaDependency: the upstream chart of a key.
type umbrellaDependency struct {
	Name       string `yaml:"name"`
	Alias      string `yaml:"alias,omitempty"`
	Repository string `yaml:"repository"`
	Version    string `yaml:"version"`
}

// umbrella: umbrellaChartFile, the chart of the instance, named and
// versioned after service.yaml (Result.Service, Result.Version), with one
// dependency per compiled key: its upstream chart, aliased to the key when
// the key is not the chart's name (Helm then names the subchart after the
// key: its .Chart.Name is the key); and UmbrellaValues, the final values of
// each key under the key, each with its own global, marked generated
// (valuesFile). Indented by indent spaces.
func umbrella(res *okdp.Result, indent int) (map[string][]byte, error) {
	switch {
	case res.Service == "":
		return nil, errors.New("Result.Service is required: the name of the umbrella chart, service.yaml's")
	case !chart.IsVersion(res.Version):
		return nil, fmt.Errorf("service.yaml: version %q: the version of the umbrella chart, SemVer (1.2.3)", res.Version)
	}
	c := umbrellaChart{APIVersion: "v2", Name: res.Service, Version: res.Version}
	values := map[string]any{}
	for _, key := range slices.Sorted(maps.Keys(res.Values)) {
		ref := res.Charts[key]
		d := umbrellaDependency{Name: ref.Chart, Repository: ref.Repository, Version: ref.Version}
		if key != ref.Chart {
			d.Alias = key
		}
		c.Dependencies = append(c.Dependencies, d)
		values[key] = res.Values[key]
	}
	chartFile, err := marshal(c, indent)
	if err != nil {
		return nil, err
	}
	instanceValues, err := valuesFile(values, "", indent)
	if err != nil {
		return nil, err
	}
	return map[string][]byte{umbrellaChartFile: chartFile, UmbrellaValues: instanceValues}, nil
}
