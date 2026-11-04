// Layers: the values layers of a compile, the context's (Inputs.Context), by
// level, then the instance's (Inputs.Values), Go values of any type read as
// Helm values (value.Normalize). global is the context's: global.okdp (the
// instance's host, where its OAuth clients register, the chart its objects
// are rendered with, the platform's policy) and what every chart receives
// as global (chart.FinalValues). Level 0 is the platform's; each level
// above adds to those below it, it changes none of their values: no value
// they set, no connection they give, no claim they declare, no class they
// define, nor the default class they set; and of global.okdp, the
// platform's settings, it sets provisioning only, its classes: every other
// key of global.okdp, known to the compiler or not, is level 0's. An
// instance sets only the keys of global its package's parameter schema
// declares, never okdp, no connections, its own being a context layer of
// its level, and no claims, its project's (level 1 or above, never the
// platform's). None but level 0 gives template code: the platform's layers
// and the package's schema defaults are reviewed, the higher levels' and
// the instance's are input.

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
// instance's, as values (normalized), for chart.Parameters, and the
// context's global.okdp (contextLayers). Each instance
// layer in turn: one that sets global fails, but for the keys the parameter
// schema (chart.Schema.Doc) declares under it (properties.global.properties),
// okdp excepted; then one that sets connections, then claims, then
// access, to any value; then one with "{{" in a key or a string, anywhere
// (chart.NoTemplate).
func valuesLayers(schema M, in Inputs) ([]map[string]any, M, error) {
	layers, platform, err := contextLayers(in.Context)
	if err != nil {
		return nil, nil, err
	}
	declared := value.AsMap(value.Dig(schema, "properties.global.properties"))
	for i, l := range in.Values {
		name := fmt.Sprintf("values layer %d", i+1)
		v, err := value.Normalize(l)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", name, err)
		}
		layer := value.AsMap(v)
		g, isMap := layer["global"].(M)
		if _, set := layer["global"]; set && !isMap {
			return nil, nil, fmt.Errorf("%s: global is set by the context (Inputs.Context)", name)
		}
		for _, k := range slices.Sorted(maps.Keys(g)) {
			if _, ok := declared[k]; !ok || k == "okdp" {
				return nil, nil, fmt.Errorf("%s: global.%s is set by the context (Inputs.Context)", name, k)
			}
		}
		if _, set := layer["connections"]; set {
			return nil, nil, fmt.Errorf("%s: connections are given by the context (Inputs.Context): an instance's own in a context layer of its level", name)
		}
		if _, set := layer["claims"]; set {
			return nil, nil, fmt.Errorf("%s: claims are declared by the context (Inputs.Context): a project's, of level 1 or above", name)
		}
		if _, set := layer[AccessKey]; set {
			return nil, nil, fmt.Errorf("%s: %s is the project's, its file's (the context at level 1)", name, AccessKey)
		}
		// No template code in what the instance gives, whatever key it is
		// under: parameters, upstream values (chart.NoTemplate).
		if err := chart.NoTemplate(layer, func(p string) string { return name + ": " + p }); err != nil {
			return nil, nil, err
		}
		layers = append(layers, layer)
	}
	return layers, platform, nil
}

