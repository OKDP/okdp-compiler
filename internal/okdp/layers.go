// Layers: the values layers of a compile, the platform's (Inputs.Platform)
// then the instance's (Inputs.Values). global is the platform's: global.okdp
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

// valuesLayers: the layers of in, the platform's then the instance's, for
// chart.Parameters. An instance layer that sets global fails, but for the
// keys values.schema.json of files declares under it
// (properties.global.properties), okdp excepted; then one with "{{" in a
// key or a string, anywhere (chart.NoTemplate), each layer normalized
// first, as chart.Parameters merges it.
func valuesLayers(files fs.FS, in Inputs) ([]map[string]any, error) {
	var declared M // properties.global.properties, read once, when needed
	for i, layer := range in.Values {
		g, ok := layer["global"]
		if !ok {
			continue
		}
		keys, isMap := value.Normalize(g).(M)
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
	for i, layer := range in.Values {
		at := func(p string) string { return fmt.Sprintf("values layer %d: %s", i+1, p) }
		if err := chart.NoTemplate(value.Normalize(layer), at); err != nil {
			return nil, err
		}
	}
	return append(slices.Clip(in.Platform), in.Values...), nil
}
