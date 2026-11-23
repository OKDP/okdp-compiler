package chart

import (
	"reflect"
	"strings"
	"testing"

	"github.com/okdp/okdp-compiler/value"
)

// TestParseValues: a values file as Helm 3 reads it (YAML 1.1, numbers as
// float64, the first document only).
func TestParseValues(t *testing.T) {
	v, err := ParseValues([]byte("a: 1\nb: [x, no, \"no\"]\non: 1.0\n---\nc: ~\n"))
	if err != nil {
		t.Fatal(err)
	}
	if want := (M{"a": float64(1), "b": []any{"x", false, "no"}, "true": float64(1)}); !reflect.DeepEqual(v, want) {
		t.Errorf("got %#v, want %#v", v, want)
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

// TestParseValuesErrors: what Helm refuses in a values file, invalid YAML
// among it.
func TestParseValuesErrors(t *testing.T) {
	for in, want := range map[string]string{
		"- a\n":  "cannot unmarshal yaml document",
		"a: [\n": "cannot unmarshal yaml document",
		"~: 1\n": "unsupported map key",
	} {
		if _, err := ParseValues([]byte(in)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: got %v, want an error containing %q", in, err, want)
		}
	}
}
