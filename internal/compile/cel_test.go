package compile

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/types/ref"

	"github.com/okdp/okdp-compiler/internal/helmfn"
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
	d, err := NewSession(context.Background(), 0).Load([]byte(src), mapCtx(), "output")
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
	if _, err := runOutput("output: {x: {l: [1, '${{ type(1) }}']}}\n"); err == nil || err.Error() != "output: x: l[1]: ${{ type(1) }}: type conversion not supported for 'type'" {
		t.Errorf("an error at its path: got %v", err)
	}
}

// TestLibraries: the CEL extensions at their pinned versions (libraries):
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

// TestLibraryVersions: the CEL libraries table of the functions spec
// lists the libraries enabled (pinnedLibraries), each at its pinned
// version: a version is raised with the docs.
func TestLibraryVersions(t *testing.T) {
	const spec = "docs/specs/04_functions.md"
	b, err := os.ReadFile(filepath.Join("..", "..", spec))
	if err != nil {
		t.Fatal(err)
	}
	_, section, ok := strings.Cut(string(b), "\n### CEL libraries\n")
	if !ok {
		t.Fatalf("%s: no CEL libraries section", spec)
	}
	section, _, _ = strings.Cut(section, "\n#")
	// The table's rows after its header: the Version column, by the
	// function in the Library column (strings (`ext.Strings`)).
	docs := map[string]string{}
	var header []string
	for line := range strings.Lines(section) {
		if !strings.HasPrefix(line, "|") || strings.HasPrefix(line, "|---") {
			continue
		}
		cells := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		if header == nil {
			header = cells
			continue
		}
		lib, ver := slices.Index(header, "Library"), slices.Index(header, "Version")
		if lib < 0 || ver < 0 || len(cells) <= max(lib, ver) {
			t.Fatalf("%s: a CEL libraries row without a Library and a Version: %q", spec, line)
		}
		_, name, _ := strings.Cut(cells[lib], "`")
		name, _, _ = strings.Cut(name, "`")
		docs[name] = cells[ver]
	}
	for _, l := range pinnedLibraries {
		want := strconv.FormatUint(uint64(l.version), 10)
		if got, ok := docs[l.name]; !ok {
			t.Errorf("%s: %s is not in the CEL libraries table", spec, l.name)
		} else if got != want {
			t.Errorf("%s: %s at version %s, pinned at %s (pinnedLibraries)", spec, l.name, got, want)
		}
		delete(docs, l.name)
	}
	for name := range docs {
		t.Errorf("%s: %s in the CEL libraries table is not enabled (pinnedLibraries)", spec, name)
	}
}

// yaml11Doc: a document of YAML 1.1 scalars and keys, for TestMappingYAML11
// and FuzzMappingReader.
const yaml11Doc = `
let:
  n: 1.0
  y: 2.0
output:
  x:
    bools: [yes, no, on, off, y, N, "yes", 'no', !!str on, true, !!bool yes, !!bool "Off"]
    numbers: [0777, 0x1F, 1_000, 1.0, 012345678901234567890, 0o17]
    strings: [2001-12-14, "0777", 1.2.3, 0o+7, 0o-1_0]
    keys: {on: a, 1.0: b, 0x10: c, "y": d, "<<": e, -1e100: f}
    lets: [{$for: c in values.items, $let: {Y: '${{ c.n + n }}'}, $each: '${{ [y, Y] }}'}]
`

