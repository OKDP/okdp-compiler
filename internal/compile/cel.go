// Package compile is the "values.yaml with expressions" language: YAML
// shaped like the values it computes, where ${{ <CEL> }} computes a value.
// It knows neither Helm charts nor OKDP: the caller gives a document its
// context and its own host functions (Load), then evaluates the sections it
// knows (Doc.Section), or the values of a template (EvalTemplate).
//
//	key: ${{ expr }}              the whole scalar: the value keeps the expression's type
//	key: text ${{ expr }} text    interpolation: the value is a string
//	$${{                          a literal "${{"
//	key: {$if: ${{ cond }}, ...}  the key is left out when cond is false
//	key: {$if: ..., $value: x}    same, for a value that is not a map
//	key: {$for: c in <expr>, $key: ..., $value: ...}   a map built from a list
//	{$merge: ${{ map }}}          merged into the enclosing map
//	- {$for: c in <expr>, $each: item or [items]}     list items repeated per element
//	$let: {name: ..., ...}        with $for: bindings per element, in order
//	- {$spread: ${{ list }}}      list items spliced in
//	- {$if: ${{ cond }}, ...}     the item is left out when cond is false
//
// Top level, before any section:
//
//	let (ordered bindings)
//	errors (expressions returning lists of messages: any message fails the compile)
//
// A template (EvalTemplate) has one section, values.
package compile

import (
	"fmt"
	"maps"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"github.com/google/cel-go/ext"
	"google.golang.org/protobuf/types/known/structpb"
	"gopkg.in/yaml.v3"

	"github.com/okdp/okdp-compiler/internal/value"
)

// M: a values map (value.M).
type M = value.M

// omap: an ordered YAML map
// A let binding can reference previous ones. Must stay sequential.
type omap struct {
	keys []string
	vals map[string]any
	raw  map[string]string // key -> the key as written
}

// name: key k as written, for the keys that are binding names (let and
// $let): a binding n or y keeps its name, YAML 1.1 does not make it a
// boolean.
func (m *omap) name(k string) string {
	if r, ok := m.raw[k]; ok {
		return r
	}
	return k
}

