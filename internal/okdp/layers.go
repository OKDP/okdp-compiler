// Layers: the values layers of a compile (Inputs.Values), merged in order
// as helm -f merges files. Any layer may set any key: parameters, upstream
// values, global, global.okdp (the settings), connections and claims. The
// compiler does not know where a layer comes from: a host that gives
// layers of several origins decides what each may set.
//
// A host gives settings of its own beside the layers (Inputs.Settings):
// they join the settings the compile reads, never the values.

package okdp

import (
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/okdp/okdp-compiler/value"
)

// layered is what valuesLayers reads of the values layers and the host's
// settings.
type layered struct {
	// layers are the layers, normalized copies, for chart.Parameters.
	layers []map[string]any
	// written is global.okdp of the layers, as {global: {okdp: ...}}, or {}
	// when no layer sets it: the settings the values hold.
	written M
	// platform is written with Inputs.Settings: the settings the compile
	// reads, as {global: {okdp: ...}}, or {} when neither gives any.
	platform M
}

// valuesLayers reads the layers of in and the settings of in: global.okdp
// of the layers alone, never of the parameters, so the defaults of a
// package schema do not reach it, then Inputs.Settings (hostSettings).
func valuesLayers(in Inputs) (layered, error) {
	var out layered
	out.written = M{}
	for i, l := range in.Values {
		v, err := value.Normalize(l)
		if err != nil {
			return layered{}, fmt.Errorf("values layer %d: %w", i+1, err)
		}
		layer := value.AsMap(v)
		out.layers = append(out.layers, layer)
		if okdp, set := value.AsMap(layer["global"])["okdp"]; set {
			out.written = value.Merge(out.written, M{"global": M{"okdp": okdp}})
		}
	}
	platform, err := hostSettings(out.written, in.Settings)
	if err != nil {
		return layered{}, err
	}
	out.platform = platform
	return out, nil
}

// hostSettings returns written, global.okdp of the layers as {global:
// {okdp: ...}}, with the host's settings, the content of global.okdp. A
// value that both give is an error, each such path reported (bothGiven):
// neither overrides the other, so that no values file changes what the
// host gives, nor the host what a values file says.
func hostSettings(written M, host map[string]any) (M, error) {
	if len(host) == 0 {
		return written, nil
	}
	v, err := value.Normalize(host)
	if err != nil {
		return nil, fmt.Errorf("Inputs.Settings: %w", err)
	}
	h := value.AsMap(v)
	if okdp, set := value.AsMap(written["global"])["okdp"]; set {
		if errs := bothGiven("global.okdp", okdp, h); errs != nil {
			return nil, errors.Join(errs...)
		}
	}
	return value.Merge(value.AsMap(value.Copy(written)), M{"global": M{"okdp": h}}), nil
}

// bothGiven returns an error for each key of h, the host's settings at
// the path at, that w, the values' at the same path, gives too, in key
// order. The host gives a value whole, but a branch: a map that holds
// maps alone, whose keys are compared in turn. So a layer may set a key
// beside the host's, never one of the host's nor a key in a map that
// holds the host's values: a group beside the host's groups.
func bothGiven(at string, w any, h M) []error {
	wm, ok := w.(M)
	if !ok {
		return []error{givenTwice(at)}
	}
	var errs []error
	for _, k := range slices.Sorted(maps.Keys(h)) {
		wv, set := wm[k]
		if !set {
			continue
		}
		if hm, ok := h[k].(M); ok && branch(hm) {
			errs = append(errs, bothGiven(value.Key(at, k), wv, hm)...)
		} else {
			errs = append(errs, givenTwice(value.Key(at, k)))
		}
	}
	return errs
}

// branch reports whether m holds keys, and maps alone.
func branch(m M) bool {
	if len(m) == 0 {
		return false
	}
	for _, v := range m {
		if _, ok := v.(M); !ok {
			return false
		}
	}
	return true
}

func givenTwice(at string) error {
	return fmt.Errorf("%s: set both by the values (Inputs.Values) and by the host's settings (Inputs.Settings), which never override each other", at)
}
