package chart

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/okdp/okdp-compiler/internal/helmtest"
	"github.com/okdp/okdp-compiler/internal/value"
)

// valuesFile: a values file of two documents, for TestParseValues and
// FuzzParseValues.
const valuesFile = "a: 1\nb: [x, no, \"no\"]\non: 1.0\n---\nc: ~\n"

// TestParseValues: a values file as Helm 3 reads it (YAML 1.1, numbers as
// float64, the first document only); invalid YAML fails.
func TestParseValues(t *testing.T) {
	v, err := ParseValues([]byte(valuesFile))
	if err != nil {
		t.Fatal(err)
	}
	if want := (M{"a": float64(1), "b": []any{"x", false, "no"}, "true": float64(1)}); !reflect.DeepEqual(v, want) {
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

// parseValuesErrors: what Helm refuses in a values file, for
// TestParseValuesErrors and FuzzParseValues.
var parseValuesErrors = map[string]string{
	"- a\n":  "cannot unmarshal yaml document",
	"a: [\n": "cannot unmarshal yaml document",
	"~: 1\n": "unsupported map key",
}

// TestParseValuesErrors: what Helm refuses in a values file.
func TestParseValuesErrors(t *testing.T) {
	for in, want := range parseValuesErrors {
		if _, err := ParseValues([]byte(in)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: got %v, want an error containing %q", in, err, want)
		}
	}
}

// FuzzParseValues: what ParseValues reads is values (value.Normalize keeps
// it as it is: string keys, float64 numbers, none NaN or infinite, UTF-8
// text), and written as the compiler writes values (yaml.v3, fsio), it
// reads back the same. Seeds: the files of TestParseValues,
// TestParseValuesErrors and the values fixtures (TestHelmValues).
func FuzzParseValues(f *testing.F) {
	f.Add([]byte(valuesFile))
	for in := range parseValuesErrors {
		f.Add([]byte(in))
	}
	files, _ := filepath.Glob(filepath.Join(helmtest.Dir, "values", "*", "*.yaml"))
	for _, name := range files {
		b, err := os.ReadFile(name)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b)
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		v, err := ParseValues(b)
		if err != nil {
			return
		}
		if n, err := value.Normalize(v); err != nil || !reflect.DeepEqual(n, v) {
			t.Fatalf("%q: read as %#v, which values do not hold (%v)", b, v, err)
		}
		var out bytes.Buffer
		enc := yaml.NewEncoder(&out)
		enc.SetIndent(2)
		if err := enc.Encode(v); err != nil {
			t.Fatalf("%q: read as %#v, not written: %v", b, v, err)
		}
		if err := enc.Close(); err != nil {
			t.Fatal(err)
		}
		if back, err := ParseValues(out.Bytes()); err != nil || !reflect.DeepEqual(back, v) {
			t.Errorf("%q: read as %#v, written\n%s\nread back as %#v (%v)", b, v, out.Bytes(), back, err)
		}
	})
}
