package chart

import (
	"reflect"
	"strings"
	"testing"

	"github.com/okdp/okdp-compiler/internal/value"
)

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
	if _, ok := defaults["n"]; !ok || value.AsMap(defaults["b"])["d"] != float64(1) || user["a"] != nil {
		t.Errorf("an argument changed: %#v %#v", defaults, user)
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
