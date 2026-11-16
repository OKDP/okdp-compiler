// Package value is the data every stage shares: YAML and JSON values as
// Helm values are after their JSON round trip (maps with string keys, []any
// lists, float64 numbers, strings, booleans, null), and the helpers that
// read them. A value computed by CEL (compile) and the same value read from
// a file (chart) are equal.
package value

import (
	"fmt"
	"strings"

	celvalue "github.com/okdp/cel2yaml/value"
)

// M: a values map (YAML or JSON object).
type M = celvalue.M

// Normalize: v, a value a caller built in memory, as Helm values are after
// their JSON round trip, in a copy (cel2yaml/value.Normalize): written by
// encoding/json/v2, map keys sorted, and read back. At any depth a map is
// an M, a slice or an array a []any, every number a float64, a nil map or
// slice empty. What values cannot hold fails, its path named as a JSON
// pointer: a func, a chan, a complex, a NaN or an infinity, text that is
// not UTF-8, a key that is not text or a number (a map[any]any's), two
// keys that read as the same, a value that contains itself.
func Normalize(v any) (any, error) { return celvalue.Normalize(v) }

// Copy: a copy of v that shares no map or list with it.
func Copy(v any) any { return celvalue.Copy(v) }

// AsMap: v as a map, or an empty map.
func AsMap(v any) M { return celvalue.AsMap(v) }

// AsList: v as a list, or nil.
func AsList(v any) []any { return celvalue.AsList(v) }

// Merge: Helm's MergeMaps, the merge of its -f files (and sprig's
// mergeOverwrite on values): maps merge, lists and scalars replace, a null
// replaces too and stays null. dst is changed in place and returned; src is
// copied.
func Merge(dst, src M) M { return celvalue.Merge(dst, src) }

// Truthy: v as a Helm condition sees it (empty is false).
func Truthy(v any) bool { return celvalue.Truthy(v) }

// Str: v as text, as Go templates print it.
func Str(v any) string { return celvalue.Str(v) }

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

// Short: a value for a message, the values of its sensitive keys
// redacted (Redact), cut at 160 characters. A caller that knows the key of
// v calls ShortAt.
func Short(v any) string {
	s := fmt.Sprintf("%#v", Redact(v))
	if len(s) > 160 {
		s = s[:160] + "..."
	}
	return s
}
