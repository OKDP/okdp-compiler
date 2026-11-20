// The functions the documents of a rule set call that the language lacks:
// a recursive walk of a value, a split on several characters, and the
// parse of a URL.

package rules

import (
	"maps"
	"net/url"
	"slices"
	"strings"
)

// leaves returns every scalar under v with its path, as a list of
// {path, value}: depth first, map keys sorted, list items in order, a
// list index an int of the path. Null is a scalar; an empty map or list
// gives nothing. v itself, a scalar, has the path [].
func leaves(v any) []any {
	out := []any{}
	var walk func(v any, path []any)
	walk = func(v any, path []any) {
		switch t := v.(type) {
		case map[string]any:
			for _, k := range slices.Sorted(maps.Keys(t)) {
				walk(t[k], append(slices.Clip(path), k))
			}
		case []any:
			for i, x := range t {
				walk(x, append(slices.Clip(path), i))
			}
		default:
			out = append(out, map[string]any{"path": slices.Clone(path), "value": v})
		}
	}
	walk(v, []any{})
	return out
}

// fields splits s on any character of seps and drops the empty fields.
func fields(s, seps string) []any {
	out := []any{}
	for _, f := range strings.FieldsFunc(s, func(c rune) bool { return strings.ContainsRune(seps, c) }) {
		out = append(out, f)
	}
	return out
}

// urlParse returns what url.Parse reads of s: ok, false when it fails,
// with every other field empty; the scheme; the host without its port
// (Hostname); and whether user information is given.
func urlParse(s string) map[string]any {
	u, err := url.Parse(s)
	if err != nil {
		return map[string]any{"ok": false, "scheme": "", "host": "", "user": false}
	}
	return map[string]any{"ok": true, "scheme": u.Scheme, "host": u.Hostname(), "user": u.User != nil}
}
