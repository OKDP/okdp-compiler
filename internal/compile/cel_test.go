package compile

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/okdp/okdp-compiler/internal/value"
)

// mapCtx: a small context for document tests.
func mapCtx() M {
	return M{
		"release": M{"name": "demo-x", "namespace": "demo"},
		"values": M{
			"items": []any{M{"name": "a", "n": float64(1)}, M{"name": "b", "n": float64(2)}},
			"on":    true,
			"off":   false,
			"extra": M{"k": "v", "x": float64(2)},
		},
	}
}

// runOutput: loads a document on mapCtx and returns its output section.
func runOutput(src string) (M, error) {
	d, err := Load([]byte(src), mapCtx(), nil)
	if err != nil {
		return nil, err
	}
	v, err := d.Section("output")
	return value.AsMap(v), err
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
		"$$ writes a key starting with $": {
			`{$$name: 1, m: {$$if: a, $$$x: b}}`,
			M{"$name": float64(1), "m": M{"$if": "a", "$$x": "b"}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			out, err := runOutput("output:\n  x: " + tc.src + "\n")
			if err != nil {
				t.Fatal(err)
			}
			if got := value.AsMap(out["x"]); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %#v\nwant %#v", got, tc.want)
			}
		})
	}
}

// TestExpressionValues: the value of an expression is data, wherever its
// optionals, numbers, bytes, timestamps are; what the values cannot hold
// fails, at its position in the value.
func TestExpressionValues(t *testing.T) {
	for expr, want := range map[string]any{
		"{'n': values.?nope, 'o': values.?extra.k, 'l': [1, 2u]}": M{"n": nil, "o": "v", "l": []any{float64(1), float64(2)}},
		"[optional.of([optional.none()])]":                        []any{[]any{nil}},
		"9007199254740993":                                        float64(9007199254740992),
		"b'abc'":                                                  "abc",
		"[duration('1h'), timestamp('2024-01-02T03:04:05Z')]":     []any{"3600s", "2024-01-02T03:04:05Z"},
		"first([0, {'a': values.?nope}])":                         M{"a": nil},
		"[str(b'abc'), str({1: 0.0/0.0}), toJson(b'abc')]":        []any{"abc", "map[1:NaN]", `"abc"`},
	} {
		if got, err := Eval(expr, mapCtx()); err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %#v (%v), want %#v", expr, got, err, want)
		}
	}
	for expr, want := range map[string]string{
		"0.0/0.0":                "${{ 0.0/0.0 }}: not a finite number: NaN",
		"{'a': [1.0, -1.0/0.0]}": "${{ {'a': [1.0, -1.0/0.0]} }}: a[1]: not a finite number: -Inf",
		"[{1: 2}]":               "${{ [{1: 2}] }}: [0]: a map key must be a string, got int 1",
		"{'k': {true: 1}}":       "${{ {'k': {true: 1}} }}: k: a map key must be a string, got bool true",
		"b'\\xff'":               "${{ b'\\xff' }}: bytes that are not UTF-8 text (encode them with base64.encode)",
		"type(1)":                "${{ type(1) }}: a CEL type has no data form",
		"toJson(0.0/0.0)":        "${{ toJson(0.0/0.0) }}: toJson: json: unsupported value: NaN",
	} {
		if _, err := Eval(expr, mapCtx()); err == nil || err.Error() != want {
			t.Errorf("%s: got %v, want %q", expr, err, want)
		}
	}
	if _, err := runOutput("output: {x: {l: [1, '${{ 1.0/0.0 }}']}}\n"); err == nil || err.Error() != "output: x: l[1]: ${{ 1.0/0.0 }}: not a finite number: +Inf" {
		t.Errorf("an error at its path: got %v", err)
	}
}

