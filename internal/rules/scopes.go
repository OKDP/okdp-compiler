// The scope documents of a rule set: together, what a mapping sees as
// platform.

package rules

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/okdp/okdp-compiler/value"
)

// loadScopes reads the scope documents of a set, documents of the sets
// read so far. A document is listed once, by one set.
func (r *RuleSet) loadScopes(doc M) error {
	for _, v := range value.AsList(doc["scopes"]) {
		name := value.Str(v)
		if _, ok := r.sources[name]; !ok {
			return fmt.Errorf("%s: scopes: %s is none of documents", RulesFile, name)
		}
		if slices.Contains(r.scopes, name) {
			return r.taken("scopes", name)
		}
		r.scopes = append(r.scopes, name)
	}
	return nil
}

// Scope evaluates the scope documents of the rule set, in the order their
// sets were read, each on input, and returns their values.platform merged:
// maps key by key. A value that two documents give at one path, a map
// that is not under both, is an error naming both.
func (r *RuleSet) Scope(ctx context.Context, input M) (M, error) {
	if !r.Loaded() {
		return nil, fmt.Errorf("the rule set: %w", ErrNotLoaded)
	}
	platform := M{}
	given := make([]M, 0, len(r.scopes))
	for _, name := range r.scopes {
		v, err := r.Values(ctx, name, input)
		if err != nil {
			return nil, err
		}
		p := value.AsMap(v["platform"])
		if at := mergeScope(platform, p, nil); at != nil {
			first := slices.IndexFunc(given, func(g M) bool { return holds(g, at) })
			return nil, fmt.Errorf("the rule set: scopes: platform.%s is given by both %s and %s", strings.Join(at, "."), r.scopes[first], name)
		}
		given = append(given, p)
	}
	return platform, nil
}

// mergeScope merges src into dst, the maps at path, key by key. It
// returns the path of the first key both give a value at that is not a map
// in both, nil when src is merged.
func mergeScope(dst, src M, path []string) []string {
	for _, k := range slices.Sorted(maps.Keys(src)) {
		at := append(slices.Clip(path), k)
		d, set := dst[k]
		if !set {
			dst[k] = value.Copy(src[k])
			continue
		}
		dm, dMap := d.(M)
		sm, sMap := src[k].(M)
		if !dMap || !sMap {
			return at
		}
		if conflict := mergeScope(dm, sm, at); conflict != nil {
			return conflict
		}
	}
	return nil
}

// holds reports whether m gives a value at path.
func holds(m M, path []string) bool {
	for i, k := range path {
		v, set := m[k]
		if !set {
			return false
		}
		if i < len(path)-1 {
			if m, set = v.(M); !set {
				return false
			}
		}
	}
	return true
}
