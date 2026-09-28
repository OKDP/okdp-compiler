package compiler

import (
	"reflect"
	"testing"
)

// TestNormalize: string keys, and every number a float64.
func TestNormalize(t *testing.T) {
	in := map[any]any{"a": 1, 2: []any{int64(3), uint64(4), float32(0.5)}, "m": map[string]any{"b": "x"}}
	want := M{"a": float64(1), "2": []any{float64(3), float64(4), float64(0.5)}, "m": M{"b": "x"}}
	if got := normalize(in); !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

// TestParseValues: a values file, normalized; invalid YAML fails.
func TestParseValues(t *testing.T) {
	v, err := ParseValues([]byte("a: 1\nb: [x]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if want := (M{"a": float64(1), "b": []any{"x"}}); !reflect.DeepEqual(v, want) {
		t.Errorf("got %#v, want %#v", v, want)
	}
	if _, err := ParseValues([]byte("a: [")); err == nil {
		t.Error("invalid YAML must fail")
	}
}

// TestTruthyStr: values as Helm conditions and Go templates see them.
func TestTruthyStr(t *testing.T) {
	for _, tc := range []struct {
		v      any
		truthy bool
		str    string
	}{
		{nil, false, ""},
		{false, false, "false"},
		{true, true, "true"},
		{"", false, ""},
		{"x", true, "x"},
		{float64(0), false, "0"},
		{float64(3), true, "3"},
		{0.5, true, "0.5"},
		{M{}, false, "map[]"},
		{M{"a": 1}, true, "map[a:1]"},
		{[]any{}, false, "[]"},
		{[]any{1}, true, "[1]"},
	} {
		if got := truthy(tc.v); got != tc.truthy {
			t.Errorf("truthy(%#v) = %v", tc.v, got)
		}
		if got := str(tc.v); got != tc.str {
			t.Errorf("str(%#v) = %q, want %q", tc.v, got, tc.str)
		}
	}
}

// TestCoalesce: Helm's merge of user values over defaults; null deletes.
func TestCoalesce(t *testing.T) {
	got := coalesce(
		M{"a": float64(1), "b": M{"c": float64(1), "d": float64(1)}, "l": []any{float64(1)}},
		M{"a": nil, "b": M{"c": float64(2)}, "l": []any{float64(2)}, "e": "x"},
	)
	want := M{"b": M{"c": float64(2), "d": float64(1)}, "l": []any{float64(2)}, "e": "x"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

// TestMergeOverwrite: maps merge, lists and scalars replace, null sets null.
func TestMergeOverwrite(t *testing.T) {
	got := mergeOverwrite(
		M{"a": float64(1), "b": M{"c": float64(1), "d": float64(1)}, "l": []any{float64(1)}},
		M{"a": nil, "b": M{"c": float64(2)}, "l": []any{float64(2)}},
	)
	want := M{"a": nil, "b": M{"c": float64(2), "d": float64(1)}, "l": []any{float64(2)}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

// TestDeepCopy: the copy shares no map or list with the original.
func TestDeepCopy(t *testing.T) {
	orig := M{"m": M{"l": []any{"x"}}}
	cp := asMap(deepCopy(orig))
	asMap(cp["m"])["l"].([]any)[0] = "y"
	asMap(cp["m"])["k"] = "z"
	if !reflect.DeepEqual(orig, M{"m": M{"l": []any{"x"}}}) {
		t.Errorf("the original changed: %#v", orig)
	}
}

// TestDig: a dotted path, through maps only.
func TestDig(t *testing.T) {
	m := M{"a": M{"b": M{"c": "x"}, "s": "y"}}
	for path, want := range map[string]any{"a.b.c": "x", "a.s": "y", "a.s.t": nil, "a.missing": nil, "z": nil} {
		if got := dig(m, path); got != want {
			t.Errorf("dig(%q) = %#v, want %#v", path, got, want)
		}
	}
}