// TestMappingYAML11: the mapping's scalars and keys are read as Helm reads
// values (YAML 1.1), except binding names (let, $let).
func TestMappingYAML11(t *testing.T) {
	out, err := runOutput(`
let:
  n: 1.0
  y: 1.0
  Y: 2.0
  yes: 3.0
output:
  x:
    bools: [yes, no, on, off, y, N, "yes", 'no', !!str on, true]
    numbers: [0777, 0x1F, 1_000, 1.0, 012345678901234567890]
    strings: [2001-12-14, "0777", 1.2.3]
    keys: {on: a, 1.0: b, 0x10: c, "y": d, dup: 1, dup: 2}
    lets: [{$for: c in values.items, $let: {y: '${{ c.n + n }}', Y: '${{ y + Y }}'}, $each: '${{ [y, Y, yes] }}'}]
`)
	if err != nil {
		t.Fatal(err)
	}
	want := M{
		"bools":   []any{true, false, true, false, true, false, "yes", "no", "on", true},
		"numbers": []any{float64(511), float64(31), float64(1000), float64(1), float64(12345678901234567890)},
		"strings": []any{"2001-12-14", "0777", "1.2.3"},
		"keys":    M{"true": "a", "1": "b", "16": "c", "y": "d", "dup": float64(2)},
		"lets":    []any{float64(2), float64(4), float64(3), float64(3), float64(5), float64(3)},
	}
	if got := value.AsMap(out["x"]); !reflect.DeepEqual(got, want) {
		t.Errorf("got  %#v\nwant %#v", got, want)
	}
	if _, err := runOutput("output: {x: {~: 1}}\n"); err == nil || !strings.Contains(err.Error(), "unsupported map key") {
		t.Errorf("a null key: got %v", err)
	}
}

// TestAliases: an alias is a copy of its anchor's node; an alias inside its
// anchor, and aliases expanding to too many nodes, fail.
func TestAliases(t *testing.T) {
	out, err := runOutput("output: {x: {a: &a {k: [1]}, b: *a, c: [*a, *a]}}\n")
	if err != nil {
		t.Fatal(err)
	}
	a := M{"k": []any{float64(1)}}
	if got, want := value.AsMap(out["x"]), (M{"a": a, "b": a, "c": []any{a, a}}); !reflect.DeepEqual(got, want) {
		t.Errorf("got  %#v\nwant %#v", got, want)
	}
	// Ten aliases of the level before, five levels: 211,110 nodes at l5.
	bomb := "l0: &l0 [x]\n"
	for i := 1; i <= 5; i++ {
		bomb += fmt.Sprintf("l%d: &l%d [%s*l%d]\n", i, i, strings.Repeat(fmt.Sprintf("*l%d, ", i-1), 9), i-1)
	}
	for name, tc := range map[string]struct{ src, want string }{
		"a list in itself":   {"output: {a: &x [*x]}\n", "line 1: the alias *x is inside the anchor &x it refers to"},
		"a map in itself":    {"output:\n  a: &x\n    b: {c: *x}\n", "line 3: the alias *x is inside the anchor &x it refers to"},
		"excessive aliasing": {bomb, "line 6: excessive aliasing: the aliases expand to more than 100000 nodes"},
	} {
		if _, err := runOutput(tc.src); err == nil || err.Error() != tc.want {
			t.Errorf("%s: got %v, want %q", name, err, tc.want)
		}
	}
}

// TestMappingFunctions: the host functions in a document.
func TestMappingFunctions(t *testing.T) {
	out, err := runOutput(`
let:
  config: {b: 1, a: [x]}
output:
  x:
    checksum: ${{ sha256(toJson(config)) }}
    json: ${{ toJson(config) }}
    yaml: ${{ toYaml(config) }}
    quoted: ${{ quote(values.items[0].n) }}
`)
	if err != nil {
		t.Fatal(err)
	}
	want := M{
		"checksum": sha256Hex(`{"a":["x"],"b":1}`),
		"json":     `{"a":["x"],"b":1}`,
		"yaml":     "a:\n- x\nb: 1",
		"quoted":   `"1"`,
	}
	if got := value.AsMap(out["x"]); !reflect.DeepEqual(got, want) {
		t.Errorf("got  %#v\nwant %#v", got, want)
	}
	if _, err := runOutput(`output: {x: {v: '${{ toYaml("\x01 \x7f") }}'}}`); err == nil || !strings.Contains(err.Error(), "toYaml: ") {
		t.Errorf("an encoding error: got %v", err)
	}
}

