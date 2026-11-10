package compile

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/okdp/cel2yaml"

	"github.com/okdp/okdp-compiler/value"
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
	d, err := Eval(cel2yaml.NewSession(context.Background(), 0), mapCtx(), []string{"output"}, srcs...)
	if err != nil {
		return nil, err
	}
	return value.AsMap(d.Sections["output"]), nil
}

// yamlDoc: a document of YAML 1.2 scalars and keys, for TestMappingYAML12.
const yamlDoc = `
vars:
  n: 1.0
  y: 2.0
  y: 3.0
output:
  x:
    vars: [{$for: c in values.items, $vars: {Y: '${{ c.n + n }}'}, $each: '${{ [y, Y] }}'}]
`

// readerErrors: the documents the reader refuses, and their errors, which
// start with the name of the file.
var readerErrors = map[string]string{
	"output:\n  x:\n    a: &a {k: 1}\n    b: *a\n": "values.cel.yaml: line 4: aliases are not read",
	"output: {x: {v: !!bool yes}}\n":               "values.cel.yaml: line 1: yaml: cannot decode !!str `yes` as a !!bool",
	"output: {x: {v: !!binary gIA=}}\n":            "values.cel.yaml: line 1: jsontext: invalid UTF-8",
}

// TestMappingYAML12: the bindings of vars, a name bound twice, the later
// winning, seen by the section; the documents the reader refuses, their
// errors starting with the name of the file.
func TestMappingYAML12(t *testing.T) {
	out, err := runOutput(yamlDoc)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := value.AsMap(out["x"])["vars"], []any{float64(3), float64(2), float64(3), float64(3)}; !reflect.DeepEqual(got, want) {
		t.Errorf("got  %#v\nwant %#v", got, want)
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

// TestMappingErrors: invalid mappings, and where their errors point: the
// errors section, vars, the file.
func TestMappingErrors(t *testing.T) {
	for name, tc := range map[string]struct{ src, want string }{
		"errors position":     {"errors:\n  - {$if: '${{ false }}', $value: x}\n  - {$if: '${{ nope }}', $value: x}\n", "values.cel.yaml: errors[1]: ${{ nope }}:"},
		"not a map":           {"- a\n", "values.cel.yaml: the mapping must be a YAML map"},
		"invalid YAML":        {"output: [\n", "values.cel.yaml: yaml: line 1: did not find expected node content"},
		"vars name":           {"vars: {a: '${{ nope }}'}\n", "values.cel.yaml: vars a:"},
		"vars list position":  {"vars: {a: [1, '${{ nope }}']}\n", "values.cel.yaml: vars a[1]: ${{ nope }}:"},
		"section path":        {"output: {x: {a: {b: '${{ nope }}'}}}\n", "values.cel.yaml: output: x: a: b:"},
		"section list":        {"output: {x: {l: [a, {b: [1, '${{ nope }}']}]}}\n", "values.cel.yaml: output: x: l[1]: b[1]: ${{ nope }}:"},
		"$vars name":          {"output: {x: {l: [{$for: c in values.items, $vars: {d: '${{ nope }}'}, $each: 1}]}}\n", "values.cel.yaml: output: x: l[0]: $vars d: ${{ nope }}:"},
		"scalar without data": {"output: {x: {v: .nan}}\n", "values.cel.yaml: line 1: json: cannot marshal from Go float64: unsupported value: NaN"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := runOutput(tc.src)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

// TestMistakes: mistakes in the top level of a document, and in the
// language under it, fail when the document is loaded, at their path.
func TestMistakes(t *testing.T) {
	for name, tc := range map[string]struct{ src, want string }{
		"unknown section":     {"outptu: {}\n", "values.cel.yaml: outptu: unknown section (allowed: vars, errors, output)"},
		"untaken branch":      {"output: {x: {m: {$if: '${{ values.off }}', a: '${{ nope }}'}}}\n", "values.cel.yaml: output: x: m: a: ${{ nope }}: ERROR: <input>:1:2: undeclared reference to 'nope'"},
		"under $each":         {"output: {x: {l: [{$for: c in values.items, $each: {$iff: 1}}]}}\n", "values.cel.yaml: output: x: l[0]: $each: $iff: not allowed here (allowed: $if, $merge)"},
		"vars not a map":      {"vars: [a]\n", "values.cel.yaml: vars: must be a map of bindings, got a list"},
		"vars a scalar":       {"vars: 5\n", "values.cel.yaml: vars: must be a map of bindings, got 5"},
		"vars value":          {"vars: {a: {$iff: 1}}\n", "values.cel.yaml: vars a: $iff: not allowed here (allowed: $if, $merge)"},
		"errors item":         {"errors: [{$iff: '${{ true }}', $value: x}]\n", "values.cel.yaml: errors[0]: $iff: not allowed here (allowed: $if, $value, $else)"},
		"top-level directive": {"$if: '${{ false }}'\noutput: {}\n", "values.cel.yaml: $if: unknown section (allowed: vars, errors, output)"},
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
	_, err := Eval(cel2yaml.NewSession(context.Background(), 0), mapCtx(), []string{"values"}, Source{Name: "t.cel.yaml", Data: []byte("values: {a: [1, '${{ nope }}']}\n")})
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
	d, err := Eval(cel2yaml.NewSession(context.Background(), 0), mapCtx(), []string{"output"}, okdp, values("$if: ${{ n > 2.0 }}\na: 1\n"))
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

// TestWritten: the keys a section writes at its top level, as written,
// whether it is kept or not: neither its directives nor the keys its
// $merge or a $for gives.
func TestWritten(t *testing.T) {
	values := func(src string) Source { return Source{Name: "values.cel.yaml", Section: "output", Data: []byte(src)} }
	for name, tc := range map[string]struct {
		src  Source
		want []string
	}{
		"keys":          {values("a: 1\n$$b: 2\nc: {$if: '${{ false }}'}\na: 3\n"), []string{"a", "$b", "c"}},
		"left out":      {values("$if: ${{ false }}\na: 1\n$merge: \"${{ {'m': 1} }}\"\n"), []string{"a"}},
		"$for":          {values("{$for: i in values.items, $key: '${{ i.name }}', $value: 1}\n"), nil},
		"$value":        {values("{$value: {a: 1}}\n"), nil},
		"an expression": {values("\"${{ {'a': 1} }}\"\n"), nil},
		"in a mapping":  {mapping("output: {a: 1}\n"), []string{"a"}},
	} {
		t.Run(name, func(t *testing.T) {
			d, err := Eval(cel2yaml.NewSession(context.Background(), 0), mapCtx(), []string{"output"}, tc.src)
			if err != nil {
				t.Fatal(err)
			}
			if got, ok := d.Written["output"]; !ok || !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %#v, want %#v", got, tc.want)
			}
		})
	}
	d, err := Eval(cel2yaml.NewSession(context.Background(), 0), mapCtx(), []string{"output"}, mapping("vars: {}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := d.Written["output"]; ok {
		t.Errorf("a section no source gives: got %#v", d.Written)
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
		"alias":              {[]Source{okdp("{}\n"), values("a: &a 1\nb: *a\n")}, "values.cel.yaml: line 2: aliases are not read"},
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

// TestVarsCost: the CEL cost of a compile is shared by the bindings of vars
// and the sections, and by the documents of the session; an error past the
// limit names the binding.
func TestVarsCost(t *testing.T) {
	one := "vars:\n  a: ${{ values.items.map(i, i.n * 2.0) }}\n"
	two := one + "  b: ${{ values.items.map(i, i.n * 2.0) }}\n"
	s := cel2yaml.NewSession(context.Background(), math.MaxUint64)
	if _, err := Eval(s, mapCtx(), nil, mapping(one)); err != nil {
		t.Fatal(err)
	}
	cost := s.Used()
	if cost == 0 {
		t.Fatal("no cost counted")
	}
	limit := 2*cost - 1
	_, err := Eval(cel2yaml.NewSession(context.Background(), limit), mapCtx(), nil, mapping(two))
	if want := fmt.Sprintf("values.cel.yaml: vars b: ${{ values.items.map(i, i.n * 2.0) }}: the expressions of the compile exceed its CEL cost limit (%d)", limit); err == nil || err.Error() != want {
		t.Errorf("past the limit: got %v, want %q", err, want)
	}
	s = cel2yaml.NewSession(context.Background(), limit)
	if _, err := Eval(s, mapCtx(), nil, mapping(one)); err != nil {
		t.Fatal(err)
	}
	if _, err := Eval(s, mapCtx(), nil, mapping(one)); err == nil || !strings.HasSuffix(err.Error(), fmt.Sprintf("exceed its CEL cost limit (%d)", limit)) {
		t.Errorf("past the limit, in a second document: got %v", err)
	}
	if _, err := Eval(cel2yaml.NewSession(context.Background(), 2*cost), mapCtx(), nil, mapping(two)); err != nil {
		t.Errorf("within the limit: %v", err)
	}
}

// TestPrepared: a document built once, evaluated on several inputs: its
// vars, then its items in order, a text or a path and a message, those
// that are empty left out; a failure to evaluate is an error.
func TestPrepared(t *testing.T) {
	s := cel2yaml.NewSession(context.Background(), 0)
	p, err := Prepare(s, []string{"x"}, nil, Source{Name: "check.yaml", Data: []byte(`
vars:
  big: ${{ x > 1 }}
errors:
  - {$if: "${{ big }}", $value: "big: ${{ x }}"}
  - {$if: "${{ x < 0 }}", path: a.b, message: negative}
  - ""
`)})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		x    any
		want []Problem
	}{
		{1.0, nil},
		{2.0, []Problem{{Message: "big: 2"}}},
		{-1.0, []Problem{{Path: "a.b", Message: "negative"}}},
	} {
		_, got, err := p.Eval(M{"x": tc.x})
		if err != nil || !slices.Equal(got, tc.want) {
			t.Errorf("x %v: got %v (%v), want %v", tc.x, got, err, tc.want)
		}
	}
	if _, _, err := p.Eval(M{"x": "text"}); err == nil || !strings.Contains(err.Error(), "check.yaml: vars big") {
		t.Errorf("a failure to evaluate: got %v", err)
	}
}
