// Package compile is the "values.yaml with expressions" language: YAML
// shaped like the values it computes, where ${{ <CEL> }} computes a value.
// It knows neither Helm charts nor OKDP: the caller gives a document its
// context and its own host functions (Load), then evaluates the sections it
// knows (Doc.Section), or the values of a template (EvalTemplate).
//
//	key: ${{ expr }}              the whole scalar: the value keeps the expression's type
//	key: text ${{ expr }} text    interpolation: the value is a string
//	$${{                          a literal "${{"
//	$$key: ...                    a key starting with $, $key
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
//	errors (rules: {$if: <condition>, message: <text>}, or expressions
//	returning lists of messages: any message fails the compile)
//
// A template (EvalTemplate) has one section, values.
package compile

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"github.com/google/cel-go/common/types/traits"
	"github.com/google/cel-go/ext"
	"github.com/google/cel-go/interpreter"
	"gopkg.in/yaml.v3"

	"github.com/okdp/okdp-compiler/internal/value"
)

// M: a values map (value.M).
type M = value.M

// omap: an ordered YAML map: its keys as Helm reads them (YAML 1.1), each
// once, in order; and its entries as written, every one, for the maps whose
// keys are binding names (let, $let): a binding y or Y keeps its name,
// YAML 1.1 does not make both the key "true". Bindings are sequential, each
// seeing the previous ones.
type omap struct {
	keys    []string
	vals    map[string]any
	written []entry
}

// entry: a key of a map as written, and its value.
type entry struct {
	name string
	val  any
}

// newOmap: an empty omap.
func newOmap() *omap { return &omap{vals: map[string]any{}} }

// put: k bound to v. A key given twice: the last value wins, at the
// position of the first, as in Helm values.
func (m *omap) put(k string, v any) {
	if _, dup := m.vals[k]; !dup {
		m.keys = append(m.keys, k)
	}
	m.vals[k] = v
}

// maxAliasNodes: the nodes the aliases of one document may expand to, in
// all. A few hundred bytes of aliases of aliases expand to millions of
// nodes; Helm's reader refuses such excessive aliasing too.
const maxAliasNodes = 100_000

// reader: reads the nodes of one document, its aliases expanded. An alias
// inside the anchor it refers to (a cycle) fails, and so do aliases that
// expand to more than maxAliasNodes nodes in all.
type reader struct {
	open     map[*yaml.Node]bool // the anchored nodes being read
	aliases  int                 // the aliases being expanded
	line     int                 // the line of the outermost of them
	expanded int                 // the nodes they have produced
}

