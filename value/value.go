// Package value is the data every stage shares, and the helpers that read
// it: YAML and JSON values in the form Helm values have after their JSON
// round trip. This single form makes a value computed by CEL equal to the
// same value read from a file.
package value

import (
	"fmt"
	"strings"

	celvalue "github.com/okdp/cel2yaml/value"
)

// M is a values map (a YAML or JSON object).
type M = celvalue.M

// Normalize returns a copy of v, a value a caller built in memory, in
// that form: at any depth a map becomes an M, a slice a []any, every
// number a float64, and a nil map or slice an empty one. It fails on what
// values cannot hold (a func, a NaN, a key that is not text, a value that
// contains itself), and names the path as a JSON pointer.
func Normalize(v any) (any, error) { return celvalue.Normalize(v) }

// Copy returns a copy of v that shares no map or list with it.
func Copy(v any) any { return celvalue.Copy(v) }

// AsMap returns v as a map, or an empty map when it is not one.
func AsMap(v any) M { return celvalue.AsMap(v) }

// AsList returns v as a list, or nil when it is not one.
func AsList(v any) []any { return celvalue.AsList(v) }

// Merge merges src into dst as Helm merges -f files. Maps merge key by
// key; lists, scalars and nulls replace. Merge changes dst in place and
// returns it; it copies what it takes from src.
func Merge(dst, src M) M { return celvalue.Merge(dst, src) }

// Truthy reports whether v is true as a Helm condition sees it.
func Truthy(v any) bool { return celvalue.Truthy(v) }

// Str returns v as text, as Go templates print it.
func Str(v any) string { return celvalue.Str(v) }

// Dig returns the value at a dotted path of m, or nil when there is none.
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

// Short formats v for a message: its sensitive keys redacted, cut at 160
// characters. A caller that knows the key of v calls ShortAt instead.
func Short(v any) string {
	s := fmt.Sprintf("%#v", Redact(v))
	if len(s) > 160 {
		s = s[:160] + "..."
	}
	return s
}
