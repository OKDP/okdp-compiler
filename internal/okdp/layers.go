// Layers: the values layers of a compile, the platform's (Inputs.Platform)
// then the instance's (Inputs.Values), Go values of any type read as Helm
// values (value.Normalize). global is the platform's: global.okdp
// (the instance's host, where its OAuth clients register, the chart its
// objects are rendered with) and what every chart receives as global
// (chart.FinalValues). An instance sets only the keys of global its
// package's parameter schema declares, never okdp, and no template code:
// the platform's layers and the package's values.yaml are reviewed, the
// instance's are input.
package okdp

import (
	"fmt"
	"io/fs"
	"maps"
	"slices"

	"github.com/okdp/okdp-compiler/internal/chart"
	"github.com/okdp/okdp-compiler/internal/value"
)

// valuesLayers: the layers of in, the platform's then the instance's, as
// values (normalized), for chart.Parameters. An instance layer that sets
// global fails, but for the keys values.schema.json of files declares under
// it (properties.global.properties), okdp excepted; then one with "{{" in a
// key or a string, anywhere (chart.NoTemplate).
func valuesLayers(files fs.FS, in Inputs) ([]map[string]any, error) {
	platform, err := normalized("platform layer", in.Platform)
	if err != nil {
		return nil, err
	}
	instance, err := normalized("values layer", in.Values)
	if err != nil {
		return nil, err
	}
	var declared M // properties.global.properties, read once, when needed
	for i, layer := range instance {
		g, ok := layer["global"]
		if !ok {
			continue
		}
		keys, isMap := g.(M)
		if !isMap {
			return nil, fmt.Errorf("values layer %d: global is set by the platform (--platform, Inputs.Platform)", i+1)
		}
		for _, k := range slices.Sorted(maps.Keys(keys)) {
			if declared == nil {
				schema, err := readJSON(files, "values.schema.json")
				if err != nil {
					return nil, err
				}
				declared = value.AsMap(value.Dig(schema, "properties.global.properties"))
			}
			if _, ok := declared[k]; !ok || k == "okdp" {
				return nil, fmt.Errorf("values layer %d: global.%s is set by the platform (--platform, Inputs.Platform)", i+1, k)
			}
		}
	}
	// No template code in what the instance gives, whatever key it is under:
	// parameters, connections, upstream values (chart.NoTemplate).
	for i, layer := range instance {
		at := func(p string) string { return fmt.Sprintf("values layer %d: %s", i+1, p) }
		if err := chart.NoTemplate(layer, at); err != nil {
			return nil, err
		}
	}
	return append(platform, instance...), nil
}

// normalized: layers, Go values of any type, as values (value.Normalize),
// in a copy: the caller's are never changed. One that values cannot hold
// fails, named <name> <n>, n from 1, with the path of what fails.
func normalized(name string, layers []map[string]any) ([]map[string]any, error) {
	out := make([]map[string]any, len(layers))
	for i, l := range layers {
		v, err := value.Normalize(l)
		if err != nil {
			return nil, fmt.Errorf("%s %d: %w", name, i+1, err)
		}
		out[i] = value.AsMap(v)
	}
	return out, nil
}