// fromNode: a YAML node as data, maps as omap to keep their key order.
// Scalars and keys are read as Helm reads values (YAML 1.1, see scalar and
// mapKey): the mapping is YAML shaped like the upstream values, a snippet
// copied from a values.yaml means the same in both.
func (r *reader) fromNode(n *yaml.Node) (any, error) {
	if r.aliases > 0 {
		if r.expanded++; r.expanded > maxAliasNodes {
			return nil, fmt.Errorf("line %d: excessive aliasing: the aliases expand to more than %d nodes", r.line, maxAliasNodes)
		}
	}
	if n.Anchor != "" {
		r.open[n] = true
		defer delete(r.open, n)
	}
	switch n.Kind {
	case yaml.DocumentNode:
		return r.fromNode(n.Content[0])
	case yaml.MappingNode:
		return r.mapping(n)
	case yaml.SequenceNode:
		out := []any{}
		for _, c := range n.Content {
			v, err := r.fromNode(c)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	// A scalar: decoded as YAML 1.1 types (int, bool...), then normalized.
	// .nan and .inf are refused: Helm cannot read them back from the final
	// values.
	case yaml.ScalarNode:
		v, err := scalar(n)
		if err != nil {
			return nil, err
		}
		if f, ok := v.(float64); ok && (math.IsNaN(f) || math.IsInf(f, 0)) {
			return nil, fmt.Errorf("line %d: not a finite number: %s (quote it for the text)", n.Line, n.Value)
		}
		return value.Normalize(v), nil
	// An alias: a copy of its anchor's node, unless it is inside it.
	case yaml.AliasNode:
		if r.open[n.Alias] {
			return nil, fmt.Errorf("line %d: the alias *%s is inside the anchor &%s it refers to", n.Line, n.Value, n.Value)
		}
		if r.aliases == 0 {
			r.line = n.Line
		}
		r.aliases++
		defer func() { r.aliases-- }()
		return r.fromNode(n.Alias)
	}
	return nil, fmt.Errorf("unsupported YAML node at line %d", n.Line)
}

// mapping: a map node as an omap. A merge key (<<) merges a map in, as
// Helm's reader does: in the order written, so a key written after <<
// replaces the merged one. A key written before a << that replaces it
// fails: Helm's reader would take the merged value, where YAML's merge keys
// keep the written one.
func (r *reader) mapping(n *yaml.Node) (*omap, error) {
	m := newOmap()
	keys := map[string]*yaml.Node{} // the keys written so far
	for i := 0; i < len(n.Content); i += 2 {
		kn, vn := n.Content[i], n.Content[i+1]
		if kn.Kind == yaml.ScalarNode && kn.Value == "<<" && kn.ShortTag() == "!!merge" {
			merged, err := r.merged(vn)
			if err != nil {
				return nil, err
			}
			for _, k := range merged.keys {
				if w, ok := keys[k]; ok {
					return nil, fmt.Errorf("line %d: %s is written before the merge key << that replaces it: write it after <<", w.Line, w.Value)
				}
				m.put(k, merged.vals[k])
			}
			m.written = append(m.written, merged.written...)
			continue
		}
		k, err := mapKey(kn)
		if err != nil {
			return nil, err
		}
		v, err := r.fromNode(vn)
		if err != nil {
			return nil, err
		}
		m.put(k, v)
		keys[k] = kn
		name := kn.Value
		if kn.Kind == yaml.AliasNode {
			name = kn.Alias.Value
		}
		m.written = append(m.written, entry{name, v})
	}
	return m, nil
}

// merged: the map a merge key merges: a map, an alias of a map, or a list
// of them, the first map of the list winning.
func (r *reader) merged(n *yaml.Node) (*omap, error) {
	items := []*yaml.Node{n}
	if n.Kind == yaml.SequenceNode {
		items = n.Content
	}
	out := newOmap()
	for _, it := range items {
		target := it
		if it.Kind == yaml.AliasNode {
			target = it.Alias
		}
		if target.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("line %d: the merge key << takes a map, or a list of maps", it.Line)
		}
		v, err := r.fromNode(it)
		if err != nil {
			return nil, err
		}
		m := v.(*omap)
		for _, k := range m.keys {
			if _, ok := out.vals[k]; !ok {
				out.put(k, m.vals[k])
			}
		}
		out.written = append(out.written, m.written...)
	}
	return out, nil
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
	envs   map[string]*cel.Env
	funcs  []cel.EnvOption
	budget *Budget // nil: no cancellation, no cost limit
}

// Budget: what the evaluations of one compile may use, shared by its
// documents (the mapping, the templates): its context, which cancels them,
// and a total CEL cost, when limited. Not safe for concurrent use: one per
// compile.
type Budget struct {
	ctx   context.Context
	limit uint64 // 0: no limit
	used  uint64 // counted under a limit only
}

// NewBudget: the budget of a compile: cancelled with ctx (nil: never), its
// expressions' total CEL cost at most limit (0: no limit).
func NewBudget(ctx context.Context, limit uint64) *Budget {
	if ctx == nil {
		ctx = context.Background()
	}
	return &Budget{ctx: ctx, limit: limit}
}