// fromNode: a YAML node as data, maps as omap to keep their key order.
// Scalars and keys are read as Helm reads values (YAML 1.1, see scalar and
// mapKey): the mapping is YAML shaped like the upstream values, a snippet
// copied from a values.yaml means the same in both.
func fromNode(n *yaml.Node) (any, error) {
	switch n.Kind {
	case yaml.DocumentNode:
		return fromNode(n.Content[0])
	case yaml.MappingNode:
		m := &omap{vals: map[string]any{}, raw: map[string]string{}}
		for i := 0; i < len(n.Content); i += 2 {
			k, err := mapKey(n.Content[i])
			if err != nil {
				return nil, err
			}
			v, err := fromNode(n.Content[i+1])
			if err != nil {
				return nil, err
			}
			// A key given twice: the last value wins, as in Helm values.
			if _, dup := m.vals[k]; !dup {
				m.keys = append(m.keys, k)
				m.raw[k] = n.Content[i].Value
			}
			m.vals[k] = v
		}
		return m, nil
	case yaml.SequenceNode:
		out := []any{}
		for _, c := range n.Content {
			v, err := fromNode(c)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	// A scalar: decoded as YAML 1.1 types (int, bool...), then normalized.
	case yaml.ScalarNode:
		v, err := scalar(n)
		if err != nil {
			return nil, err
		}
		return value.Normalize(v), nil
	case yaml.AliasNode:
		return fromNode(n.Alias)
	}
	return nil, fmt.Errorf("unsupported YAML node at line %d", n.Line)
}

// yaml11Bools: the plain scalars YAML 1.1 (Helm's sigs.k8s.io/yaml) reads
// as booleans and YAML 1.2 (yaml.v3) as strings.
var yaml11Bools = map[string]bool{
	"y": true, "Y": true, "yes": true, "Yes": true, "YES": true, "on": true, "On": true, "ON": true,
	"n": false, "N": false, "no": false, "No": false, "NO": false, "off": false, "Off": false, "OFF": false,
}

// scalar: a scalar node decoded as Helm decodes values (YAML 1.1). yaml.v3
// resolves plain scalars as YAML 1.1 does (ints with 0777, 0x, 0b, _...,
// floats, nulls) except booleans: a plain, untagged y, yes, on, n, no, off
// (and their case variants) is a boolean. Timestamps stay strings, as in
// Helm. Quoted scalars and explicit tags (!!str yes) are left as they are.
func scalar(n *yaml.Node) (any, error) {
	var v any
	if err := n.Decode(&v); err != nil {
		return nil, err
	}
	if _, ok := v.(time.Time); ok {
		return n.Value, nil
	}
	if s, ok := v.(string); ok && n.Style == 0 && n.Tag == "!!str" {
		if b, ok := yaml11Bools[s]; ok {
			return b, nil
		}
	}
	return v, nil
}

// mapKey: a map key as Helm reads it: decoded as a YAML 1.1 scalar, then
// turned into a string as sigs.k8s.io/yaml does (on: is "true", 1.0: is
// "1", 0x10: is "16"). A null or a non-scalar key is an error, as in Helm.
func mapKey(n *yaml.Node) (string, error) {
	if n.Kind == yaml.AliasNode {
		return mapKey(n.Alias)
	}
	if n.Kind != yaml.ScalarNode {
		return "", fmt.Errorf("line %d: a map key must be a scalar", n.Line)
	}
	v, err := scalar(n)
	if err != nil {
		return "", err
	}
	switch t := v.(type) {
	case string:
		return t, nil
	case bool:
		return strconv.FormatBool(t), nil
	case int:
		return strconv.Itoa(t), nil
	case int64:
		return strconv.FormatInt(t, 10), nil
	case float64:
		s := strconv.FormatFloat(t, 'g', -1, 32)
		switch s {
		case "+Inf":
			s = ".inf"
		case "-Inf":
			s = "-.inf"
		case "NaN":
			s = ".nan"
		}
		return s, nil
	}
	return "", fmt.Errorf("line %d: unsupported map key %q", n.Line, n.Value)
}

// celEngine: evaluates a document. It caches the CEL environments, built
// with the caller's host functions.
type celEngine struct {
	envs  map[string]*cel.Env
	funcs []cel.EnvOption
}

// env: the CEL environment for these variable names, cached.
func (e *celEngine) env(names []string) (*cel.Env, error) {
	// One environment per set of variable names, built once.
	sort.Strings(names)
	key := strings.Join(names, ",")
	if env, ok := e.envs[key]; ok {
		return env, nil
	}
	// CEL extensions, then the functions documents may call: the language's,
	// then the caller's.
	opts := []cel.EnvOption{
		ext.Strings(), ext.Lists(), ext.Bindings(), ext.TwoVarComprehensions(), ext.Encoders(), cel.OptionalTypes(),
		cel.Function("str", cel.Overload("str_dyn", []*cel.Type{cel.DynType}, cel.StringType,
			cel.UnaryBinding(func(v ref.Val) ref.Val { return types.String(value.Str(Native(v))) }))),
		cel.Function("first", cel.Overload("first_list", []*cel.Type{cel.ListType(cel.DynType)}, cel.DynType,
			cel.UnaryBinding(func(v ref.Val) ref.Val {
				for _, x := range Native(v).([]any) {
					if value.Truthy(x) {
						return types.DefaultTypeAdapter.NativeToValue(x)
					}
				}
				return types.String("")
			}))),
	}
	opts = append(opts, hostFunctions()...)
	opts = append(opts, e.funcs...)
	// Every variable is dynamic: values are plain YAML data.
	for _, n := range names {
		opts = append(opts, cel.Variable(n, cel.DynType))
	}
	env, err := cel.NewEnv(opts...)
	if err != nil {
		return nil, err
	}
	e.envs[key] = env
	return env, nil
}

// Native: a CEL value as plain Go data (maps, lists, float64, string, bool, nil).
func Native(v ref.Val) any {
	pb, err := v.ConvertToNative(reflect.TypeFor[*structpb.Value]())
	if err != nil {
		return v.Value()
	}
	return pb.(*structpb.Value).AsInterface()
}

// eval: compiles and evaluates one CEL expression in scope.
func (e *celEngine) eval(expr string, scope M) (any, error) {
	names := make([]string, 0, len(scope))
	for k := range scope {
		names = append(names, k)
	}
	env, err := e.env(names)
	if err != nil {
		return nil, err
	}
	ast, iss := env.Compile(expr)
	if iss.Err() != nil {
		return nil, fmt.Errorf("${{ %s }}: %w", strings.TrimSpace(expr), iss.Err())
	}
	prg, err := env.Program(ast)
	if err != nil {
		return nil, err
	}
	out, _, err := prg.Eval(map[string]any(scope))
	if err != nil {
		return nil, fmt.Errorf("${{ %s }}: %w", strings.TrimSpace(expr), err)
	}
	return Native(out), nil
}

// piece: a literal text, or the source of an expression.
type piece struct {
	text string
	expr bool
}

// scan: the ${{ }} expressions of a string (brace depth and quotes tracked,
// so a CEL map literal may contain "}}").
func scan(s string) ([]piece, error) {
	var out []piece
	var lit strings.Builder
	for i := 0; i < len(s); {
		// $${{ is a literal ${{.
		if strings.HasPrefix(s[i:], "$${{") {
			lit.WriteString("${{")
			i += 4
			continue
		}
		if !strings.HasPrefix(s[i:], "${{") {
			lit.WriteByte(s[i])
			i++
			continue
		}
		if lit.Len() > 0 {
			out = append(out, piece{text: lit.String()})
			lit.Reset()
		}
		j := closing(s, i+3)
		if j < 0 {
			return nil, fmt.Errorf("unterminated ${{ in %q", s)
		}
		out = append(out, piece{text: s[i+3 : j], expr: true})
		i = j + 2
	}
	if lit.Len() > 0 {
		out = append(out, piece{text: lit.String()})
	}
	return out, nil
}

// closing: the index of the }} that closes an expression starting at from,
// skipping quoted strings and nested braces, or -1.
func closing(s string, from int) int {
	depth := 0
	var quote byte
	for j := from; j < len(s); j++ {
		c := s[j]
		if quote != 0 {
			switch c {
			case '\\':
				j++
			case quote:
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
		case '{':
			depth++
		case '}':
			if depth == 0 && j+1 < len(s) && s[j+1] == '}' {
				return j
			}
			depth--
		}
	}
	return -1
}

// evalString: a scalar with its ${{ }} expressions evaluated.
func (e *celEngine) evalString(s string, scope M) (any, error) {
	pieces, err := scan(s)
	if err != nil {
		return nil, err
	}
	// A lone expression keeps its type; otherwise the result is text.
	if len(pieces) == 1 && pieces[0].expr {
		return e.eval(pieces[0].text, scope)
	}
	var b strings.Builder
	for _, p := range pieces {
		if !p.expr {
			b.WriteString(p.text)
			continue
		}
		v, err := e.eval(p.text, scope)
		if err != nil {
			return nil, err
		}
		b.WriteString(value.Str(v))
	}
	return b.String(), nil
}

// with: a copy of scope with k bound to v.
func with(scope M, k string, v any) M {
	out := M{}
	maps.Copy(out, scope)
	out[k] = v
	return out
}

// iteration: the scope of one $for element: the loop variable, then the
// $let bindings of the $for, evaluated in order.
func (e *celEngine) iteration(m *omap, scope M, name string, item any) (M, error) {
	s := with(scope, name, item)
	if lets, ok := m.vals["$let"].(*omap); ok {
		for _, k := range lets.keys {
			v, _, err := e.node(lets.vals[k], s)
			if err != nil {
				return nil, atKey("$let "+lets.name(k), err)
			}
			s = with(s, lets.name(k), v)
		}
	}
	return s, nil
}

// loop: "c in <expr>" -> variable, items.
func (e *celEngine) loop(spec any, scope M) (string, []any, error) {
	s := value.Str(spec)
	name, expr, ok := strings.Cut(s, " in ")
	if !ok {
		return "", nil, fmt.Errorf("$for: %q is not \"<name> in <expression>\"", s)
	}
	v, err := e.evalString("${{"+expr+"}}", scope)
	if err != nil {
		return "", nil, err
	}
	items, ok := v.([]any)
	if !ok && v != nil {
		return "", nil, fmt.Errorf("$for %q: not a list", s)
	}
	return strings.TrimSpace(name), items, nil
}

// nodeError: the error of an expression, at its path in the document: the
// section, binding, map keys and list positions down to the expression
// (output: app: env[2]: value).
type nodeError struct {
	path  string
	index bool // the path starts with a list position
	err   error
}

// Error: the path, then the error of the expression.
func (e *nodeError) Error() string { return e.path + ": " + e.err.Error() }

// Unwrap: the error of the expression.
func (e *nodeError) Unwrap() error { return e.err }

// atKey: err, under the key k (a map key, a section, "let <name>").
func atKey(k string, err error) error { return within(k, false, err) }

// atIndex: err, at the position i of a list.
func atIndex(i int, err error) error { return within(fmt.Sprintf("[%d]", i), true, err) }

// within: err, its path below seg: a list position follows the key before
// it without a separator (env[2]), any other step after ": ".
func within(seg string, index bool, err error) error {
	ne, ok := err.(*nodeError)
	switch {
	case !ok:
		return &nodeError{path: seg, index: index, err: err}
	case ne.index:
		return &nodeError{path: seg + ne.path, index: index, err: ne.err}
	}
	return &nodeError{path: seg + ": " + ne.path, index: index, err: ne.err}
}

// node: evaluates a template node. keep=false: the node is left out.
func (e *celEngine) node(n any, scope M) (any, bool, error) {
	switch t := n.(type) {
	case string:
		v, err := e.evalString(t, scope)
		return v, true, err
	case []any:
		out := []any{}
		for i, item := range t {
			// Directives that produce several items: $for and $spread.
			if m, ok := item.(*omap); ok {
				if spec, ok := m.vals["$for"]; ok {
					name, items, err := e.loop(spec, scope)
					if err != nil {
						return nil, false, atIndex(i, err)
					}
					for _, it := range items {
						s, err := e.iteration(m, scope, name, it)
						if err != nil {
							return nil, false, atIndex(i, err)
						}
						v, keep, err := e.node(m.vals["$each"], s)
						if err != nil {
							return nil, false, atIndex(i, err)
						}
						// $each may give one item or a list of items.
						if l, ok := v.([]any); ok {
							out = append(out, l...)
						} else if keep {
							out = append(out, v)
						}
					}
					continue
				}
				if spec, ok := m.vals["$spread"]; ok {
					v, err := e.evalString(value.Str(spec), scope)
					if err != nil {
						return nil, false, atIndex(i, err)
					}
					if l, ok := v.([]any); ok {
						out = append(out, l...)
					}
					continue
				}
			}
			v, keep, err := e.node(item, scope)
			if err != nil {
				return nil, false, atIndex(i, err)
			}
			if keep {
				out = append(out, v)
			}
		}
		return out, true, nil
	case *omap:
		// $if false: the whole node is left out.
		if cond, ok := t.vals["$if"]; ok {
			c, err := e.evalString(value.Str(cond), scope)
			if err != nil {
				return nil, false, err
			}
			if !value.Truthy(c) {
				return nil, false, nil
			}
		}
		// $for in a map: one key per element.
		if spec, ok := t.vals["$for"]; ok {
			name, items, err := e.loop(spec, scope)
			if err != nil {
				return nil, false, err
			}
			out := M{}
			for _, it := range items {
				s, err := e.iteration(t, scope, name, it)
				if err != nil {
					return nil, false, err
				}
				k, _, err := e.node(t.vals["$key"], s)
				if err != nil {
					return nil, false, err
				}
				v, _, err := e.node(t.vals["$value"], s)
				if err != nil {
					return nil, false, err
				}
				out[value.Str(k)] = v
			}
			return out, true, nil
		}
		// $value: the node is that value, not a map.
		if v, ok := t.vals["$value"]; ok {
			return e.node(v, scope)
		}
		// A plain map: each key in order, then $merge on top.
		out := M{}
		for _, k := range t.keys {
			if k == "$if" || k == "$merge" {
				continue
			}
			v, keep, err := e.node(t.vals[k], scope)
			if err != nil {
				return nil, false, atKey(k, err)
			}
			if keep {
				out[k] = v
			}
		}
		if spec, ok := t.vals["$merge"]; ok {
			v, err := e.evalString(value.Str(spec), scope)
			if err != nil {
				return nil, false, err
			}
			out = value.Merge(out, value.AsMap(v))
		}
		return out, true, nil
	}
	return n, true, nil // number, bool, null
}

// parseDoc: a YAML + CEL document (a mapping, a template), its maps
// ordered.
func parseDoc(b []byte) (*omap, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	root, err := fromNode(&doc)
	if err != nil {
		return nil, err
	}
	top, ok := root.(*omap)
	if !ok {
		return nil, fmt.Errorf("the mapping must be a YAML map")
	}
	return top, nil
}

// bind: the scope of a document's sections: the context, then its let
// bindings, in order (each sees the previous ones); then its errors
// section, every message collected before failing.
func (e *celEngine) bind(top *omap, ctx M) (M, error) {
	scope := M{}
	maps.Copy(scope, ctx)
	if lets, ok := top.vals["let"].(*omap); ok {
		for _, k := range lets.keys {
			v, _, err := e.node(lets.vals[k], scope)
			if err != nil {
				return nil, atKey("let "+lets.name(k), err)
			}
			scope[lets.name(k)] = v
		}
	}
	var msgs []string
	for i, x := range value.AsList(top.vals["errors"]) {
		v, _, err := e.node(x, scope)
		if err != nil {
			return nil, atKey("errors", atIndex(i, err))
		}
		for _, m := range value.AsList(v) {
			msgs = append(msgs, value.Str(m))
		}
	}
	if len(msgs) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(msgs, "\n"))
	}
	return scope, nil
}

