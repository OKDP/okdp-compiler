// Package value is the data every stage shares: YAML and JSON values as
// Helm values are after their JSON round trip (maps with string keys, []any
// lists, float64 numbers, strings, booleans, null), and the helpers that
// read them. A value computed by CEL (compile) and the same value read from
// a file (chart) are equal.
package value

import (
	"fmt"
	"strings"
)

// M: a values map (YAML or JSON object).
type M = map[string]any

// Normalize: maps with string keys, every number a float64 (as Helm values,
// which go through JSON), for values a caller built in memory.
func Normalize(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := M{}
		for k, x := range t {
			out[k] = Normalize(x)
		}
		return out
	case map[any]any:
		out := M{}
		for k, x := range t {
			out[fmt.Sprint(k)] = Normalize(x)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = Normalize(x)
		}
		return out
	case int:
		return float64(t)
	case int64:
		return float64(t)
	case uint64:
		return float64(t)
	case float32:
		return float64(t)
	}
	return v
}

// Copy: a copy of v that shares no map or list with it.
func Copy(v any) any {
	switch t := v.(type) {
	case M:
		out := M{}
		for k, x := range t {
			out[k] = Copy(x)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = Copy(x)
		}
		return out
	}
	return v
}

// AsMap: v as a map, or an empty map.
func AsMap(v any) M {
	if m, ok := v.(M); ok {
		return m
	}
	return M{}
}

// AsList: v as a list, or nil.
func AsList(v any) []any {
	if l, ok := v.([]any); ok {
		return l
	}
	return nil
}

// Merge: Helm's MergeMaps, the merge of its -f files (and sprig's
// mergeOverwrite on values): maps merge, lists and scalars replace, a null
// replaces too and stays null. dst is changed in place and returned; src is
// copied.
func Merge(dst, src M) M {
	for k, sv := range src {
		if sm, ok := sv.(M); ok {
			if dm, ok := dst[k].(M); ok {
				dst[k] = Merge(dm, sm)
				continue
			}
		}
		dst[k] = Copy(sv)
	}
	return dst
}

// Truthy: v as a Helm condition sees it (empty is false).
func Truthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case string:
		return t != ""
	case float64:
		return t != 0
	case M:
		return len(t) > 0
	case []any:
		return len(t) > 0
	}
	return true
}

// Str: v as text, as Go templates print it.
func Str(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	}
	return fmt.Sprint(v) // Go %v: float64(3) -> "3", 0.5 -> "0.5", true -> "true"
}

// Dig: the value at a dotted path of m, or nil.
func Dig(m M, path string) any {
	var cur any = m
	for seg := range strings.SplitSeq(path, ".") {
		mm, ok := cur.(M)
		if !ok {
			return nil
		}
		cur = mm[seg]
	}
	return cur
}

// Short: a value for a message, cut at 160 characters.
func Short(v any) string {
	s := fmt.Sprintf("%#v", v)
	if len(s) > 160 {
		s = s[:160] + "..."
	}
	return s
}
