package compile

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/types/ref"

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

// mapping: a document of one file, values.cel.yaml.
func mapping(src string) Source { return Source{Name: "values.cel.yaml", Data: []byte(src)} }

// runOutput: evaluates a document of one file, values.cel.yaml, on mapCtx
// and returns its output section.
func runOutput(src string) (M, error) { return evalOutput(mapping(src)) }

// evalOutput: evaluates the document of srcs on mapCtx and returns its
// output section.
func evalOutput(srcs ...Source) (M, error) {
	d, err := NewSession(context.Background(), 0).Eval(mapCtx(), []string{"output"}, srcs...)
	if err != nil {
		return nil, err
	}
	return value.AsMap(d.Sections["output"]), nil
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
			`{s: '${{ str(1.5) }}', f: '${{ first(["", "y", "z"]) }}', e: '${{ first(["", ""]) }}', l: '${{ first("x") }}'}`,
			M{"s": "1.5", "f": "y", "e": "", "l": ""},
		},
		"$if on a key": {
			`{dropped: {$if: '${{ values.off }}', a: 1}, kept: {$if: '${{ values.on }}', a: 1}}`,
			M{"kept": M{"a": float64(1)}},
		},
		"$if with $value": {
			`{dropped: {$if: '${{ values.off }}', $value: 1}, kept: {$if: '${{ values.on }}', $value: [1]}}`,
			M{"kept": []any{float64(1)}},
		},
		"$else when the condition is not true": {
			`{a: {$if: '${{ values.on }}', $value: 1, $else: 2}, b: {$if: '${{ values.off }}', $value: 1, $else: {c: '${{ values.items[0].name }}'}}}`,
			M{"a": float64(1), "b": M{"c": "a"}},
		},
		"$else not evaluated when the condition is true": {
			`{a: {$if: '${{ values.on }}', $value: 1, $else: '${{ values.nope }}'}}`,
			M{"a": float64(1)},
		},
		"$else list is one item": {
			`{l: [0, {$if: '${{ values.off }}', $value: 1, $else: [2, 3]}]}`,
			M{"l": []any{float64(0), []any{float64(2), float64(3)}}},
		},
		"$else without $if is not read": {
			`{a: {$value: 1, $else: '${{ nope }}'}}`,
			M{"a": float64(1)},
		},
		"$else nested, an else if": {
			`{a: {$if: '${{ values.off }}', $value: 1, $else: {$if: '${{ values.off }}', $value: 2, $else: 3}}, b: {$if: '${{ values.off }}', $value: 1, $else: {$if: '${{ values.off }}', $value: 2}}}`,
			M{"a": float64(3)},
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
		"$vars bindings in order": {
			`{l: [{$for: c in values.items, $vars: {d: '${{ c.n * 2.0 }}', e: '${{ d + 1.0 }}'}, $each: '${{ e }}'}]}`,
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
		"directives of any node": {
			`{a: {$if: false, b: 1}, c: {$if: 'x${{ false }}', $value: 1}, m: {x: 1, $merge: {z: 2}}, l: [{$spread: [1, '${{ 2 }}']}]}`,
			M{"c": float64(1), "m": M{"x": float64(1), "z": float64(2)}, "l": []any{float64(1), float64(2)}},
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

// TestExpressionValues: the value of an expression, and a host function's
// argument, is data, as cel-go converts it to JSON, wherever its optionals,
// numbers, bytes, timestamps are; what has no data form fails.
func TestExpressionValues(t *testing.T) {
	s := NewSession(context.Background(), 0)
	for expr, want := range map[string]any{
		"{'o': values.?extra.k, 'l': [1, 2u]}":                M{"o": "v", "l": []any{float64(1), float64(2)}},
		"[9007199254740993, 0.0/0.0, -1.0/0.0]":               []any{"9007199254740993", "NaN", "-Infinity"},
		"b'abc'":                                              "YWJj",
		"[duration('1h'), timestamp('2024-01-02T03:04:05Z')]": []any{"3600s", "2024-01-02T03:04:05Z"},
		"first([0, {'a': values.?extra.k}])":                  M{"a": "v"},
		"[str(b'abc'), str(0.0/0.0), toJson(b'abc')]":         []any{"YWJj", "NaN", `"YWJj"`},
	} {
		if got, err := s.eval(expr, mapCtx()); err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %#v (%v), want %#v", expr, got, err, want)
		}
	}
	for expr, want := range map[string]string{
		"[{1: 2}]":        "${{ [{1: 2}] }}: unsupported type conversion from 'int' to string",
		"optional.none()": "${{ optional.none() }}: optional.none() dereference",
		"type(1)":         "${{ type(1) }}: type conversion not supported for 'type'",
		"str(type(1))":    "${{ str(type(1)) }}: type conversion not supported for 'type'",
	} {
		if _, err := s.eval(expr, mapCtx()); err == nil || err.Error() != want {
			t.Errorf("%s: got %v, want %q", expr, err, want)
		}
	}
	if _, err := runOutput("output: {x: {l: [1, '${{ type(1) }}']}}\n"); err == nil || err.Error() != "values.cel.yaml: output: x: l[1]: ${{ type(1) }}: type conversion not supported for 'type'" {
		t.Errorf("an error at its path: got %v", err)
	}
}

// TestLibraries: the CEL extensions at their pinned versions (Session.env):
// json.encode (encoders 1), format's precision at most 100 (strings 5),
// last (optional types 2).
func TestLibraries(t *testing.T) {
	s := NewSession(context.Background(), 0)
	for expr, want := range map[string]any{
		"json.encode({'b': [1, 'x'], 'a': 2.5})": `{"a":2.5,"b":[1,"x"]}`,
		"'%.2f'.format([1.0])":                   "1.00",
		"[1, 2].last()":                          float64(2),
	} {
		if got, err := s.eval(expr, mapCtx()); err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %#v (%v), want %#v", expr, got, err, want)
		}
	}
	if _, err := s.eval("'%.101f'.format([1.0])", mapCtx()); err == nil || !strings.Contains(err.Error(), "precision") {
		t.Errorf("a precision over 100: got %v", err)
	}
}

// yamlDoc: a document of YAML 1.2 scalars and keys, for TestMappingYAML12
// and FuzzMappingReader.
const yamlDoc = `
vars:
  n: 1.0
  y: 2.0
  y: 3.0
output:
  x:
    bools: [yes, no, on, off, y, N, "yes", true, False]
    numbers: [0777, 0o17, 0x1F, 1_000, 1.0, 012345678901234567890]
    strings: [2001-12-14, "0777", 1.2.3, !!str 1]
    keys: {on: a, 1.0: b, 0x10: c, "y": d, <<: e, ~: f, dup: 1, dup: 2}
    vars: [{$for: c in values.items, $vars: {Y: '${{ c.n + n }}'}, $each: '${{ [y, Y] }}'}]
`

// readerErrors: the documents the reader refuses, and their errors, for
// TestMappingYAML12 and FuzzMappingReader.
var readerErrors = map[string]string{
	"output:\n  x:\n    a: &a {k: 1}\n    b: *a\n": "values.cel.yaml: line 4: aliases are not read: bind the value in vars",
	"output: {x: {v: !!bool yes}}\n":               "values.cel.yaml: line 1: yaml: cannot decode !!str `yes` as a !!bool",
	"output: {x: {v: !!binary gIA=}}\n":            "values.cel.yaml: line 1: jsontext: invalid UTF-8",
}

// TestMappingYAML12: the mapping's scalars are read as yaml.v3 decodes
// them (YAML 1.2: yes and on are text), its keys as written, the later of
// a key written twice winning; a second document is ignored; aliases are
// refused.
func TestMappingYAML12(t *testing.T) {
	out, err := runOutput(yamlDoc)
	if err != nil {
		t.Fatal(err)
	}
	want := M{
		"bools":   []any{"yes", "no", "on", "off", "y", "N", "yes", true, false},
		"numbers": []any{float64(511), float64(15), float64(31), float64(1000), float64(1), float64(12345678901234567890)},
		"strings": []any{"2001-12-14", "0777", "1.2.3", "1"},
		"keys":    M{"on": "a", "1.0": "b", "0x10": "c", "y": "d", "<<": "e", "~": "f", "dup": float64(2)},
		"vars":    []any{float64(3), float64(2), float64(3), float64(3)},
	}
	if got := value.AsMap(out["x"]); !reflect.DeepEqual(got, want) {
		t.Errorf("got  %#v\nwant %#v", got, want)
	}
	// Found by FuzzMappingReader: a block scalar ending the file keeps its
	// newline, as in Helm.
	if out, err := runOutput("output:\n  x:\n    v: >\n      text"); err != nil || value.AsMap(out["x"])["v"] != "text\n" {
		t.Errorf("a block scalar ending the file: got %#v (%v)", out, err)
	}
	if out, err := runOutput("output: {x: 1}\n---\noutput: {x: 2}\n"); err != nil || out["x"] != float64(1) {
		t.Errorf("a second document: got %#v (%v)", out, err)
	}
	for src, want := range readerErrors {
		if _, err := runOutput(src); err == nil || err.Error() != want {
			t.Errorf("%q: got %v, want %q", src, err, want)
		}
	}
}

// FuzzMappingReader: the mapping reader (parseDoc) returns on any document,
// with values as values hold them (value.Normalize keeps them as they are).
// Seeds: the documents of TestMappingYAML12.
func FuzzMappingReader(f *testing.F) {
	f.Add([]byte(yamlDoc))
	for src := range readerErrors {
		f.Add([]byte(src))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		top, err := parseDoc(b)
		if err != nil {
			return
		}
		v := plain(top)
		if n, err := value.Normalize(v); err != nil || !reflect.DeepEqual(n, v) {
			t.Fatalf("%q: read as %#v, which values do not hold (%v)", b, v, err)
		}
	})
}

