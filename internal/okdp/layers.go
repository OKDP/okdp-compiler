// Layers: the values layers of a compile, the context's (Inputs.Context), by
// level, then the instance's (Inputs.Values), Go values of any type read as
// Helm values (value.Normalize). global is the context's: global.okdp (the
// instance's host, where its OAuth clients register, the chart its objects
// are rendered with) and what every chart receives as global
// (chart.FinalValues). Level 0 is the platform's; each level above adds to
// those below it, it changes none of their values: no value they set, no
// connection they give. An instance sets only the keys of global its
// package's parameter schema declares, never okdp, and no connections: its
// own are a context layer of its level. None but level 0 gives template
// code: the platform's layers and the package's schema defaults are
// reviewed, the higher levels' and the instance's are input.

package okdp

import (
	"cmp"
	"fmt"
	"maps"
	"slices"

	"github.com/okdp/okdp-compiler/internal/chart"
	"github.com/okdp/okdp-compiler/internal/value"
)

// valuesLayers: the layers of in, the context's (contextLayers), then the
// instance's, as values (normalized), for chart.Parameters. Each instance
// layer in turn: one that sets global fails, but for the keys the parameter
// schema (chart.Schema.Doc) declares under it (properties.global.properties),
// okdp excepted; then one that sets connections, to any value; then one
// with "{{" in a key or a string, anywhere (chart.NoTemplate).
func valuesLayers(schema M, in Inputs) ([]map[string]any, error) {
	layers, err := contextLayers(in.Context)
	if err != nil {
		return nil, err
	}
	declared := value.AsMap(value.Dig(schema, "properties.global.properties"))
	for i, l := range in.Values {
		name := fmt.Sprintf("values layer %d", i+1)
		v, err := value.Normalize(l)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		layer := value.AsMap(v)
		g, isMap := layer["global"].(M)
		if _, set := layer["global"]; set && !isMap {
			return nil, fmt.Errorf("%s: global is set by the context (--context, Inputs.Context)", name)
		}
		for _, k := range slices.Sorted(maps.Keys(g)) {
			if _, ok := declared[k]; !ok || k == "okdp" {
				return nil, fmt.Errorf("%s: global.%s is set by the context (--context, Inputs.Context)", name, k)
			}
		}
		if _, set := layer["connections"]; set {
			return nil, fmt.Errorf("%s: connections are given by the context (--context, Inputs.Context): an instance's own in a context layer of its level", name)
		}
		// No template code in what the instance gives, whatever key it is
		// under: parameters, upstream values (chart.NoTemplate).
		if err := chart.NoTemplate(layer, func(p string) string { return name + ": " + p }); err != nil {
			return nil, err
		}
		layers = append(layers, layer)
	}
	return layers, nil
}

// contextLayers: the context's layers as values (normalized), in a copy,
// by level, lowest first, a level's in their order: named context layer
// <n> (level <l>), n their position in layers, from 1. A layer may change
// what a layer of its level sets; it adds to the lower levels' values and
// changes none of them: it fails at the first lower level, then the first
// path (in path order), at which it sets a value that level sets, to any
// value, the same one included: a key whose value is not a map, a
// connection (connections.<name>, taken whole), or a parent of either set
// to anything but a map (chart.Overlap). Above level 0, no template code
// (chart.NoTemplate): only the platform's level is reviewed.
func contextLayers(layers []ContextLayer) ([]map[string]any, error) {
	order := make([]int, len(layers))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int { return cmp.Compare(layers[a].Level, layers[b].Level) })
	connection := func(keys []string) bool { return len(keys) == 2 && keys[0] == "connections" }
	var out []map[string]any
	var levels []ContextLayer // the values each level set so far, merged, lowest first
	for _, i := range order {
		l := layers[i]
		name := fmt.Sprintf("context layer %d (level %d)", i+1, l.Level)
		v, err := value.Normalize(l.Values)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		layer := value.AsMap(v)
		if n := len(levels); n == 0 || levels[n-1].Level != l.Level {
			levels = append(levels, ContextLayer{Level: l.Level, Values: M{}})
		}
		for _, lower := range levels[:len(levels)-1] {
			if p := chart.Overlap(lower.Values, layer, connection); p != "" {
				return nil, fmt.Errorf("%s: %s is set at level %d (--context, Inputs.Context): a level adds values, it does not change a lower level's", name, p, lower.Level)
			}
		}
		if l.Level > 0 {
			if err := chart.NoTemplate(layer, func(p string) string { return name + ": " + p }); err != nil {
				return nil, err
			}
		}
		levels[len(levels)-1].Values = value.Merge(levels[len(levels)-1].Values, layer)
		out = append(out, layer)
	}
	return out, nil
}
