package compiler

import (
	"reflect"
	"strings"
	"testing"
)

// mapCtx: a small context for mapping tests.
func mapCtx() M {
	return M{
		"release": M{"name": "demo-x", "namespace": "demo"},
		"values": M{
			"items": []any{M{"name": "a", "n": float64(1)}, M{"name": "b", "n": float64(2)}},
			"on":    true,
			"off":   false,
			"extra": M{"k": "v", "x": float64(2)},
		},
		"global":   M{"okdp": M{}},
		"platform": M{},
	}
}

// runOutput: runs a mapping on mapCtx and returns its output section.
func runOutput(src string) (M, error) {
	res, err := runCEL([]byte(src), mapCtx())
	if err != nil {
		return nil, err
	}
	return res.output, nil
}

// TestMappingDirectives: each directive of the mapping language, on one chart x.
func TestMappingDirectives(t *testing.T) {
	for name, tc := range map[string]struct {
		src  string // the values of chart x
		want M
	}{
		"lone expression keeps its type": {
			`{num: '${{ values.items[1].n }}', b: '${{ values.on }}', l: '${{ [1, 2] }}'}`,
			M{"num": float64(2), "b": true, "l": []any{float64(1), float64(2)}},
		},
		"interpolation is text": {
			`{s: 'n=${{ values.items[1].n }} on=${{ values.on }}'}`,
			M{"s": "n=2 on=true"},
		},
		"escaped expression": {
			`{s: '$${{ not evaluated }}'}`,
			M{"s": "${{ not evaluated }}"},
		},
		"braces in a string literal": {
			`{s: '${{ {"k": "}}"}.k }}'}`,
			M{"s": "}}"},
		},
		"functions str and first": {
			`{s: '${{ str(1.5) }}', f: '${{ first(["", "y", "z"]) }}', e: '${{ first(["", ""]) }}'}`,
			M{"s": "1.5", "f": "y", "e": ""},
		},
		"$if on a key": {
			`{dropped: {$if: '${{ values.off }}', a: 1}, kept: {$if: '${{ values.on }}', a: 1}}`,
			M{"kept": M{"a": float64(1)}},
		},
		"$if with $value": {
			`{dropped: {$if: '${{ values.off }}', $value: 1}, kept: {$if: '${{ values.on }}', $value: [1]}}`,
			M{"kept": []any{float64(1)}},
		},
		"$if on a list item": {
			`{l: [{$if: '${{ values.off }}', a: 1}, {b: 2}]}`,
			M{"l": []any{M{"b": float64(2)}}},
		},
		"$for in a map": {
			`{m: {$for: c in values.items, $key: '${{ c.name }}', $value: '${{ c.n }}'}}`,
			M{"m": M{"a": float64(1), "b": float64(2)}},
		},
		"$for in a map, guarded by $if": {
			`{m: {$if: '${{ values.off }}', $for: c in values.items, $key: '${{ c.name }}', $value: 1}}`,
			M{},
		},
		"$for in a list, one item each": {
			`{l: [0, {$for: c in values.items, $each: '${{ c.name }}'}]}`,
			M{"l": []any{float64(0), "a", "b"}},
		},
		"$for in a list, several items each": {
			`{l: [{$for: c in values.items, $each: ['${{ c.name }}', '${{ c.name }}2']}]}`,
			M{"l": []any{"a", "a2", "b", "b2"}},
		},
		"$let bindings in order": {
			`{l: [{$for: c in values.items, $let: {d: '${{ c.n * 2.0 }}', e: '${{ d + 1.0 }}'}, $each: '${{ e }}'}]}`,
			M{"l": []any{float64(3), float64(5)}},
		},
		"$spread": {
			`{l: [0, {$spread: '${{ [1, 2] }}'}, 3]}`,
			M{"l": []any{float64(0), float64(1), float64(2), float64(3)}},
		},
		"$merge on top of the keys": {
			`{m: {x: 1, w: 1, $merge: '${{ values.extra }}'}}`,
			M{"m": M{"x": float64(2), "w": float64(1), "k": "v"}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			out, err := runOutput("output:\n  x: " + tc.src + "\n")
			if err != nil {
				t.Fatal(err)
			}
			if got := asMap(out["x"]); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %#v\nwant %#v", got, tc.want)
			}
		})
	}
}

