// Layers: the values layers of a compile, the platform's (Inputs.Platform),
// the project's (Inputs.Project), then the instance's (Inputs.Values), Go
// values of any type read as Helm values (value.Normalize). global is the
// platform's: global.okdp (the instance's host, where its OAuth clients
// register, the chart its objects are rendered with) and what every chart
// receives as global (chart.FinalValues). A project adds to the platform's
// values, it changes none of them: no value the platform sets, no connection
// it gives. An instance sets only the keys of global its package's parameter
// schema declares, never okdp. Neither gives template code: the platform's
// layers and the package's schema defaults are reviewed, the project's and
// the instance's are input.

package compiler

import (
	"fmt"
	"maps"
	"slices"

	"github.com/okdp/okdp-compiler/internal/chart"
	"github.com/okdp/okdp-compiler/internal/value"
)

// valuesLayers: the layers of in, the platform's, the project's, then the
// instance's, as values (normalized), for chart.Parameters. A project layer
// that sets a value the platform's layers set fails (projectLayers); an
// instance layer that sets global fails, but for the keys the parameter
// schema (chart.Schema.Doc) declares under it
// (properties.global.properties), okdp excepted; then a project or instance
// layer with "{{" in a key or a string, anywhere (chart.NoTemplate).
func valuesLayers(schema M, in Inputs) ([]map[string]any, error) {
	platform, err := normalized("platform layer", in.Platform)
	if err != nil {
		return nil, err
	}
	project, err := normalized("project layer", in.Project)
	if err != nil {
		return nil, err
	}
	instance, err := normalized("values layer", in.Values)
	if err != nil {
		return nil, err
	}
	if err := projectLayers(platform, project); err != nil {
		return nil, err
	}
	declared := value.AsMap(value.Dig(schema, "properties.global.properties"))
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
	return slices.Concat(platform, project, instance), nil
}

// projectLayers: the project's layers add to the platform's, merged in
// order, and change none of their values: a project layer fails at the
// first path, in path order, at which it sets a value the platform's set,
// to any value, the same one included: a key whose value is not a map, a
// connection (connections.<name>, taken whole), or a parent of either set
// to anything but a map (chart.Overlap). A project layer may change what an
// earlier one sets. Then no template code, as in an instance layer
// (chart.NoTemplate): a project is not reviewed with the platform.
func projectLayers(platform, project []map[string]any) error {
	if len(project) == 0 {
		return nil
	}
	set := M{}
	for _, layer := range platform {
		set = value.Merge(set, layer)
	}
	connection := func(keys []string) bool { return len(keys) == 2 && keys[0] == "connections" }
	for i, layer := range project {
		if p := chart.Overlap(set, layer, connection); p != "" {
			return fmt.Errorf("project layer %d: %s is set by the platform (--platform, Inputs.Platform): a project adds values, it does not change the platform's", i+1, p)
		}
		at := func(p string) string { return fmt.Sprintf("project layer %d: %s", i+1, p) }
		if err := chart.NoTemplate(layer, at); err != nil {
			return err
		}
	}
	return nil
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
