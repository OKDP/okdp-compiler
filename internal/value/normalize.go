// Normalize: values a caller built in memory, of any Go type, read as the
// values they would be once written to a values file and read back.
//
// By reflection rather than a JSON round trip of each layer, which would
// give the same values but name no path in its errors, refuse map[any]any
// (what a YAML decoder gives) and turn text that is not UTF-8 into U+FFFD
// rather than fail. What only encoding/json gives a meaning to (a struct
// and its json tags, a MarshalJSON or MarshalText method) is taken as it
// writes it.
package value

import (
	"encoding"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"reflect"
	"slices"
	"strconv"
	"unicode/utf8"
)

// normalizer: the maps, lists and pointers Normalize is inside, to refuse a
// value that contains itself rather than recurse forever.
type normalizer map[address]bool

// address: a map, list or pointer: where it points, its kind and length.
type address struct {
	p    uintptr
	kind reflect.Kind
	n    int
}

// The types encoding/json writes its own way: by their methods, and
// json.Number as a number.
var (
	jsonMarshaler = reflect.TypeFor[json.Marshaler]()
	textMarshaler = reflect.TypeFor[encoding.TextMarshaler]()
	jsonNumber    = reflect.TypeFor[json.Number]()
)

// value: x normalized (Normalize), at path.
func (o normalizer) value(x any, path string) (any, error) {
	v := reflect.ValueOf(x)
	if !v.IsValid() || v.Kind() == reflect.Pointer && v.IsNil() {
		return nil, nil
	}
	t := v.Type()
	if t.Kind() == reflect.Struct || t.Implements(jsonMarshaler) || t.Implements(textMarshaler) {
		return fromJSON(x, path)
	}
	switch t.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice:
		a := address{v.Pointer(), t.Kind(), 0}
		if t.Kind() == reflect.Slice {
			a.n = v.Len()
		}
		if o[a] {
			return nil, at(path, "the value contains itself")
		}
		o[a] = true
		defer delete(o, a)
		switch {
		case t.Kind() == reflect.Pointer:
			return o.value(v.Elem().Interface(), path)
		case t.Kind() == reflect.Map:
			return o.mapValue(v, path)
		case t.Elem().Kind() == reflect.Uint8:
			// A []byte: its base64 text, as encoding/json writes it.
			return base64.StdEncoding.EncodeToString(v.Bytes()), nil
		}
		return o.list(v, path)
	case reflect.Array:
		return o.list(v, path)
	case reflect.String:
		s := v.String()
		if t == jsonNumber {
			f, err := strconv.ParseFloat(s, 64)
			if err != nil && !errors.Is(err, strconv.ErrRange) {
				return nil, at(path, "json.Number %q is not a number", s)
			}
			return number(f, path)
		}
		if !utf8.ValidString(s) {
			return nil, at(path, "a string that is not UTF-8: %q", s)
		}
		return s, nil
	case reflect.Bool:
		return v.Bool(), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(v.Int()), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return float64(v.Uint()), nil
	case reflect.Float32:
		// As encoding/json writes it: the shortest decimal of the float32,
		// 0.1 rather than 0.10000000149011612.
		f, _ := strconv.ParseFloat(strconv.FormatFloat(v.Float(), 'g', -1, 32), 64)
		return number(f, path)
	case reflect.Float64:
		return number(v.Float(), path)
	}
	return nil, at(path, "a %s cannot be a value", t)
}

// list: v, a slice or an array, as a list; a nil slice is empty.
func (o normalizer) list(v reflect.Value, path string) ([]any, error) {
	out := make([]any, v.Len())
	for i := range out {
		x, err := o.value(v.Index(i).Interface(), Index(path, i))
		if err != nil {
			return nil, err
		}
		out[i] = x
	}
	return out, nil
}

// mapValue: v, a map, with string keys (mapKey), its values normalized in
// key order, so that the error reported is stable; a nil map is empty. Two
// keys that read as the same text fail: one would replace the other, the
// one map iteration gives last.
func (o normalizer) mapValue(v reflect.Value, path string) (M, error) {
	entries := make(map[string]reflect.Value, v.Len())
	var bad []string
	for it := v.MapRange(); it.Next(); {
		k, err := mapKey(it.Key().Interface())
		if _, dup := entries[k]; dup && err == nil {
			err = fmt.Errorf("two keys read as %q", k)
		}
		if err != nil {
			bad = append(bad, err.Error())
			continue
		}
		entries[k] = it.Value()
	}
	if len(bad) > 0 {
		return nil, at(path, "%s", slices.Min(bad))
	}
	out := make(M, len(entries))
	for _, k := range slices.Sorted(maps.Keys(entries)) {
		x, err := o.value(entries[k].Interface(), Key(path, k))
		if err != nil {
			return nil, err
		}
		out[k] = x
	}
	return out, nil
}

// mapKey: a map key as text: a string, or a number or a boolean printed as
// Helm reads a YAML key (sigs.k8s.io/yaml: 1, 1.5, true; a float at the
// precision of a float32); an encoding.TextMarshaler, its text, as
// encoding/json writes it.
func mapKey(x any) (string, error) {
	v := reflect.ValueOf(x)
	var s string
	switch {
	case !v.IsValid():
		return "", errors.New("a map key must be a string, a number or a boolean, got null")
	case v.Kind() == reflect.String:
		s = v.String()
	case v.Type().Implements(textMarshaler) && (v.Kind() != reflect.Pointer || !v.IsNil()):
		b, err := x.(encoding.TextMarshaler).MarshalText()
		if err != nil {
			return "", fmt.Errorf("a map key: %w", err)
		}
		s = string(b)
	case v.CanInt():
		s = strconv.FormatInt(v.Int(), 10)
	case v.CanUint():
		s = strconv.FormatUint(v.Uint(), 10)
	case v.Kind() == reflect.Bool:
		s = strconv.FormatBool(v.Bool())
	case v.CanFloat() && !math.IsNaN(v.Float()) && !math.IsInf(v.Float(), 0):
		s = strconv.FormatFloat(v.Float(), 'g', -1, 32)
	default:
		return "", fmt.Errorf("a map key must be a string, a number or a boolean, got %s", v.Type())
	}
	if !utf8.ValidString(s) {
		return "", fmt.Errorf("a map key that is not UTF-8: %q", s)
	}
	return s, nil
}

// fromJSON: x as encoding/json writes it, read back as values.
func fromJSON(x any, path string) (any, error) {
	b, err := json.Marshal(x)
	if err != nil {
		return nil, at(path, "%v", err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, at(path, "%v", err)
	}
	return out, nil
}

// number: f, unless it is a NaN or an infinity, which a values file cannot
// hold.
func number(f float64, path string) (any, error) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return nil, at(path, "not a finite number: %v", f)
	}
	return f, nil
}

// at: an error at path, of the whole value when path is empty.
func at(path, format string, args ...any) error {
	msg := fmt.Sprintf(format, args...)
	if path == "" {
		return errors.New(msg)
	}
	return fmt.Errorf("%s: %s", path, msg)
}