// TestMappingSections: let in order, instance and provides.
func TestMappingSections(t *testing.T) {
	res, err := runCEL([]byte(`
let:
  a: 1.0
  b: ${{ a + 1.0 }}
output:
  x: {b: '${{ b }}'}
instance:
  url: https://${{ release.name }}
provides:
  - contract: http
    values: {url: '${{ "u" + str(b) }}'}
`), mapCtx())
	if err != nil {
		t.Fatal(err)
	}
	if got := asMap(res.output["x"])["b"]; got != float64(2) {
		t.Errorf("output: got %#v, want 2", got)
	}
	if got := res.instance["url"]; got != "https://demo-x" {
		t.Errorf("instance: got %#v", got)
	}
	want := []any{M{"contract": "http", "values": M{"url": "u2"}}}
	if !reflect.DeepEqual(res.provides, want) {
		t.Errorf("provides: got %#v", res.provides)
	}
}

// TestMappingYAML11: the mapping's scalars and keys are read as Helm reads
// values (YAML 1.1), except binding names (let, $let).
func TestMappingYAML11(t *testing.T) {
	res, err := runCEL([]byte(`
let:
  n: 1.0
output:
  x:
    bools: [yes, no, on, off, y, N, "yes", 'no', !!str on, true]
    numbers: [0777, 0x1F, 1_000, 1.0, 012345678901234567890]
    strings: [2001-12-14, "0777", 1.2.3]
    keys: {on: a, 1.0: b, 0x10: c, "y": d, dup: 1, dup: 2}
    lets: [{$for: c in values.items, $let: {y: '${{ c.n + n }}'}, $each: '${{ y }}'}]
`), mapCtx())
	if err != nil {
		t.Fatal(err)
	}
	want := M{
		"bools":   []any{true, false, true, false, true, false, "yes", "no", "on", true},
		"numbers": []any{float64(511), float64(31), float64(1000), float64(1), float64(12345678901234567890)},
		"strings": []any{"2001-12-14", "0777", "1.2.3"},
		"keys":    M{"true": "a", "1": "b", "16": "c", "y": "d", "dup": float64(2)},
		"lets":    []any{float64(2), float64(3)},
	}
	if got := asMap(res.output["x"]); !reflect.DeepEqual(got, want) {
		t.Errorf("got  %#v\nwant %#v", got, want)
	}
	if _, err := runCEL([]byte("output: {x: {~: 1}}\n"), mapCtx()); err == nil || !strings.Contains(err.Error(), "unsupported map key") {
		t.Errorf("a null key: got %v", err)
	}
}

// TestMappingFunctions: the host functions in a mapping, next to a let
// binding named fullname (the function is okdpFullname).
func TestMappingFunctions(t *testing.T) {
	res, err := runCEL([]byte(`
let:
  fullname: ${{ release.name }}-mine
  config: {b: 1, a: [x]}
output:
  x:
    name: ${{ fullname }}
    okdp: ${{ okdpFullname('app') }}
    checksum: ${{ sha256(toJson(config)) }}
    json: ${{ toJson(config) }}
    yaml: ${{ toYaml(config) }}
    quoted: ${{ quote(values.items[0].n) }}
`), mapCtx())
	if err != nil {
		t.Fatal(err)
	}
	want := M{
		"name":     "demo-x-mine",
		"okdp":     "demo-x-app",
		"checksum": sha256Hex(`{"a":["x"],"b":1}`),
		"json":     `{"a":["x"],"b":1}`,
		"yaml":     "a:\n- x\nb: 1",
		"quoted":   `"1"`,
	}
	if got := asMap(res.output["x"]); !reflect.DeepEqual(got, want) {
		t.Errorf("got  %#v\nwant %#v", got, want)
	}
	if _, err := runCEL([]byte(`output: {x: {v: '${{ toYaml("\x01 \x7f") }}'}}`), mapCtx()); err == nil || !strings.Contains(err.Error(), "toYaml: ") {
		t.Errorf("an encoding error: got %v", err)
	}
}

