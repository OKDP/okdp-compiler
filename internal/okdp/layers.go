// Layers: the values layers of a compile. The context's layers
// (Inputs.Context) come first, by level, then the instance's
// (Inputs.Values).
//
// Level 0 is the platform's. Each level above adds to the levels below
// and changes nothing they set: no value, no connection, no claim, no
// class, no default class. Of global.okdp, the platform's settings, a
// level above 0 sets provisioning only (its classes). Every other key
// belongs to level 0, whether the compiler knows it or not.
//
// An instance sets only the keys of global that its package's parameter
// schema declares, and never okdp. It sets no connections (its own are a
// context layer of its level), no claims (they are its project's) and no
// access.
//
// Only level 0 may give template code: the platform's layers and the
// package's schema defaults are reviewed, the higher levels and the
// instance are input.

package okdp

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"github.com/okdp/okdp-compiler/chart"
	ruleset "github.com/okdp/okdp-compiler/internal/rules"
	"github.com/okdp/okdp-compiler/value"
)

// valuesLayers returns the normalized layers of in for chart.Parameters:
// the context's, then the instance's, and the context's global.okdp.
// schema is the document of the parameter schema. It refuses an instance
// layer that breaks the rules above.
func valuesLayers(ctx context.Context, schema M, in Inputs) ([]map[string]any, M, error) {
	layers, platform, err := contextLayers(ctx, in.rules(), in.Context)
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
		if err := in.rules().Refuse(ctx, ruleset.CheckValuesLayer, M{"name": name, "layer": layer, "declared": declared}); err != nil {
			return nil, nil, err
		}
		// The instance gives no template code, under any key: parameters or
		// upstream values.
		if err := chart.NoTemplate(layer, func(p string) string { return name + ": " + p }); err != nil {
			return nil, nil, err
		}
		layers = append(layers, layer)
	}
	return layers, platform, nil
}

// contextLayers returns the context's layers as normalized copies,
// lowest level first, and within a level in their given order. It
// applies the rules above. A layer may change what another layer of its
// level sets, but fails when it sets something a lower level sets, even
// to the same value. A connection, a named claim, a class and the
// default class of a contract are each taken whole (the rule set's
// Whole); otherwise what counts as set is a key whose value is not a
// map.
//
// access is left out of the returned layers. projectAccess reads it
// apart, and no instance receives it. A layer sets it at level 1 only,
// and a layer of level 0 declares no claims.
//
// contextLayers also returns the context's global.okdp, as {global:
// {okdp: ...}}, or {} when no layer sets it. It builds it from these
// layers alone, never from the parameters, so the defaults of a package
// schema do not reach it. Of a layer above level 0 it reads
// global.okdp.provisioning only, whatever the checks let through.
func contextLayers(ctx context.Context, rules *RuleSet, layers []ContextLayer) ([]map[string]any, M, error) {
	order := levelOrder(layers)
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
			if p := chart.Overlap(lower.Values, layer, rules.Whole); p != "" {
				return nil, nil, fmt.Errorf("%s: %s is set at level %d (Inputs.Context): a level adds values, it does not change a lower level's", name, p, lower.Level)
			}
		}
		if err := rules.Refuse(ctx, ruleset.CheckContextLayer, M{"name": name, "level": int64(l.Level), "layer": layer, "platform": platform}); err != nil {
			return nil, nil, err
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
			for _, k := range rules.List(ruleset.TablePlatformKeysAbove) {
				if p, set := value.AsMap(okdp)[k]; set {
					platform = value.Merge(platform, M{"global": M{"okdp": M{k: p}}})
				}
			}
		}
	}
	return out, platform, nil
}

// levelOrder returns the positions of layers sorted by level, lowest
// first, and in their given order within a level.
func levelOrder(layers []ContextLayer) []int {
	order := make([]int, len(layers))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int { return cmp.Compare(layers[a].Level, layers[b].Level) })
	return order
}

// upstreamGlobals gives the host's policy (Inputs.CheckGlobals) the
// global of every layer above level 0: the keys of global that a project
// or the instance sets, which every chart receives. layers is the result
// of valuesLayers for in.
func upstreamGlobals(ctx context.Context, in Inputs, layers []map[string]any, okdp M) error {
	if in.CheckGlobals == nil {
		return nil
	}
	order := levelOrder(in.Context)
	var named []GlobalLayer
	for i, layer := range layers {
		name := fmt.Sprintf("values layer %d", i-len(in.Context)+1)
		if i < len(order) {
			l := in.Context[order[i]]
			if l.Level == 0 {
				continue
			}
			name = fmt.Sprintf("context layer %d (level %d)", order[i]+1, l.Level)
		}
		if g := value.AsMap(layer["global"]); len(g) > 0 {
			named = append(named, GlobalLayer{Name: name, Global: g})
		}
	}
	return in.CheckGlobals(ctx, GlobalsInput{Rules: in.rules(), Settings: okdp, Namespace: in.Namespace, Privileged: privileged(okdp, in.Namespace), Layers: named})
}