// TestErrorRules: rules ($if and message), per element ($for), beside an
// expression returning messages; the message of a rule that holds no error
// is not evaluated; what an item may not be.
func TestErrorRules(t *testing.T) {
	_, err := runOutput(`
errors:
  - $if: ${{ values.on }}
    message: on is set
  - $if: ${{ values.off }}
    message: '${{ nope }}, not evaluated'
  - $for: c in values.items
    $each:
      $if: ${{ c.n > 1.0 }}
      message: item ${{ c.name }} is too large
  - '${{ ["a list", "of messages"] }}'
`)
	if want := "on is set\nitem b is too large\na list\nof messages"; err == nil || err.Error() != want {
		t.Errorf("got %v, want %q", err, want)
	}
	for name, tc := range map[string]struct{ src, want string }{
		"not a rule":     {"errors:\n  - {$if: '${{ true }}', msg: x}\n", "errors[0]: a rule is {$if: <condition>, message: <text>}, or an expression returning a list of messages"},
		"a string":       {"errors:\n  - '${{ \"x\" }}'\n", "errors[0]: a rule is {$if"},
		"not text":       {"errors:\n  - {message: '${{ 1 }}'}\n", "errors[0]: message must be text, got 1"},
		"empty":          {"errors:\n  - {message: ''}\n", "errors[0]: an empty message"},
		"message error":  {"errors:\n  - '${{ [] }}'\n  - {$if: '${{ true }}', message: '${{ nope }}'}\n", "errors[1]: message: ${{ nope }}:"},
		"$each position": {"errors:\n  - '${{ [] }}'\n  - {$for: c in values.items, $each: {message: '${{ nope }}'}}\n", "errors[1]: message: ${{ nope }}:"},
	} {
		if _, err := runOutput(tc.src); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want an error containing %q", name, err, tc.want)
		}
	}
}