// TestMappingErrors: invalid mappings, and where their errors point.
func TestMappingErrors(t *testing.T) {
	for name, tc := range map[string]struct{ src, want string }{
		"errors are collected":  {"errors:\n  - '${{ [\"one\"] }}'\n  - '${{ [] }}'\n  - '${{ [\"two\"] }}'\n", "one\ntwo"},
		"errors position":       {"errors:\n  - '${{ [] }}'\n  - '${{ nope }}'\n", "errors[1]: ${{ nope }}:"},
		"not a map":             {"- a\n", "must be a YAML map"},
		"invalid YAML":          {"output: [\n", "yaml"},
		"unterminated":          {"output: {x: {v: '${{ 1 + '}}\n", "unterminated ${{"},
		"syntax error":          {"output: {x: {v: '${{ 1 + }}'}}\n", "${{ 1 + }}"},
		"unknown variable":      {"output: {x: {v: '${{ nope }}'}}\n", "undeclared reference"},
		"key path in the error": {"output: {x: {a: {b: '${{ nope }}'}}}\n", "output: x: a: b:"},
		"list positions":        {"output: {x: {l: [a, {b: [1, '${{ nope }}']}]}}\n", "output: x: l[1]: b[1]: ${{ nope }}:"},
		"list in a list":        {"output: {x: {l: [[1, '${{ nope }}']]}}\n", "output: x: l[0][1]: ${{ nope }}:"},
		"$for position":         {"output: {x: {l: [0, {$for: c in values.items, $each: '${{ nope }}'}]}}\n", "output: x: l[1]: ${{ nope }}:"},
		"$spread position":      {"output: {x: {l: [0, {$spread: '${{ nope }}'}]}}\n", "output: x: l[1]: ${{ nope }}:"},
		"let name":              {"let: {a: '${{ nope }}'}\n", "let a:"},
		"let list position":     {"let: {a: [1, '${{ nope }}']}\n", "let a[1]: ${{ nope }}:"},
		"$let name":             {"output: {x: {l: [{$for: c in values.items, $let: {d: '${{ nope }}'}, $each: 1}]}}\n", "output: x: l[0]: $let d: ${{ nope }}:"},
		"$for syntax":           {"output: {x: {m: {$for: c values.items, $key: a, $value: 1}}}\n", `is not "<name> in <expression>"`},
		"$for not a list":       {"output: {x: {m: {$for: c in values.extra, $key: a, $value: 1}}}\n", "not a list"},
		"function error":        {"output: {x: {v: '${{ okdpDcr({}) }}'}}\n", "authMethod"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := runCEL([]byte(tc.src), mapCtx())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

// TestScan: expressions, nested braces in strings, and the $${{ escape.
func TestScan(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []piece
	}{
		{"plain", []piece{{text: "plain"}}},
		{"${{ a }}", []piece{{text: " a ", expr: true}}},
		{`a ${{ {"k": "}}"}.k }} $${{ b`, []piece{{text: "a "}, {text: ` {"k": "}}"}.k `, expr: true}, {text: " ${{ b"}}},
		{`${{ 'it\'s }}' }}`, []piece{{text: ` 'it\'s }}' `, expr: true}}},
		{"${{ {'a': {'b': 1}} }}x", []piece{{text: " {'a': {'b': 1}} ", expr: true}, {text: "x"}}},
	} {
		got, err := scan(tc.in)
		if err != nil {
			t.Errorf("%q: %v", tc.in, err)
			continue
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%q: got %#v, want %#v", tc.in, got, tc.want)
		}
	}
	if _, err := scan("${{ a }"); err == nil {
		t.Error("an unterminated expression must fail")
	}
}
