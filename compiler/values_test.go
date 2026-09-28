package compiler

import (
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

// TestNormalize: string keys, and every number a float64.
func TestNormalize(t *testing.T) {
	in := map[any]any{"a": 1, 2: []any{int64(3), uint64(4), float32(0.5)}, "m": map[string]any{"b": "x"}}
	want := M{"a": float64(1), "2": []any{float64(3), float64(4), float64(0.5)}, "m": M{"b": "x"}}
	if got := normalize(in); !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

// TestParseValues: a values file as Helm reads it (YAML 1.1, numbers as
// float64, documents merged); invalid YAML fails.
func TestParseValues(t *testing.T) {
	v, err := ParseValues([]byte("a: 1\nb: [x, no, \"no\"]\non: 1.0\n---\nc: ~\n"))
	if err != nil {
		t.Fatal(err)
	}
	if want := (M{"a": float64(1), "b": []any{"x", false, "no"}, "true": float64(1), "c": nil}); !reflect.DeepEqual(v, want) {
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

// TestCoalesceValues: Helm's coalescing of user values over defaults: a null
// deletes a default and stays where there is none, null defaults are
// dropped, and neither argument changes.
func TestCoalesceValues(t *testing.T) {
	defaults := M{"a": float64(1), "n": nil, "b": M{"c": float64(1), "d": float64(1), "n": nil}, "l": []any{float64(1)}, "s": "x"}
	user := M{"a": nil, "b": M{"c": float64(2), "e": nil}, "l": []any{float64(2)}, "s": M{"t": "y"}, "f": nil}
	got := coalesceValues(defaults, user)
	want := M{"b": M{"c": float64(2), "d": float64(1), "e": nil}, "l": []any{float64(2)}, "s": M{"t": "y"}, "f": nil}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
	if _, ok := defaults["n"]; !ok || asMap(defaults["b"])["d"] != float64(1) || user["a"] != nil {
		t.Errorf("an argument changed: %#v %#v", defaults, user)
	}
}

// TestMergeMaps: Helm's merge of -f files: maps merge, lists and scalars
// replace, null sets null; src is copied.
func TestMergeMaps(t *testing.T) {
	src := M{"a": nil, "b": M{"c": float64(2)}, "l": []any{float64(2)}}
	got := mergeMaps(
		M{"a": float64(1), "b": M{"c": float64(1), "d": float64(1)}, "l": []any{float64(1)}},
		src,
	)
	want := M{"a": nil, "b": M{"c": float64(2), "d": float64(1)}, "l": []any{float64(2)}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
	got["l"].([]any)[0] = "changed"
	if src["l"].([]any)[0] != float64(2) {
		t.Error("src shares a list with the result")
	}
}

// TestLayered: the final values merge: null defaults dropped, layers in
// order, nulls kept.
func TestLayered(t *testing.T) {
	got := layered(M{"a": nil, "b": M{"n": nil, "c": float64(1)}}, M{"c": nil}, M{"b": M{"c": nil}, "d": float64(1)})
	want := M{"b": M{"c": nil}, "c": nil, "d": float64(1)}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

// TestParseValuesErrors: what Helm refuses in a values file.
func TestParseValuesErrors(t *testing.T) {
	for in, want := range map[string]string{
		"- a\n":            "cannot unmarshal yaml document",
		"a: 1\n--- b: 2\n": "invalid Yaml document separator: b: 2",
		"a: [\n":           "cannot unmarshal yaml document",
		"~: 1\n":           "unsupported map key",
	} {
		if _, err := ParseValues([]byte(in)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: got %v, want an error containing %q", in, err, want)
		}
	}
}

// TestReadChart: Chart.yaml fields as Helm reads them, numbers as strings.
func TestReadChart(t *testing.T) {
	c, err := readChart(fstest.MapFS{"Chart.yaml": {Data: []byte("name: x\nversion: 1.0.0\nappVersion: 1.10\n")}})
	if err != nil {
		t.Fatal(err)
	}
	if want := (chartMeta{Name: "x", Version: "1.0.0", AppVersion: "1.1"}); c != want {
		t.Errorf("got %#v, want %#v", c, want)
	}
	if _, err := readChart(fstest.MapFS{"Chart.yaml": {Data: []byte("name: [\n")}}); err == nil {
		t.Error("invalid Chart.yaml must fail")
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