// TestEmptyDocument: an empty, comment-only or null document has no
// section.
func TestEmptyDocument(t *testing.T) {
	for _, src := range []string{"", "# only a comment\n", "---\n", "--- # a comment\n", "~\n"} {
		if out, err := runOutput(src); err != nil || len(out) != 0 {
			t.Errorf("%q: got %v %#v, want no section", src, err, out)
		}
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
	} {
		t.Run(name, func(t *testing.T) {
			_, err := runOutput(tc.src)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

// TestMistakes: mistakes in the language fail at their path, in every
// branch, those no evaluation takes included.
func TestMistakes(t *testing.T) {
	for name, tc := range map[string]struct{ src, want string }{
		"second document":      {"output: {}\n---\nerrors: ['${{ [\"x\"] }}']\n", "line 2: a second YAML document: the mapping is one document"},
		"$for without $each":   {"output: {x: {l: [{$for: c in values.items}]}}\n", "output: x: l[0]: $for: in a list item, needs $each"},
		"$for without $key":    {"output: {x: {m: {$for: c in values.items, $value: 1}}}\n", "output: x: m: $for: in a map, needs $key and $value"},
		"unknown directive":    {"output: {x: {m: {$iff: '${{ true }}', a: 1}}}\n", "output: x: m: $iff: unknown directive (the key $iff is written $$iff)"},
		"unknown in a $for":    {"output: {x: {l: [{$for: c in values.items, $Each: 1}]}}\n", "output: x: l[0]: $Each: unknown directive (the key $Each is written $$Each)"},
		"$each in a map":       {"output: {x: {m: {$each: 1}}}\n", "output: x: m: $each: not in a map, which holds keys, $if, $value, $for (with $key, $value, $let) and $merge"},
		"$key in a list item":  {"output: {x: {l: [{$key: 1}]}}\n", "output: x: l[0]: $key: not in a list item, which holds keys, $if, $value, $merge, $for (with $each, $let) and $spread"},
		"$if beside $for":      {"output: {x: {l: [{$if: '${{ true }}', $for: c in values.items, $each: 1}]}}\n", "output: x: l[0]: $if: not in a list item with $for, which holds $for, $each and $let only"},
		"a key beside $spread": {"output: {x: {l: [{$spread: '${{ [] }}', a: 1}]}}\n", "output: x: l[0]: a: not in a list item with $spread, which holds $spread only"},
		"a key beside $value":  {"output: {x: {m: {$value: 1, $merge: '${{ {} }}'}}}\n", "output: x: m: $merge: not in a map with $value, which holds $if and $value only"},
		"a key beside $for":    {"output: {x: {m: {$for: c in values.items, $key: a, $value: 1, b: 2}}}\n", "output: x: m: b: not in a map with $for, which holds $if, $for, $key, $value and $let only"},
		"literal $if":          {"output: {x: {m: {$if: false, a: 1}}}\n", "output: x: m: $if: takes one expression, ${{ <condition> }}, got false"},
		"interpolated $if":     {"output: {x: {m: {$if: 'x${{ true }}', a: 1}}}\n", `output: x: m: $if: takes one expression, ${{ <condition> }}, got "x${{ true }}"`},
		"literal $merge":       {"output: {x: {m: {$merge: {a: 1}}}}\n", "output: x: m: $merge: takes one expression, ${{ <map> }}, got a map"},
		"literal $spread":      {"output: {x: {l: [{$spread: [1]}]}}\n", "output: x: l[0]: $spread: takes one expression, ${{ <list> }}, got a list"},
		"not evaluated":        {"output: {x: {m: {$if: '${{ false }}', w: [{$iff: 1}]}}}\n", "output: x: m: w[0]: $iff: unknown directive"},
		"$let not a map":       {"output: {x: {l: [{$for: c in values.items, $let: [a], $each: 1}]}}\n", "output: x: l[0]: $let: must be a map of bindings, got a list"},
		"$let bound twice":     {"output: {x: {l: [{$for: c in values.items, $let: {d: 1, d: 2}, $each: 1}]}}\n", "output: x: l[0]: $let d: bound twice"},
		"let bound twice":      {"let: {a: 1, b: 2, a: 3}\n", "let a: bound twice"},
		"let named $":          {"let: {$a: 1}\n", "let $a: a binding name cannot start with $"},
		"let not a map":        {"let: [a]\n", "let: must be a map of bindings, got a list"},
		"let value":            {"let: {a: {$iff: 1}}\n", "let a: $iff: unknown directive"},
		"errors not a list":    {"errors: '${{ [] }}'\n", `errors: must be a list of rules, got "${{ [] }}"`},
		"errors item":          {"errors: [{$iff: '${{ true }}', message: x}]\n", "errors[0]: $iff: unknown directive"},
		"top-level directive":  {"$if: '${{ false }}'\noutput: {}\n", "$if: not a directive of the top level: its keys are sections"},
		".nan":                 {"output: {x: {v: .nan}}\n", "line 1: not a finite number: .nan (quote it for the text)"},
		"-.inf":                {"output:\n  x: {v: [1, -.Inf]}\n", "line 2: not a finite number: -.Inf (quote it for the text)"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := runOutput(tc.src); err == nil || !strings.HasPrefix(err.Error(), tc.want) {
				t.Errorf("got %v, want %q", err, tc.want)
			}
		})
	}
}

// TestMergeKeys: << merges a map, an alias of one, or a list of them, the
// first map of the list winning; a key written after << replaces the merged
// one, a key written before it fails.
func TestMergeKeys(t *testing.T) {
	out, err := runOutput(`
output:
  x:
    a: &a {k: 1, m: {p: 1}}
    b: &b {k: 2, w: 2}
    alias: {<<: *a, k: 3}
    list: {<<: [*a, *b, {o: 4}]}
    let: '${{ [k, m] }}'
let:
  <<: {k: 5}
  m: 6
`)
	if err != nil {
		t.Fatal(err)
	}
	a := M{"k": float64(1), "m": M{"p": float64(1)}}
	want := M{
		"a": a, "b": M{"k": float64(2), "w": float64(2)},
		"alias": M{"k": float64(3), "m": M{"p": float64(1)}},
		"list":  M{"k": float64(1), "m": M{"p": float64(1)}, "w": float64(2), "o": float64(4)},
		"let":   []any{float64(5), float64(6)},
	}
	if got := value.AsMap(out["x"]); !reflect.DeepEqual(got, want) {
		t.Errorf("got  %#v\nwant %#v", got, want)
	}
	for name, tc := range map[string]struct{ src, want string }{
		"written before":  {"output:\n  x:\n    a: &a {k: 1}\n    b:\n      k: 2\n      <<: *a\n", "line 5: k is written before the merge key << that replaces it: write it after <<"},
		"not a map":       {"output:\n  x: {a: &a [1], b: {<<: *a}}\n", "line 2: the merge key << takes a map, or a list of maps"},
		"a list of lists": {"output:\n  x: {<<: [{a: 1}, [b]]}\n", "line 2: the merge key << takes a map, or a list of maps"},
		"a cycle":         {"output: {x: &x {<<: *x}}\n", "line 1: the alias *x is inside the anchor &x it refers to"},
	} {
		if _, err := runOutput(tc.src); err == nil || err.Error() != tc.want {
			t.Errorf("%s: got %v, want %q", name, err, tc.want)
		}
	}
}

// TestTemplateErrors: an error of a template's values names its path.
func TestTemplateErrors(t *testing.T) {
	_, err := EvalTemplate([]byte("values: {a: [1, '${{ nope }}']}\n"), mapCtx(), nil)
	if want := "values: a[1]: ${{ nope }}:"; err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("got %v, want an error containing %q", err, want)
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

// TestBudget: the CEL cost of a compile, shared by its evaluations, and its
// cancellation, before and during an evaluation.
func TestBudget(t *testing.T) {
	one := "let:\n  a: ${{ values.items.map(i, i.n * 2.0) }}\n"
	two := one + "  b: ${{ values.items.map(i, i.n * 2.0) }}\n"
	// The cost of one binding, measured under a limit it cannot reach.
	b := NewBudget(context.Background(), math.MaxUint64)
	if _, err := Load([]byte(one), mapCtx(), b); err != nil {
		t.Fatal(err)
	}
	cost := b.used
	if cost == 0 {
		t.Fatal("no cost counted")
	}
	// Enough for one binding, not for two.
	_, err := Load([]byte(two), mapCtx(), NewBudget(context.Background(), cost))
	if want := fmt.Sprintf("let b: ${{ values.items.map(i, i.n * 2.0) }}: the expressions of the compile exceed its CEL cost limit (%d)", cost); err == nil || err.Error() != want {
		t.Errorf("past the limit: got %v, want %q", err, want)
	}
	if _, err := Load([]byte(two), mapCtx(), NewBudget(context.Background(), 2*cost)); err != nil {
		t.Errorf("within the limit: %v", err)
	}
	// Cancelled before an evaluation: the context's error.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Load([]byte(one), mapCtx(), NewBudget(ctx, 0)); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: got %v, want context.Canceled", err)
	}
	// Cancelled during one: a comprehension stops at its next check.
	items := make([]any, 2*interruptEvery)
	for i := range items {
		items[i] = float64(i)
	}
	_, err = Load([]byte("let:\n  a: ${{ values.items.map(i, i * 2.0) }}\n"), M{"values": M{"items": items}}, NewBudget(doneCtx{context.Background()}, 0))
	if err == nil || !strings.Contains(err.Error(), "operation interrupted") {
		t.Errorf("interrupted: got %v", err)
	}
}

// doneCtx: a context done though its Err is nil, so that the check before
// an evaluation passes and the evaluation sees it.
type doneCtx struct{ context.Context }

func (doneCtx) Done() <-chan struct{} {
	c := make(chan struct{})
	close(c)
	return c
}
