// Checks of values the compile applies itself: no template code, and no
// level that changes a lower one's.

package chart

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/okdp/okdp-compiler/value"
)

// UpstreamCheck: what a caller refuses of the instance's upstream values
// of a chart, user, whatever the package's protect paths: the platform's
// policy. values: the computed values they are applied over (OKDP's
// defaults and the mapping's), which it does not change; at names a path of
// user in the messages (upstream.<key>.<path>, Key and Index paths). Its
// error fails the compile.
type UpstreamCheck func(user, values M, at func(p string) string) error

// NoTemplate: no key or string of v, at any depth, contains "{{". Values
// reach a chart as data, and some charts pass them to tpl, where template
// code could call lookup and read the cluster's Secrets. The first one
// found, in path order (keys sorted, items in order), fails, at(p) naming
// where: p, its path in v (value.Key, value.Index). A value of a type
// values do not hold (package value) fails too: it could not be checked.
func NoTemplate(v any, at func(p string) string) error {
	return noTemplate(v, "", at)
}

// noTemplate: NoTemplate on v, found at the path p.
func noTemplate(v any, p string, at func(string) string) error {
	switch t := v.(type) {
	case nil, bool, float64:
	case string:
		if strings.Contains(t, "{{") {
			return fmt.Errorf("%s: the value contains \"{{\"", at(p))
		}
	case M:
		for _, k := range slices.Sorted(maps.Keys(t)) {
			kp := value.Key(p, k)
			if strings.Contains(k, "{{") {
				return fmt.Errorf("%s: the key contains \"{{\"", at(kp))
			}
			if err := noTemplate(t[k], kp, at); err != nil {
				return err
			}
		}
	case []any:
		for i, x := range t {
			if err := noTemplate(x, value.Index(p, i), at); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("%s: cannot be checked for \"{{\": %T is not a values type", at(p), v)
	}
	return nil
}

// Overlap: the first path, in path order (keys sorted), at which layer sets
// a value base sets, "" when there is none: a key of both whose values are
// not both maps (a null, a list, a map over a scalar), or whose path whole
// names, a map then taken as one value. The path is written as NoTemplate
// writes it (FormatPath).
func Overlap(base, layer M, whole func(keys []string) bool) string {
	return overlap(base, layer, nil, whole)
}

// overlap: Overlap below the keys at.
func overlap(base, layer M, at []string, whole func([]string) bool) string {
	for _, k := range slices.Sorted(maps.Keys(layer)) {
		b, ok := base[k]
		if !ok {
			continue
		}
		p := append(slices.Clip(at), k)
		bm, bok := b.(M)
		lm, lok := layer[k].(M)
		if !bok || !lok || whole(p) {
			return FormatPath(p)
		}
		if s := overlap(bm, lm, p, whole); s != "" {
			return s
		}
	}
	return ""
}