// plain: a value parseDoc read, its maps as values maps.
func plain(v any) any {
	switch t := v.(type) {
	case *omap:
		m := M{}
		for _, f := range t.fields() {
			m[f.key] = plain(f.val)
		}
		return m
	case []any:
		l := make([]any, len(t))
		for i, x := range t {
			l[i] = plain(x)
		}
		return l
	}
	return v
}

// TestMappingFunctions: the host functions in a document.
func TestMappingFunctions(t *testing.T) {
	out, err := runOutput(`
vars:
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
		"checksum": fmt.Sprintf("%x", sha256.Sum256([]byte(`{"a":["x"],"b":1}`))),
		"json":     `{"a":["x"],"b":1}`,
		"yaml":     "a:\n- x\nb: 1",
		"quoted":   `"1"`,
	}
	if got := value.AsMap(out["x"]); !reflect.DeepEqual(got, want) {
		t.Errorf("got  %#v\nwant %#v", got, want)
	}
	if _, err := runOutput(`output: {x: {v: '${{ toYaml("\x01 \x7f") }}'}}`); err == nil || !strings.Contains(err.Error(), `${{ toYaml("\x01 \x7f") }}: yaml: `) {
		t.Errorf("an encoding error: got %v", err)
	}
}

// TestErrorRules: the errors section, a list: each item it gives that is
// not empty a message ($if and $value, per element with $for, a list
// spliced with $spread), or the list an expression gives; the message of
// an item left out is not evaluated; an error at the item's position.
func TestErrorRules(t *testing.T) {
	_, err := runOutput(`
errors:
  - $if: ${{ values.on }}
    $value: on is set
  - $if: ${{ values.off }}
    $value: '${{ values.nope }}, not evaluated'
  - $for: c in values.items
    $each:
      $if: ${{ c.n > 1.0 }}
      $value: item ${{ c.name }} is too large
  - ''
  - $spread: ${{ values.items.map(i, i.name) }}
`)
	if want := "on is set\nitem b is too large\na\nb"; err == nil || err.Error() != want {
		t.Errorf("got %v, want %q", err, want)
	}
	if _, err := runOutput("errors: ${{ values.items.filter(i, i.n > 1.0).map(i, 'no ' + i.name) }}\n"); err == nil || err.Error() != "no b" {
		t.Errorf("an expression: got %v, want %q", err, "no b")
	}
	for name, tc := range map[string]struct{ src, want string }{
		"message error":  {"errors:\n  - {$if: '${{ false }}', $value: x}\n  - {$if: '${{ true }}', $value: '${{ values.nope }}'}\n", "values.cel.yaml: errors[1]: ${{ values.nope }}: no such key: nope"},
		"$each position": {"errors:\n  - {$if: '${{ false }}', $value: x}\n  - {$for: c in values.items, $each: '${{ values.nope }}'}\n", "values.cel.yaml: errors[1]: ${{ values.nope }}: no such key: nope"},
	} {
		if _, err := runOutput(tc.src); err == nil || err.Error() != tc.want {
			t.Errorf("%s: got %v, want %q", name, err, tc.want)
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
		"errors position":       {"errors:\n  - {$if: '${{ false }}', $value: x}\n  - {$if: '${{ nope }}', $value: x}\n", "values.cel.yaml: errors[1]: ${{ nope }}:"},
		"not a map":             {"- a\n", "values.cel.yaml: the mapping must be a YAML map"},
		"invalid YAML":          {"output: [\n", "values.cel.yaml: yaml: line 1: did not find expected node content"},
		"unterminated":          {"output: {x: {v: '${{ 1 + '}}\n", "unterminated ${{"},
		"syntax error":          {"output: {x: {v: '${{ 1 + }}'}}\n", "${{ 1 + }}"},
		"unknown variable":      {"output: {x: {v: '${{ nope }}'}}\n", "undeclared reference"},
		"key path in the error": {"output: {x: {a: {b: '${{ nope }}'}}}\n", "values.cel.yaml: output: x: a: b:"},
		"list positions":        {"output: {x: {l: [a, {b: [1, '${{ nope }}']}]}}\n", "values.cel.yaml: output: x: l[1]: b[1]: ${{ nope }}:"},
		"list in a list":        {"output: {x: {l: [[1, '${{ nope }}']]}}\n", "values.cel.yaml: output: x: l[0][1]: ${{ nope }}:"},
		"$for position":         {"output: {x: {l: [0, {$for: c in values.items, $each: '${{ nope }}'}]}}\n", "values.cel.yaml: output: x: l[1]: ${{ nope }}:"},
		"$spread position":      {"output: {x: {l: [0, {$spread: '${{ nope }}'}]}}\n", "values.cel.yaml: output: x: l[1]: ${{ nope }}:"},
		"vars name":             {"vars: {a: '${{ nope }}'}\n", "values.cel.yaml: vars a:"},
		"vars list position":    {"vars: {a: [1, '${{ nope }}']}\n", "values.cel.yaml: vars a[1]: ${{ nope }}:"},
		"$vars name":            {"output: {x: {l: [{$for: c in values.items, $vars: {d: '${{ nope }}'}, $each: 1}]}}\n", "values.cel.yaml: output: x: l[0]: $vars d: ${{ nope }}:"},
		"$for syntax":           {"output: {x: {m: {$for: c values.items, $key: a, $value: 1}}}\n", `is not "<name> in <expression>"`},
		"$for not a list":       {"output: {x: {m: {$for: c in values.extra, $key: a, $value: 1}}}\n", "not a list"},
		"$else path":            {"output: {x: {m: {$if: '${{ values.off }}', $value: 1, $else: {b: '${{ values.nope }}'}}}}\n", "values.cel.yaml: output: x: m: b: ${{ values.nope }}: no such key: nope"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := runOutput(tc.src)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

// TestMistakes: mistakes in the language, and expressions that do not
// compile, fail when the document is loaded, at their path, in every
// branch, those no evaluation takes included.
func TestMistakes(t *testing.T) {
	for name, tc := range map[string]struct{ src, want string }{
		"unknown section":      {"outptu: {}\n", "values.cel.yaml: outptu: unknown section (allowed: vars, errors, output)"},
		"untaken branch":       {"output: {x: {m: {$if: '${{ values.off }}', a: '${{ nope }}'}}}\n", "values.cel.yaml: output: x: m: a: ${{ nope }}: ERROR: <input>:1:2: undeclared reference to 'nope'"},
		"under $each":          {"output: {x: {l: [{$for: c in values.items, $each: {$iff: 1}}]}}\n", "values.cel.yaml: output: x: l[0]: $each: $iff: not allowed here (allowed: $if, $merge)"},
		"$for without $each":   {"output: {x: {l: [{$for: c in values.items}]}}\n", "values.cel.yaml: output: x: l[0]: $for: in a list item, needs $each"},
		"$for without $key":    {"output: {x: {m: {$for: c in values.items, $value: 1}}}\n", "values.cel.yaml: output: x: m: $for: in a map, needs $key and $value"},
		"unknown directive":    {"output: {x: {m: {$iff: '${{ true }}', a: 1}}}\n", "values.cel.yaml: output: x: m: $iff: not allowed here (allowed: $if, $merge)"},
		"unknown in a $for":    {"output: {x: {l: [{$for: c in values.items, $Each: 1}]}}\n", "values.cel.yaml: output: x: l[0]: $Each: not allowed here (allowed: $for, $each, $vars)"},
		"$each in a map":       {"output: {x: {m: {$each: 1}}}\n", "values.cel.yaml: output: x: m: $each: not allowed here (allowed: $if, $merge)"},
		"$key in a list item":  {"output: {x: {l: [{$key: 1}]}}\n", "values.cel.yaml: output: x: l[0]: $key: not allowed here (allowed: $if, $merge)"},
		"$if beside $for":      {"output: {x: {l: [{$if: '${{ true }}', $for: c in values.items, $each: 1}]}}\n", "values.cel.yaml: output: x: l[0]: $if: not allowed here (allowed: $for, $each, $vars)"},
		"a key beside $spread": {"output: {x: {l: [{$spread: '${{ [] }}', a: 1}]}}\n", "values.cel.yaml: output: x: l[0]: a: not allowed here (allowed: $spread)"},
		"a key beside $value":  {"output: {x: {m: {$value: 1, $merge: '${{ {} }}'}}}\n", "values.cel.yaml: output: x: m: $merge: not allowed here (allowed: $if, $value, $else)"},
		"$else without $value": {"output: {x: {l: [{$if: '${{ true }}', a: 1, $else: 2}]}}\n", "values.cel.yaml: output: x: l[0]: $else: not allowed here (allowed: $if, $merge)"},
		"$else beside $for":    {"output: {x: {m: {$if: '${{ true }}', $for: c in values.items, $key: a, $value: 1, $else: 2}}}\n", "values.cel.yaml: output: x: m: $else: not allowed here (allowed: $if, $for, $key, $value, $vars)"},
		"under $else":          {"output: {x: {m: {$if: '${{ true }}', $value: 1, $else: {$iff: 1}}}}\n", "values.cel.yaml: output: x: m: $else: $iff: not allowed here (allowed: $if, $merge)"},
		"untaken $else":        {"output: {x: {m: {$if: '${{ true }}', $value: 1, $else: '${{ nope }}'}}}\n", "values.cel.yaml: output: x: m: ${{ nope }}: ERROR: <input>:1:2: undeclared reference to 'nope'"},
		"a key beside $for":    {"output: {x: {m: {$for: c in values.items, $key: a, $value: 1, b: 2}}}\n", "values.cel.yaml: output: x: m: b: not allowed here (allowed: $if, $for, $key, $value, $vars)"},
		"under $merge":         {"output: {x: {m: {$merge: {$iff: 1}}}}\n", "values.cel.yaml: output: x: m: $merge: $iff: not allowed here (allowed: $if, $merge)"},
		"not evaluated":        {"output: {x: {m: {$if: '${{ false }}', w: [{$iff: 1}]}}}\n", "values.cel.yaml: output: x: m: w[0]: $iff: not allowed here (allowed: $if, $merge)"},
		"$vars not a map":      {"output: {x: {l: [{$for: c in values.items, $vars: [a], $each: 1}]}}\n", "values.cel.yaml: output: x: l[0]: $vars: must be a map of bindings, got a list"},
		"vars not a map":       {"vars: [a]\n", "values.cel.yaml: vars: must be a map of bindings, got a list"},
		"vars value":           {"vars: {a: {$iff: 1}}\n", "values.cel.yaml: vars a: $iff: not allowed here (allowed: $if, $merge)"},
		"errors item":          {"errors: [{$iff: '${{ true }}', $value: x}]\n", "values.cel.yaml: errors[0]: $iff: not allowed here (allowed: $if, $value, $else)"},
		"top-level directive":  {"$if: '${{ false }}'\noutput: {}\n", "values.cel.yaml: $if: unknown section (allowed: vars, errors, output)"},
		".nan":                 {"output: {x: {v: .nan}}\n", "values.cel.yaml: line 1: json: cannot marshal from Go float64: unsupported value: NaN"},
		"-.inf":                {"output:\n  x: {v: [1, -.Inf]}\n", "values.cel.yaml: line 2: json: cannot marshal from Go float64: unsupported value: -Inf"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := runOutput(tc.src); err == nil || !strings.HasPrefix(err.Error(), tc.want) {
				t.Errorf("got %v, want %q", err, tc.want)
			}
		})
	}
}

// TestTemplateErrors: an error of a template's values names its file and
// its path.
func TestTemplateErrors(t *testing.T) {
	_, err := NewSession(context.Background(), 0).Eval(mapCtx(), []string{"values"}, Source{Name: "t.cel.yaml", Data: []byte("values: {a: [1, '${{ nope }}']}\n")})
	if want := "t.cel.yaml: values: a[1]: ${{ nope }}:"; err == nil || !strings.HasPrefix(err.Error(), want) {
		t.Errorf("got %v, want an error containing %q", err, want)
	}
}

// TestSources: a document read from several files: the bindings of vars
// seen by every one, a file that is one section whole (its top-level
// directives those of the section's map), the keys a file skips; the
// messages of errors as written.
func TestSources(t *testing.T) {
	okdp := Source{Name: "okdp.yaml", Skip: []string{"roles", "output"}, Data: []byte("roles: [x]\noutput: {x: 1}\nvars:\n  n: ${{ values.items.size() }}\n")}
	values := func(src string) Source { return Source{Name: "values.cel.yaml", Section: "output", Data: []byte(src)} }
	out, err := evalOutput(okdp, values("$merge: \"${{ {'b': n} }}\"\na: ${{ n }}\n"))
	if want := (M{"a": float64(2), "b": float64(2)}); err != nil || !reflect.DeepEqual(out, want) {
		t.Errorf("got %#v (%v), want %#v", out, err, want)
	}
	d, err := NewSession(context.Background(), 0).Eval(mapCtx(), []string{"output"}, okdp, values("$if: ${{ n > 2.0 }}\na: 1\n"))
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := d.Sections["output"]; !ok || v != nil {
		t.Errorf("$if false at the top of a section's file: got %#v, want a null section", d.Sections)
	}
	if out, err := runOutput("output: {x: 1}\noutput: {x: 2}\n"); err != nil || out["x"] != float64(2) {
		t.Errorf("a top-level key written twice in a file: got %#v (%v), want the later", out, err)
	}
	if _, err := evalOutput(Source{Name: "okdp.yaml", Data: []byte("errors: [a, '', b]\n")}, values("{}\n")); err == nil || err.Error() != "a\nb" {
		t.Errorf("errors: got %v, want the messages as written", err)
	}
}

// TestSourceErrors: an error of a document read from several files starts
// with the name of the file, parse errors included, then its path, without
// the section in a file that is the section; vars, errors and each section
// are given by one file.
func TestSourceErrors(t *testing.T) {
	okdp := func(src string) Source { return Source{Name: "okdp.yaml", Skip: []string{"roles"}, Data: []byte(src)} }
	values := func(src string) Source { return Source{Name: "values.cel.yaml", Section: "output", Data: []byte(src)} }
	for name, tc := range map[string]struct {
		srcs []Source
		want string
	}{
		"same section":       {[]Source{okdp("output: {}\n"), values("{}\n")}, "values.cel.yaml: output: okdp.yaml gives it too: vars, errors and each section are given by one file"},
		"same vars":          {[]Source{okdp("vars: {a: 1}\n"), mapping("vars: {b: 1}\n")}, "values.cel.yaml: vars: okdp.yaml gives it too"},
		"same errors":        {[]Source{mapping("errors: []\n"), okdp("errors: []\n")}, "okdp.yaml: errors: values.cel.yaml gives it too"},
		"skipped elsewhere":  {[]Source{okdp("roles: [x]\n"), mapping("roles: [x]\n")}, "values.cel.yaml: roles: unknown section (allowed: vars, errors, output)"},
		"not a section":      {[]Source{{Name: "x.yaml", Section: "outptu", Data: []byte("{}\n")}}, "x.yaml: outptu: unknown section (allowed: vars, errors, output)"},
		"parse error":        {[]Source{okdp("vars: [\n"), values("{}\n")}, "okdp.yaml: yaml: line 1: did not find expected node content"},
		"alias":              {[]Source{okdp("{}\n"), values("a: &a 1\nb: *a\n")}, "values.cel.yaml: line 2: aliases are not read: bind the value in vars"},
		"not a map":          {[]Source{okdp("[a]\n")}, "okdp.yaml: the mapping must be a YAML map"},
		"directive":          {[]Source{okdp("{}\n"), values("$iff: 1\n")}, "values.cel.yaml: $iff: not allowed here (allowed: $if, $merge)"},
		"in a section file":  {[]Source{okdp("vars: {n: 1}\n"), values("a: [1, '${{ nope }}']\n")}, "values.cel.yaml: a[1]: ${{ nope }}: ERROR: <input>:1:2: undeclared reference to 'nope'"},
		"evaluated, section": {[]Source{okdp("vars: {n: 1}\n"), values("a: {b: '${{ values.nope + n }}'}\n")}, "values.cel.yaml: a: b: ${{ values.nope + n }}: no such key: nope"},
		"evaluated, vars":    {[]Source{okdp("vars: {a: '${{ values.nope }}'}\n"), values("{}\n")}, "okdp.yaml: vars a: ${{ values.nope }}: no such key: nope"},
		"evaluated, errors":  {[]Source{okdp("errors: [x, '${{ values.nope }}']\n"), values("{}\n")}, "okdp.yaml: errors[1]: ${{ values.nope }}: no such key: nope"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := evalOutput(tc.srcs...); err == nil || !strings.HasPrefix(err.Error(), tc.want) {
				t.Errorf("got %v, want %q", err, tc.want)
			}
		})
	}
}

// scanCases: strings and their pieces, for TestScan and FuzzScan.
var scanCases = []struct {
	in   string
	want []piece
}{
	{"plain", []piece{{text: "plain"}}},
	{"${{ a }}", []piece{{text: " a ", expr: true}}},
	{`a ${{ {"k": "}}"}.k }} $${{ b`, []piece{{text: "a "}, {text: ` {"k": "}}"}.k `, expr: true}, {text: " ${{ b"}}},
	{`${{ 'it\'s }}' }}`, []piece{{text: ` 'it\'s }}' `, expr: true}}},
	{"${{ {'a': {'b': 1}} }}x", []piece{{text: " {'a': {'b': 1}} ", expr: true}, {text: "x"}}},
	{"${{ a // a note }}x", []piece{{text: " a // a note ", expr: true}, {text: "x"}}},
}

// unterminated: strings without the }} closing an expression, for TestScan
// and FuzzScan.
var unterminated = []string{"${{ a }", `${{ '\' }}`, "${{ a // b\n }"}

// TestScan: expressions, nested braces, strings, and the $${{ escape.
func TestScan(t *testing.T) {
	for _, tc := range scanCases {
		got, err := scan(tc.in)
		if err != nil {
			t.Errorf("%q: %v", tc.in, err)
			continue
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%q: got %#v, want %#v", tc.in, got, tc.want)
		}
	}
	for _, s := range unterminated {
		if _, err := scan(s); err == nil {
			t.Errorf("%q: an unterminated expression must fail", s)
		}
	}
}

// unscanned: what the scanner does not read as CEL does: a raw or
// triple-quoted string, a comment.
var unscanned = regexp.MustCompile(`[rR]['"]|'''|"""|//`)

// FuzzScan: scan gives a string back whole, its literal text ($${{ written
// again) and the text between each ${{ and the }} that closes it; an
// expression CEL parses is one expression, ${{ <it> }}, whatever its quoted
// strings and braces hold (but with a raw or triple-quoted string, or a
// comment: unscanned). Seeds: the strings of TestScan, and its expressions.
func FuzzScan(f *testing.F) {
	for _, tc := range scanCases {
		f.Add(tc.in)
		for _, p := range tc.want {
			if p.expr {
				f.Add(p.text)
			}
		}
	}
	for _, s := range unterminated {
		f.Add(s)
	}
	env, err := cel.NewEnv()
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if pieces, err := scan(s); err == nil {
			var b strings.Builder
			for _, p := range pieces {
				if p.expr {
					b.WriteString("${{" + p.text + "}}")
				} else {
					b.WriteString(strings.ReplaceAll(p.text, "${{", "$${{"))
				}
			}
			if b.String() != s {
				t.Errorf("%q: scanned as %#v, which reads %q", s, pieces, b.String())
			}
		}
		if _, iss := env.Parse(s); iss.Err() != nil || unscanned.MatchString(s) {
			return
		}
		got, err := scan("${{" + s + "}}")
		if want := []piece{{text: s, expr: true}}; err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("${{%s}}: got %#v (%v), want the expression %q", s, got, err, s)
		}
	})
}

// TestSession: the CEL cost of a compile, shared by the evaluations of its
// documents, and its cancellation, before and during an evaluation.
func TestSession(t *testing.T) {
	one := "vars:\n  a: ${{ values.items.map(i, i.n * 2.0) }}\n"
	two := one + "  b: ${{ values.items.map(i, i.n * 2.0) }}\n"
	// The cost of one binding, measured under a limit it cannot reach.
	s := NewSession(context.Background(), math.MaxUint64)
	if _, err := s.Eval(mapCtx(), nil, mapping(one)); err != nil {
		t.Fatal(err)
	}
	cost := s.used
	if cost == 0 {
		t.Fatal("no cost counted")
	}
	// Enough for one binding, not for two, the second passing the limit (the
	// total checked after each evaluation): in one document, or in two
	// documents of the session.
	limit := 2*cost - 1
	_, err := NewSession(context.Background(), limit).Eval(mapCtx(), nil, mapping(two))
	if want := fmt.Sprintf("values.cel.yaml: vars b: ${{ values.items.map(i, i.n * 2.0) }}: the expressions of the compile exceed its CEL cost limit (%d)", limit); err == nil || err.Error() != want {
		t.Errorf("past the limit: got %v, want %q", err, want)
	}
	s = NewSession(context.Background(), limit)
	if _, err := s.Eval(mapCtx(), nil, mapping(one)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Eval(mapCtx(), nil, mapping(one)); err == nil || !strings.HasSuffix(err.Error(), fmt.Sprintf("exceed its CEL cost limit (%d)", limit)) {
		t.Errorf("past the limit, in a second document: got %v", err)
	}
	if _, err := NewSession(context.Background(), 2*cost).Eval(mapCtx(), nil, mapping(two)); err != nil {
		t.Errorf("within the limit: %v", err)
	}
	// One evaluation past the limit alone: CEL stops it there.
	if _, err := NewSession(context.Background(), cost/2).Eval(mapCtx(), nil, mapping(one)); err == nil || !strings.HasSuffix(err.Error(), fmt.Sprintf("exceed its CEL cost limit (%d)", cost/2)) {
		t.Errorf("past the limit in one evaluation: got %v", err)
	}
	// Cancelled before an evaluation: the context's error.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewSession(ctx, 0).Eval(mapCtx(), nil, mapping(one)); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: got %v, want context.Canceled", err)
	}
	// Cancelled during one: a comprehension stops at its next check.
	items := make([]any, 2*interruptEvery)
	for i := range items {
		items[i] = float64(i)
	}
	// stop cancels the context at its first call, after the check before
	// the evaluation.
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	stop := cel.Function("stop", cel.Overload("stop_dyn", []*cel.Type{cel.DynType}, cel.DynType,
		cel.UnaryBinding(func(v ref.Val) ref.Val { calls++; cancel(); return v })))
	_, err = NewSession(ctx, 0, stop).Eval(M{"values": M{"items": items}}, nil, mapping("vars:\n  a: ${{ values.items.map(i, stop(i)) }}\n"))
	if !errors.Is(err, context.Canceled) || calls > interruptEvery {
		t.Errorf("interrupted: got %v after %d of %d items", err, calls, len(items))
	}
}