// readerErrors: the documents the reader refuses, and their errors, for
// TestMappingYAML11 and FuzzMappingReader.
var readerErrors = map[string]string{
	"output: {x: {~: 1}}\n":                        `line 1: unsupported map key "~"`,
	"output: {x: {.inf: 1}}\n":                     "line 1: not a finite number: .inf (quote it for the text)",
	"output: {x: {dup: 1, dup: 2}}\n":              `line 1: dup: the key "dup" is written twice`,
	"output: {x: {8: a, 08: b}}\n":                 `line 1: 08: the key "8" is written twice`,
	"output:\n  x:\n    on: a\n    \"true\": b\n":  `line 4: true: the key "true" is written twice`,
	"let: {y: 1, Y: 2}\n":                          `line 1: Y: the key "true" is written twice`,
	"output:\n  x:\n    a: &a {k: 1}\n    b: *a\n": "line 3: anchors, aliases and merge keys are not read: bind the value in let",
	"output: {x: {<<: {k: 1}}}\n":                  "line 1: anchors, aliases and merge keys are not read: bind the value in let",
}

// TestMappingYAML11: the mapping's scalars and keys are read as Helm reads
// values (YAML 1.1), except binding names (let, $let); a key written twice
// fails, two keys Helm reads as one included, and so do anchors, aliases
// and merge keys.
func TestMappingYAML11(t *testing.T) {
	out, err := runOutput(yaml11Doc)
	if err != nil {
		t.Fatal(err)
	}
	want := M{
		"bools":   []any{true, false, true, false, true, false, "yes", "no", "on", true, true, false},
		"numbers": []any{float64(511), float64(31), float64(1000), float64(1), float64(12345678901234567890), float64(15)},
		"strings": []any{"2001-12-14", "0777", "1.2.3", "0o+7", "0o-1_0"},
		"keys":    M{"true": "a", "1": "b", "16": "c", "y": "d", "<<": "e", "-.inf": "f"},
		"lets":    []any{float64(2), float64(2), float64(2), float64(3)},
	}
	if got := value.AsMap(out["x"]); !reflect.DeepEqual(got, want) {
		t.Errorf("got  %#v\nwant %#v", got, want)
	}
	// Found by FuzzMappingReader: a block scalar ending the file keeps its
	// newline, as in Helm.
	if out, err := runOutput("output:\n  x:\n    v: >\n      text"); err != nil || value.AsMap(out["x"])["v"] != "text\n" {
		t.Errorf("a block scalar ending the file: got %#v (%v)", out, err)
	}
	for src, want := range readerErrors {
		if _, err := runOutput(src); err == nil || err.Error() != want {
			t.Errorf("%q: got %v, want %q", src, err, want)
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
		"checksum": helmfn.Sha256Sum(`{"a":["x"],"b":1}`),
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

// TestErrorRules: rules ($if and message), per element ($for); the message
// of a rule that holds no error is not evaluated; what an item may not be.
func TestErrorRules(t *testing.T) {
	_, err := runOutput(`
errors:
  - $if: ${{ values.on }}
    message: on is set
  - $if: ${{ values.off }}
    message: '${{ values.nope }}, not evaluated'
  - $for: c in values.items
    $each:
      $if: ${{ c.n > 1.0 }}
      message: item ${{ c.name }} is too large
`)
	if want := "on is set\nitem b is too large"; err == nil || err.Error() != want {
		t.Errorf("got %v, want %q", err, want)
	}
	const notRule = "a rule is {$if: <condition>, message: <text>}, got "
	for name, tc := range map[string]struct{ src, want string }{
		"not a rule":     {"errors:\n  - {$if: '${{ true }}', msg: x}\n", "errors[0]: " + notRule + `map[string]interface {}{"msg":"x"}`},
		"a list":         {"errors:\n  - '${{ [\"x\"] }}'\n", "errors[0]: " + notRule + `[]interface {}{"x"}`},
		"not text":       {"errors:\n  - {message: '${{ 1 }}'}\n", "errors[0]: " + notRule + `map[string]interface {}{"message":1}`},
		"empty":          {"errors:\n  - {message: ''}\n", "errors[0]: " + notRule + `map[string]interface {}{"message":""}`},
		"message error":  {"errors:\n  - {$if: '${{ false }}', message: x}\n  - {$if: '${{ true }}', message: '${{ nope }}'}\n", "errors[1]: message: ${{ nope }}:"},
		"$each position": {"errors:\n  - {$if: '${{ false }}', message: x}\n  - {$for: c in values.items, $each: {message: '${{ nope }}'}}\n", "errors[1]: message: ${{ nope }}:"},
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
		"errors position":       {"errors:\n  - {$if: '${{ false }}', message: x}\n  - {$if: '${{ nope }}', message: x}\n", "errors[1]: ${{ nope }}:"},
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
		"$else path":            {"output: {x: {m: {$if: '${{ values.off }}', $value: 1, $else: {b: '${{ values.nope }}'}}}}\n", "output: x: m: b: ${{ values.nope }}: no such key: nope"},
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
		"second document":      {"output: {}\n---\nerrors: ['${{ [\"x\"] }}']\n", "line 2: a second YAML document: the mapping is one document"},
		"unknown section":      {"outptu: {}\n", "outptu: unknown section (allowed: let, errors, output)"},
		"untaken branch":       {"output: {x: {m: {$if: '${{ values.off }}', a: '${{ nope }}'}}}\n", "output: x: m: a: ${{ nope }}: ERROR: <input>:1:2: undeclared reference to 'nope'"},
		"under $each":          {"output: {x: {l: [{$for: c in values.items, $each: {$iff: 1}}]}}\n", "output: x: l[0]: $each: $iff: unknown directive"},
		"$for without $each":   {"output: {x: {l: [{$for: c in values.items}]}}\n", "output: x: l[0]: $for: in a list item, needs $each"},
		"$for without $key":    {"output: {x: {m: {$for: c in values.items, $value: 1}}}\n", "output: x: m: $for: in a map, needs $key and $value"},
		"unknown directive":    {"output: {x: {m: {$iff: '${{ true }}', a: 1}}}\n", "output: x: m: $iff: unknown directive (the key $iff is written $$iff)"},
		"unknown in a $for":    {"output: {x: {l: [{$for: c in values.items, $Each: 1}]}}\n", "output: x: l[0]: $Each: unknown directive (the key $Each is written $$Each)"},
		"$each in a map":       {"output: {x: {m: {$each: 1}}}\n", "output: x: m: $each: not in a map, which holds keys, $if, $value (with $else), $for (with $key, $value, $let) and $merge"},
		"$key in a list item":  {"output: {x: {l: [{$key: 1}]}}\n", "output: x: l[0]: $key: not in a list item, which holds keys, $if, $value (with $else), $merge, $for (with $each, $let) and $spread"},
		"$if beside $for":      {"output: {x: {l: [{$if: '${{ true }}', $for: c in values.items, $each: 1}]}}\n", "output: x: l[0]: $if: not in a list item with $for, which holds $for, $each and $let only"},
		"a key beside $spread": {"output: {x: {l: [{$spread: '${{ [] }}', a: 1}]}}\n", "output: x: l[0]: a: not in a list item with $spread, which holds $spread only"},
		"a key beside $value":  {"output: {x: {m: {$value: 1, $merge: '${{ {} }}'}}}\n", "output: x: m: $merge: not in a map with $value, which holds $if, $value and $else only"},
		"$else without $if":    {"output: {x: {m: {$value: 1, $else: 2}}}\n", "output: x: m: $else: needs $if and $value: {$if: ${{ <condition> }}, $value: <value>, $else: <value>}"},
		"$else without $value": {"output: {x: {l: [{$if: '${{ true }}', a: 1, $else: 2}]}}\n", "output: x: l[0]: $else: needs $if and $value"},
		"$else beside $for":    {"output: {x: {m: {$if: '${{ true }}', $for: c in values.items, $key: a, $value: 1, $else: 2}}}\n", "output: x: m: $else: not in a map with $for, which holds $if, $for, $key, $value and $let only"},
		"under $else":          {"output: {x: {m: {$if: '${{ true }}', $value: 1, $else: {$iff: 1}}}}\n", "output: x: m: $else: $iff: unknown directive"},
		"untaken $else":        {"output: {x: {m: {$if: '${{ true }}', $value: 1, $else: '${{ nope }}'}}}\n", "output: x: m: ${{ nope }}: ERROR: <input>:1:2: undeclared reference to 'nope'"},
		"a key beside $for":    {"output: {x: {m: {$for: c in values.items, $key: a, $value: 1, b: 2}}}\n", "output: x: m: b: not in a map with $for, which holds $if, $for, $key, $value and $let only"},
		"literal $if":          {"output: {x: {m: {$if: false, a: 1}}}\n", "output: x: m: $if: takes one expression, ${{ <condition> }}, got false"},
		"interpolated $if":     {"output: {x: {m: {$if: 'x${{ true }}', a: 1}}}\n", `output: x: m: $if: takes one expression, ${{ <condition> }}, got "x${{ true }}"`},
		"literal $merge":       {"output: {x: {m: {$merge: {a: 1}}}}\n", "output: x: m: $merge: takes one expression, ${{ <map> }}, got a map"},
		"literal $spread":      {"output: {x: {l: [{$spread: [1]}]}}\n", "output: x: l[0]: $spread: takes one expression, ${{ <list> }}, got a list"},
		"not evaluated":        {"output: {x: {m: {$if: '${{ false }}', w: [{$iff: 1}]}}}\n", "output: x: m: w[0]: $iff: unknown directive"},
		"$let not a map":       {"output: {x: {l: [{$for: c in values.items, $let: [a], $each: 1}]}}\n", "output: x: l[0]: $let: must be a map of bindings, got a list"},
		"let bound twice":      {"let: {y: 1, \"y\": 2}\n", "let y: bound twice"},
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
			if _, err := NewSession(context.Background(), 0).Load([]byte(tc.src), mapCtx(), "output"); err == nil || !strings.HasPrefix(err.Error(), tc.want) {
				t.Errorf("got %v, want %q", err, tc.want)
			}
		})
	}
}

// TestTemplateErrors: an error of a template's values names its path.
func TestTemplateErrors(t *testing.T) {
	_, err := NewSession(context.Background(), 0).EvalTemplate([]byte("values: {a: [1, '${{ nope }}']}\n"), mapCtx())
	if want := "values: a[1]: ${{ nope }}:"; err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("got %v, want an error containing %q", err, want)
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
	// Found by FuzzScan: raw and triple-quoted strings, a quote in a comment.
	{`${{ r'\' + R"\" + br'\' + "}}" }}`, []piece{{text: ` r'\' + R"\" + br'\' + "}}" `, expr: true}}},
	{`${{ '''it's "}}"''' + """a"b""" }}`, []piece{{text: ` '''it's "}}"''' + """a"b""" `, expr: true}}},
	{"${{\n  // the user's {name}\n  values.name\n}}", []piece{{text: "\n  // the user's {name}\n  values.name\n", expr: true}}},
	{"${{0//\"}}", []piece{{text: "0//\"", expr: true}}},
	{"${{ a // a note }}x", []piece{{text: " a // a note ", expr: true}, {text: "x"}}},
}

// unterminated: strings without the }} closing an expression, for TestScan
// and FuzzScan.
var unterminated = []string{"${{ a }", `${{ '\' }}`, `${{ ar'\' }}`, `${{ '''a'' }}`, "${{ a // b\n }"}

// TestScan: expressions, nested braces in strings (raw and triple-quoted
// ones too) and comments, and the $${{ escape.
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

// FuzzScan: scan gives a string back whole, its literal text ($${{ written
// again) and the text between each ${{ and the }} that closes it; an
// expression CEL parses is one expression, ${{ <it> }}, whatever its
// strings, braces and comments hold (but a } after a comment: a }} in a
// comment closes it). Seeds: the strings of TestScan, and its expressions.
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
		if _, iss := env.Parse(s); iss.Err() != nil {
			return
		}
		if i := strings.Index(s, "//"); i >= 0 && strings.Contains(s[i:], "}") {
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
	one := "let:\n  a: ${{ values.items.map(i, i.n * 2.0) }}\n"
	two := one + "  b: ${{ values.items.map(i, i.n * 2.0) }}\n"
	// The cost of one binding, measured under a limit it cannot reach.
	s := NewSession(context.Background(), math.MaxUint64)
	if _, err := s.Load([]byte(one), mapCtx()); err != nil {
		t.Fatal(err)
	}
	cost := s.used
	if cost == 0 {
		t.Fatal("no cost counted")
	}
	// Enough for one binding, not for two, the second passing the limit
	// while it runs (its compiled program limited to what remains): in one
	// document, or in two documents of the session.
	limit := 2*cost - 1
	_, err := NewSession(context.Background(), limit).Load([]byte(two), mapCtx())
	if want := fmt.Sprintf("let b: ${{ values.items.map(i, i.n * 2.0) }}: the expressions of the compile exceed its CEL cost limit (%d)", limit); err == nil || err.Error() != want {
		t.Errorf("past the limit: got %v, want %q", err, want)
	}
	s = NewSession(context.Background(), limit)
	if _, err := s.Load([]byte(one), mapCtx()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load([]byte(one), mapCtx()); err == nil || !strings.HasSuffix(err.Error(), fmt.Sprintf("exceed its CEL cost limit (%d)", limit)) {
		t.Errorf("past the limit, in a second document: got %v", err)
	}
	if _, err := NewSession(context.Background(), 2*cost).Load([]byte(two), mapCtx()); err != nil {
		t.Errorf("within the limit: %v", err)
	}
	// Cancelled before an evaluation: the context's error.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewSession(ctx, 0).Load([]byte(one), mapCtx()); !errors.Is(err, context.Canceled) {
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
	_, err = NewSession(ctx, 0, stop).Load([]byte("let:\n  a: ${{ values.items.map(i, stop(i)) }}\n"), M{"values": M{"items": items}})
	if !errors.Is(err, context.Canceled) || calls > interruptEvery {
		t.Errorf("interrupted: got %v after %d of %d items", err, calls, len(items))
	}
}

// TestCondition: a condition outside a document, one expression whose value
// is true or false, within the session.
func TestCondition(t *testing.T) {
	s := NewSession(context.Background(), 0)
	for cond, want := range map[string]bool{"${{ values.on }}": true, "${{ values.extra.x > 2.0 }}": false} {
		if got, err := s.Condition(cond, mapCtx()); err != nil || got != want {
			t.Errorf("%s: got %v (%v), want %v", cond, got, err, want)
		}
	}
	for cond, want := range map[string]string{
		"${{ values.extra.k }}":     `${{ values.extra.k }}: must be true or false, got "v"`,
		"${{ null }}":               "${{ null }}: must be true or false, got null",
		"${{ values.items }}":       "${{ values.items }}: must be true or false, got a list",
		"${{ values.nope }}":        "${{ values.nope }}: no such key: nope",
		"true":                      `takes one expression, ${{ <condition> }}, got "true"`,
		"${{ values.on }} and more": `takes one expression, ${{ <condition> }}, got "${{ values.on }} and more"`,
	} {
		if _, err := s.Condition(cond, mapCtx()); err == nil || err.Error() != want {
			t.Errorf("%s: got %v, want %q", cond, err, want)
		}
	}
	s = NewSession(context.Background(), math.MaxUint64)
	if _, err := s.Condition("${{ values.items.all(i, i.n > 0.0) }}", mapCtx()); err != nil || s.used == 0 {
		t.Errorf("cost: %v, %d counted", err, s.used)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewSession(ctx, 0).Condition("${{ values.on }}", mapCtx()); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: got %v, want context.Canceled", err)
	}
}