// Doc: a document (a mapping, a template) bound to its context: its let
// bindings evaluated and its errors section passed.
type Doc struct {
	top   *omap
	e     *celEngine
	scope M
}

// Load: the document b on the context ctx, with the host functions funcs
// besides the language's own (str, first, and those of functions.go): its
// maps read in order as YAML 1.1, its let bindings evaluated in order, then
// its errors section, every message collected before failing.
func Load(b []byte, ctx M, funcs ...cel.EnvOption) (*Doc, error) {
	top, err := parseDoc(b)
	if err != nil {
		return nil, err
	}
	e := &celEngine{envs: map[string]*cel.Env{}, funcs: funcs}
	scope, err := e.bind(top, ctx)
	if err != nil {
		return nil, err
	}
	return &Doc{top: top, e: e, scope: scope}, nil
}

// Section: the top-level key k of the document evaluated, nil when the
// document has none; an error is reported at its path under k (output: app:
// env[2]: ...).
func (d *Doc) Section(k string) (any, error) {
	v, _, err := d.e.node(d.top.vals[k], d.scope)
	if err != nil {
		return nil, atKey(k, err)
	}
	return v, nil
}

// EvalTemplate: a template (YAML + CEL) on the context ctx, with the host
// functions funcs: its let bindings and errors, then its values section.
func EvalTemplate(b []byte, ctx M, funcs ...cel.EnvOption) (M, error) {
	d, err := Load(b, ctx, funcs...)
	if err != nil {
		return nil, err
	}
	v, err := d.Section("values")
	if err != nil {
		return nil, err
	}
	return value.AsMap(v), nil
}

// Eval: one CEL expression (without ${{ }}) on the variables of scope, with
// the host functions funcs.
func Eval(expr string, scope M, funcs ...cel.EnvOption) (any, error) {
	e := &celEngine{envs: map[string]*cel.Env{}, funcs: funcs}
	return e.eval(expr, scope)
}
