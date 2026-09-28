// Values: reading YAML and JSON as Helm values (string keys, every number a
// float64), and Helm's merge rules.
package compiler

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"

	"gopkg.in/yaml.v3"
)

// M: a values map (YAML or JSON object).
type M = map[string]any

// readYAML: a YAML file of fsys, normalized.
func readYAML(fsys fs.FS, name string) (any, error) {
	b, err := fs.ReadFile(fsys, name)
	if err != nil {
		return nil, err
	}
	var v any
	if err := yaml.Unmarshal(b, &v); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return normalize(v), nil
}

// ParseValues: a values file as the compiler reads it (string keys, every
// number a float64), for Inputs.Values.
func ParseValues(b []byte) (map[string]any, error) {
	var v any
	if err := yaml.Unmarshal(b, &v); err != nil {
		return nil, err
	}
	return asMap(normalize(v)), nil
}

// normalize: maps with string keys, every number a float64 (as Helm values,
// which go through JSON).
func normalize(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := M{}
		for k, x := range t {
			out[k] = normalize(x)
		}
		return out
	case map[any]any:
		out := M{}
		for k, x := range t {
			out[fmt.Sprint(k)] = normalize(x)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = normalize(x)
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

// deepCopy: a copy of v that shares no map or list with it.
func deepCopy(v any) any {
	switch t := v.(type) {
	case M:
		out := M{}
		for k, x := range t {
			out[k] = deepCopy(x)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = deepCopy(x)
		}
		return out
	}
	return v
}

// asMap: v as a map, or an empty map.
func asMap(v any) M {
	if m, ok := v.(M); ok {
		return m
	}
	return M{}
}

// coalesce: Helm's merge of user values over chart defaults (null deletes).
func coalesce(dst, src M) M {
	for k, sv := range src {
		if sv == nil {
			delete(dst, k)
			continue
		}
		if sm, ok := sv.(M); ok {
			if dm, ok := dst[k].(M); ok {
				dst[k] = coalesce(dm, sm)
				continue
			}
		}
		dst[k] = deepCopy(sv)
	}
	return dst
}

// mergeOverwrite: sprig's mergeOverwrite on Helm values (maps merge, lists
// and scalars replace, null sets null).
func mergeOverwrite(dst, src M) M {
	for k, sv := range src {
		if sm, ok := sv.(M); ok {
			if dm, ok := dst[k].(M); ok {
				dst[k] = mergeOverwrite(dm, sm)
				continue
			}
		}
		dst[k] = deepCopy(sv)
	}
	return dst
}

// truthy: v as a Helm condition sees it (empty is false).
func truthy(v any) bool {
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

// str: v as text, as Go templates print it.
func str(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	}
	return fmt.Sprint(v) // Go %v: float64(3) -> "3", 0.5 -> "0.5", true -> "true"
}

// dig: the value at a dotted path of m, or nil.
func dig(m M, path string) any {
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

// readJSON: a JSON file of fsys, as a map.
func readJSON(fsys fs.FS, name string) (M, error) {
	b, err := fs.ReadFile(fsys, name)
	if err != nil {
		return nil, err
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return asMap(v), nil
}
