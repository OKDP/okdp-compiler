// Fields: the maps of known keys a mapping writes (a client of
// oidcClients, okdpGeneratedSecret's arguments and keys, a provides item
// and its secret), checked against a table of their keys, each of a kind;
// a rule only one of them has stays in its validator.
package okdp

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/okdp/okdp-compiler/internal/value"
)

// field: a key of a structured section, the kind of its value, and whether
// it is required: set, and not empty ("", [] or {}).
type field struct {
	name     string
	kind     fieldKind
	required bool
}

// fieldKind: what a field holds: want, as the errors write it; is, its test
// (nil: any value, which the validator checks itself); item, the kind of
// each item of a list or value of a map, checked one by one.
type fieldKind struct {
	want string
	is   func(any) bool
	item *fieldKind
}

// The kinds of fields. A number is a float64, as the data model holds it.
var (
	anyValue        = fieldKind{}
	aString         = fieldKind{want: "a string", is: isA[string]}
	nonEmptyString  = fieldKind{want: "a non-empty string", is: func(v any) bool { return isA[string](v) && v != "" }}
	aBoolean        = fieldKind{want: "true or false", is: isA[bool]}
	aMap            = fieldKind{want: "a map", is: isA[M]}
	aList           = fieldKind{want: "a list", is: isA[[]any]}
	nonEmptyStrings = fieldKind{want: "a list of non-empty strings", is: isA[[]any], item: &nonEmptyString}
	stringMap       = fieldKind{want: "a map of strings", is: isA[M], item: &aString}
	aScalar         = fieldKind{want: "a string, a number or a boolean", is: func(v any) bool { return isA[string](v) || isA[float64](v) || isA[bool](v) }}
	scalarMap       = fieldKind{want: "a map of strings, numbers or booleans", is: isA[M], item: &aScalar}
)

// isA: v is a T.
func isA[T any](v any) bool {
	_, ok := v.(T)
	return ok
}

// check: v, the value at where of key, of the kind k, and so is each of its
// items (key[<i>]) or values (key "<name>", in key order).
func (k fieldKind) check(where, key string, v any) error {
	if k.is == nil {
		return nil
	}
	if !k.is(v) {
		return fmt.Errorf("%s: %s must be %s, got %s", where, key, k.want, value.Short(v))
	}
	if k.item == nil {
		return nil
	}
	switch t := v.(type) {
	case []any:
		for i, x := range t {
			if err := k.item.check(where, fmt.Sprintf("%s[%d]", key, i), x); err != nil {
				return err
			}
		}
	case M:
		for _, name := range slices.Sorted(maps.Keys(t)) {
			if err := k.item.check(where, fmt.Sprintf("%s %q", key, name), t[name]); err != nil {
				return err
			}
		}
	}
	return nil
}

// fieldNames: the keys of spec, in order.
func fieldNames(spec []field) []string {
	names := make([]string, len(spec))
	for i, f := range spec {
		names[i] = f.name
	}
	return names
}

// fieldMap: v, the value at where, as a map of the keys of spec; the error
// of a value of another type (null included) lists them.
func fieldMap(where string, v any, spec []field) (M, error) {
	m, ok := v.(M)
	if !ok {
		return nil, fmt.Errorf("%s: must be a map (%s), got %s", where, strings.Join(fieldNames(spec), ", "), value.Short(v))
	}
	return m, nil
}

// unknownKeys: the keys of m outside allowed, sorted; a null key is not
// set ($if, or a null expression), whatever its name.
func unknownKeys(m M, allowed []string) []string {
	var out []string
	for k, v := range m {
		if v != nil && !slices.Contains(allowed, k) {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}

// fields: m, the map at where, checked against spec: no key outside it
// (unknownKeys), then its keys (knownFields).
func fields(where string, m M, spec []field) error {
	if unknown := unknownKeys(m, fieldNames(spec)); len(unknown) > 0 {
		return fmt.Errorf("%s: unknown key(s) %s (allowed: %s)", where, strings.Join(unknown, ", "), strings.Join(fieldNames(spec), ", "))
	}
	return knownFields(where, m, spec)
}

// knownFields: the keys of spec in m, the map at where, checked in the
// order of spec, the first error reported: a null key is not set, a key
// set is of its kind, a required key is set and not empty. The other keys
// of m are not read: a provides item ignores them.
func knownFields(where string, m M, spec []field) error {
	for _, f := range spec {
		v := m[f.name]
		if v != nil {
			if err := f.kind.check(where, f.name, v); err != nil {
				return err
			}
		}
		if f.required && empty(v) {
			return fmt.Errorf("%s: %s is required", where, f.name)
		}
	}
	return nil
}

// empty: v is null, "", [] or {}: a required field not given.
func empty(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return t == ""
	case []any:
		return len(t) == 0
	case M:
		return len(t) == 0
	}
	return false
}