// contextLayers: the context's layers as values (normalized), in a copy,
// by level, lowest first, a level's in their order: named context layer
// <n> (level <l>), n their position in layers, from 1. A layer may change
// what a layer of its level sets; it adds to the lower levels' values and
// changes none of them: it fails at the first lower level, then the first
// path (in path order), at which it sets a value that level sets, to any
// value, the same one included: a key whose value is not a map, a
// connection, a named claim, a class or the default class of a contract
// (connections.<name>, claims.<name>,
// global.okdp.provisioning.<contract>.classes.<name>,
// global.okdp.provisioning.<contract>.default, taken whole), or a parent of
// either set to anything but a map (chart.Overlap). Above level 0, a layer
// sets nothing of global.okdp but provisioning (platformOnly): the other
// keys, those the compiler reads (charts, oidc, policy...) and the others,
// are the platform's, level 0's, whether level 0 sets them or not.
// Level 0 declares no claims: they are a project's. access, the roles of
// a project's groups, is level 1's only, and left out of the layers: it
// is read apart (projectAccess), no instance receives it. Above level 0,
// no template code (chart.NoTemplate): only the platform's level is
// reviewed.
//
// And the context's global.okdp, {global: {okdp: ...}} ({} when no layer
// sets it), built from these layers alone, never from the parameters (a
// package schema's defaults do not reach it): level 0's global.okdp, its
// layers merged in order, then global.okdp.provisioning of each layer
// above, merged in the same order; no other key of a higher level is read,
// whatever the checks above let through.
func contextLayers(layers []ContextLayer) ([]map[string]any, M, error) {
	order := make([]int, len(layers))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int { return cmp.Compare(layers[a].Level, layers[b].Level) })
	whole := func(keys []string) bool {
		class := len(keys) == 6 && keys[4] == "classes" || len(keys) == 5 && keys[4] == "default"
		return len(keys) == 2 && (keys[0] == "connections" || keys[0] == "claims") ||
			class && keys[0] == "global" && keys[1] == "okdp" && keys[2] == "provisioning"
	}
	var out []map[string]any
	platform := M{}           // the context's global.okdp, under global
	var levels []ContextLayer // the values each level set so far, merged, lowest first
	for _, i := range order {
		l := layers[i]
		name := fmt.Sprintf("context layer %d (level %d)", i+1, l.Level)
		v, err := value.Normalize(l.Values)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", name, err)
		}
		layer := value.AsMap(v)
		if n := len(levels); n == 0 || levels[n-1].Level != l.Level {
			levels = append(levels, ContextLayer{Level: l.Level, Values: M{}})
		}
		for _, lower := range levels[:len(levels)-1] {
			if p := chart.Overlap(lower.Values, layer, whole); p != "" {
				return nil, nil, fmt.Errorf("%s: %s is set at level %d (Inputs.Context): a level adds values, it does not change a lower level's", name, p, lower.Level)
			}
		}
		if l.Level > 0 {
			if k := platformOnly(layer); k != "" {
				return nil, nil, fmt.Errorf("%s: %s: a platform setting, level 0 only (Inputs.Context): a project sets global.okdp.provisioning, nothing else of global.okdp", name, k)
			}
		}
		if _, set := layer["claims"]; set && l.Level == 0 {
			return nil, nil, fmt.Errorf("%s: claims: a project's, of level 1 or above", name)
		}
		// The roles of the project's groups are its file's, level 1's, read
		// apart (projectAccess): no value of the instances.
		if _, set := layer[AccessKey]; set && l.Level != 1 {
			return nil, nil, fmt.Errorf("%s: %s: the roles of a project's groups, its file's, of level 1 only", name, AccessKey)
		}
		delete(layer, AccessKey)
		if l.Level > 0 {
			if err := chart.NoTemplate(layer, func(p string) string { return name + ": " + p }); err != nil {
				return nil, nil, err
			}
		}
		levels[len(levels)-1].Values = value.Merge(levels[len(levels)-1].Values, layer)
		out = append(out, layer)
		okdp, set := value.AsMap(layer["global"])["okdp"]
		switch {
		case set && l.Level == 0:
			platform = value.Merge(platform, M{"global": M{"okdp": okdp}})
		case set:
			if p, set := value.AsMap(okdp)["provisioning"]; set {
				platform = value.Merge(platform, M{"global": M{"okdp": M{"provisioning": p}}})
			}
		}
	}
	return out, platform, nil
}

// platformOnly: the first path of layer, a context layer above level 0,
// that sets a platform setting, in path order: global.okdp itself, to
// anything but a map, or a key of it but provisioning, the classes a
// project adds; "" when it sets none. The settings of global.okdp are the
// platform's, level 0's, those the compiler reads (charts, the chart the
// OAuth clients are rendered with; oidc, where they register; policy, the
// privileged projects; secrets, the store) and those it does not, which
// the mappings read: a project adds none, whether the platform sets them
// or not.
func platformOnly(layer M) string {
	okdp, set := value.AsMap(layer["global"])["okdp"]
	if !set {
		return ""
	}
	m, isMap := okdp.(M)
	if !isMap {
		return "global.okdp"
	}
	for _, k := range slices.Sorted(maps.Keys(m)) {
		if k != "provisioning" {
			return value.Key("global.okdp", k)
		}
	}
	return ""
}