// interruptEvery: how many iterations of a comprehension run between two
// checks of the context.
const interruptEvery = 100

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
			cel.UnaryBinding(func(v ref.Val) ref.Val { return types.String(Text(v)) }))),
		cel.Function("first", cel.Overload("first_list", []*cel.Type{cel.ListType(cel.DynType)}, cel.DynType,
			cel.UnaryBinding(func(v ref.Val) ref.Val {
				l, ok := Native(v).([]any)
				if !ok {
					return types.NewErr("first: the argument must be a list, got %s", value.Short(Native(v)))
				}
				for _, x := range l {
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

// Native: a CEL value as data, as a host function takes it (toData): what
// the values cannot hold is kept or written as text (str prints NaN, a map
// key 1 is "1", a type is its name).
func Native(v ref.Val) any {
	out, _ := toData(v, false)
	return out
}

// toData: a CEL value as data, the values' model: maps with string keys,
// []any lists, float64 numbers (int, uint and double alike), strings,
// booleans, null. An optional is its value, or null when it has none; a
// timestamp or a duration its text (CEL's string()); bytes their text, as
// the host functions take them. strict refuses what the values cannot
// hold, at its position in the value: a NaN or infinite number (Helm cannot
// read it back), a map key that is not a string, bytes that are not UTF-8,
// a type.
func toData(v ref.Val, strict bool) (any, error) {
	switch t := v.(type) {
	case types.Null:
		return nil, nil
	case types.Bool:
		return bool(t), nil
	case types.String:
		return string(t), nil
	case types.Int:
		return float64(t), nil
	case types.Uint:
		return float64(t), nil
	case types.Double:
		f := float64(t)
		if strict && (math.IsNaN(f) || math.IsInf(f, 0)) {
			return nil, fmt.Errorf("not a finite number: %v", f)
		}
		return f, nil
	case types.Bytes:
		if strict && !utf8.Valid(t) {
			return nil, fmt.Errorf("bytes that are not UTF-8 text (encode them with base64.encode)")
		}
		return string(t), nil
	case types.Timestamp, types.Duration:
		if s, ok := v.ConvertToType(types.StringType).(types.String); ok {
			return string(s), nil
		}
	case *types.Optional:
		if !t.HasValue() {
			return nil, nil
		}
		return toData(t.GetValue(), strict)
	case traits.Lister:
		out := []any{}
		for it, i := t.Iterator(), 0; it.HasNext() == types.True; i++ {
			x, err := toData(it.Next(), strict)
			if err != nil {
				return nil, atIndex(i, err)
			}
			out = append(out, x)
		}
		return out, nil
	case traits.Mapper:
		return mapData(t, strict)
	}
	if strict {
		return nil, fmt.Errorf("a CEL %s has no data form", v.Type().TypeName())
	}
	return fmt.Sprint(v.Value()), nil
}

// mapData: a CEL map as data (toData), its keys in order, so that the error
// reported is stable.
func mapData(m traits.Mapper, strict bool) (M, error) {
	type entry struct {
		key  ref.Val
		text string
	}
	var keys []entry
	for it := m.Iterator(); it.HasNext() == types.True; {
		k := it.Next()
		keys = append(keys, entry{k, fmt.Sprint(k.Value())})
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].text < keys[j].text })
	out := M{}
	for _, k := range keys {
		if _, ok := k.key.(types.String); !ok && strict {
			return nil, fmt.Errorf("a map key must be a string, got %s %s", k.key.Type().TypeName(), k.text)
		}
		v, _ := m.Find(k.key)
		x, err := toData(v, strict)
		if err != nil {
			return nil, atKey(k.text, err)
		}
		out[k.text] = x
	}
	return out, nil
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
	out, err := e.run(env, ast, scope)
	if err != nil {
		return nil, fmt.Errorf("${{ %s }}: %w", strings.TrimSpace(expr), err)
	}
	// The value as data: what the values cannot hold fails the expression.
	v, err := toData(out, true)
	if err != nil {
		return nil, fmt.Errorf("${{ %s }}: %w", strings.TrimSpace(expr), err)
	}
	return v, nil
}

// run: a compiled expression evaluated in scope, within the budget: the
// context checked before and during the evaluation, its cost counted
// against the limit.
func (e *celEngine) run(env *cel.Env, ast *cel.Ast, scope M) (ref.Val, error) {
	b := e.budget
	if b == nil {
		prg, err := env.Program(ast)
		if err != nil {
			return nil, err
		}
		out, _, err := prg.Eval(map[string]any(scope))
		return out, err
	}
	if err := b.ctx.Err(); err != nil {
		return nil, err
	}
	opts := []cel.ProgramOption{cel.InterruptCheckFrequency(interruptEvery)}
	if b.limit > 0 {
		if b.used >= b.limit {
			return nil, b.exceeded()
		}
		opts = append(opts, cel.CostLimit(b.limit-b.used))
	}
	prg, err := env.Program(ast, opts...)
	if err != nil {
		return nil, err
	}
	out, details, err := prg.ContextEval(b.ctx, map[string]any(scope))
	if details != nil && details.ActualCost() != nil {
		b.used += *details.ActualCost()
	}
	var cancelled interpreter.EvalCancelledError
	switch {
	case err == nil:
		return out, nil
	case errors.As(err, &cancelled) && cancelled.Cause == interpreter.CostLimitExceeded:
		return nil, b.exceeded()
	case b.ctx.Err() != nil:
		// Interrupted (CEL's "operation interrupted"): the context's error.
		return nil, b.ctx.Err()
	}
	return nil, err
}

// exceeded: the error of a compile past its cost limit.
func (b *Budget) exceeded() error {
	return fmt.Errorf("the expressions of the compile exceed its CEL cost limit (%d)", b.limit)
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
		for _, b := range lets.written {
			v, _, err := e.node(b.val, s)
			if err != nil {
				return nil, atKey("$let "+b.name, err)
			}
			s = with(s, b.name, v)
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

// items: the values the i-th item of a list gives: none ($if false), one,
// or several ($for, $spread); its errors at its position.
func (e *celEngine) items(i int, item any, scope M) ([]any, error) {
	// Directives that produce several items: $for and $spread.
	if m, ok := item.(*omap); ok {
		if spec, ok := m.vals["$for"]; ok {
			name, items, err := e.loop(spec, scope)
			if err != nil {
				return nil, atIndex(i, err)
			}
			var out []any
			for _, it := range items {
				s, err := e.iteration(m, scope, name, it)
				if err != nil {
					return nil, atIndex(i, err)
				}
				v, keep, err := e.node(m.vals["$each"], s)
				if err != nil {
					return nil, atIndex(i, err)
				}
				// $each may give one item or a list of items.
				if l, ok := v.([]any); ok {
					out = append(out, l...)
				} else if keep {
					out = append(out, v)
				}
			}
			return out, nil
		}
		if spec, ok := m.vals["$spread"]; ok {
			v, err := e.evalString(value.Str(spec), scope)
			if err != nil {
				return nil, atIndex(i, err)
			}
			return value.AsList(v), nil
		}
	}
	v, keep, err := e.node(item, scope)
	if err != nil {
		return nil, atIndex(i, err)
	}
	if !keep {
		return nil, nil
	}
	return []any{v}, nil
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
			vs, err := e.items(i, item, scope)
			if err != nil {
				return nil, false, err
			}
			out = append(out, vs...)
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
		// A plain map: each key in order, then $merge on top. A key
		// starting with $$ is written with one $ less.
		out := M{}
		for _, k := range t.keys {
			if k == "$if" || k == "$merge" {
				continue
			}
			v, keep, err := e.node(t.vals[k], scope)
			if err != nil {
				return nil, false, atKey(k, err)
			}
			if !keep {
				continue
			}
			if strings.HasPrefix(k, "$$") {
				k = k[1:]
			}
			out[k] = v
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
// ordered. It is one YAML document: a second one fails rather than being
// ignored. An empty or null document (comments or --- at most) has no
// section.
func parseDoc(b []byte) (*omap, error) {
	dec := yaml.NewDecoder(bytes.NewReader(b))
	var doc, next yaml.Node
	if err := dec.Decode(&doc); errors.Is(err, io.EOF) {
		return newOmap(), nil
	} else if err != nil {
		return nil, err
	}
	if err := dec.Decode(&next); err == nil {
		return nil, fmt.Errorf("line %d: a second YAML document: the mapping is one document", next.Line)
	} else if !errors.Is(err, io.EOF) {
		return nil, err
	}
	r := &reader{open: map[*yaml.Node]bool{}}
	root, err := r.fromNode(&doc)
	if err != nil {
		return nil, err
	}
	if root == nil {
		return newOmap(), nil
	}
	top, ok := root.(*omap)
	if !ok {
		return nil, fmt.Errorf("the mapping must be a YAML map")
	}
	return top, nil
}

// bind: the scope of a document's sections: the context, then its let
// bindings, in order (each sees the previous ones); then its errors
// section, every message collected before failing (messages).
func (e *celEngine) bind(top *omap, ctx M) (M, error) {
	scope := M{}
	maps.Copy(scope, ctx)
	if lets, ok := top.vals["let"].(*omap); ok {
		for _, b := range lets.written {
			v, _, err := e.node(b.val, scope)
			if err != nil {
				return nil, atKey("let "+b.name, err)
			}
			scope[b.name] = v
		}
	}
	var msgs []string
	for i, x := range value.AsList(top.vals["errors"]) {
		vs, err := e.items(i, x, scope)
		if err != nil {
			return nil, atKey("errors", err)
		}
		for _, v := range vs {
			m, err := messages(v)
			if err != nil {
				return nil, atKey("errors", atIndex(i, err))
			}
			msgs = append(msgs, m...)
		}
	}
	if len(msgs) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(msgs, "\n"))
	}
	return scope, nil
}

// messages: what an evaluated item of an errors section reports: a rule,
// {message: <text>} (left out by its $if when it holds no error), gives its
// message; a list gives its messages (an expression returning the messages
// of a check, none when it passes).
func messages(v any) ([]string, error) {
	switch t := v.(type) {
	case []any:
		out := make([]string, 0, len(t))
		for _, m := range t {
			out = append(out, value.Str(m))
		}
		return out, nil
	case M:
		msg, ok := t["message"]
		if !ok || len(t) != 1 {
			break
		}
		s, ok := msg.(string)
		switch {
		case !ok:
			return nil, fmt.Errorf("message must be text, got %s", value.Short(msg))
		case s == "":
			return nil, fmt.Errorf("an empty message")
		}
		return []string{s}, nil
	}
	return nil, fmt.Errorf("a rule is {$if: <condition>, message: <text>}, or an expression returning a list of messages, got %s", value.Short(v))
}

// Doc: a document (a mapping, a template) bound to its context: its let
// bindings evaluated and its errors section passed.
type Doc struct {
	top   *omap
	e     *celEngine
	scope M
}

// Load: the document b on the context ctx, with the host functions funcs
// besides the language's own (str, first, and those of functions.go),
// within budget (nil: none): its maps read in order as YAML 1.1, its top
// level, let and errors checked (checkTop), its let bindings evaluated in
// order, then its errors section, every message collected before failing.
func Load(b []byte, ctx M, budget *Budget, funcs ...cel.EnvOption) (*Doc, error) {
	top, err := parseDoc(b)
	if err != nil {
		return nil, err
	}
	if err := checkTop(top); err != nil {
		return nil, err
	}
	e := &celEngine{envs: map[string]*cel.Env{}, funcs: funcs, budget: budget}
	scope, err := e.bind(top, ctx)
	if err != nil {
		return nil, err
	}
	return &Doc{top: top, e: e, scope: scope}, nil
}

// Section: the top-level key k of the document, its directives checked,
// evaluated; nil when the document has none. An error is reported at its
// path under k (output: app: env[2]: ...).
func (d *Doc) Section(k string) (any, error) {
	if err := check(d.top.vals[k], false); err != nil {
		return nil, atKey(k, err)
	}
	v, _, err := d.e.node(d.top.vals[k], d.scope)
	if err != nil {
		return nil, atKey(k, err)
	}
	return v, nil
}

// EvalTemplate: a template (YAML + CEL) on the context ctx, with the host
// functions funcs, within budget (nil: none): its let bindings and errors,
// then its values section.
func EvalTemplate(b []byte, ctx M, budget *Budget, funcs ...cel.EnvOption) (M, error) {
	d, err := Load(b, ctx, budget, funcs...)
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

// Condition: the condition s, ${{ <CEL> }} (CheckCondition), evaluated on
// the context ctx with the host functions funcs, within budget (nil:
// none). Its value must be true or false: unlike $if, it is not tested as
// a Helm condition.
func Condition(s string, ctx M, budget *Budget, funcs ...cel.EnvOption) (bool, error) {
	if err := CheckCondition(s); err != nil {
		return false, err
	}
	e := &celEngine{envs: map[string]*cel.Env{}, funcs: funcs, budget: budget}
	v, err := e.evalString(s, ctx)
	if err != nil {
		return false, err
	}
	b, ok := v.(bool)
	if !ok {
		p, _ := scan(s)
		return false, fmt.Errorf("${{ %s }}: must be true or false, got %s", strings.TrimSpace(p[0].text), shape(v))
	}
	return b, nil
}
