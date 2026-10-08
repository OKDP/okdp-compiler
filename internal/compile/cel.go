// Package compile is the "values.yaml with expressions" language: YAML
// shaped like the values it computes, where ${{ <CEL> }} computes a value.
// It knows neither Helm charts nor OKDP: the caller opens a session with its
// own host functions (NewSession), loads each document on its context with
// the names of its sections (Session.Load: the document checked and its
// expressions compiled, in every branch), then evaluates them (Doc.Section),
// or the values of a template (Session.EvalTemplate).
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
// Any other top-level key is a section the caller names. A template
// (Session.EvalTemplate) has one section, values.
package compile

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
	"cel.dev/cel-go/common/types/traits"
	"cel.dev/cel-go/ext"
	"cel.dev/cel-go/interpreter"
	"go.yaml.in/yaml/v3"

	"github.com/okdp/okdp-compiler/internal/value"
)

// M: a values map (value.M).
type M = value.M

// omap: an ordered YAML map: its entries as written, every one, and an
// index of its keys as Helm reads them (YAML 1.1), each naming the entry
// that holds its value. Helm's view of the map (fields) has each key once,
// at its first entry. The maps whose keys are binding names (let, $let)
// are read by entry: a binding y or Y keeps its name, YAML 1.1 does not
// make both the key "true". Bindings are sequential, each seeing the
// previous ones.
type omap struct {
	entries []entry
	index   map[string]int // key -> the entry holding its value
}

// entry: a key of a map, as Helm reads it (key) and as written (name, an
// alias's anchor's), the node it was read from, and its value.
type entry struct {
	key, name string
	node      *yaml.Node
	val       any
}

// newOmap: an empty omap.
func newOmap() *omap { return &omap{index: map[string]int{}} }

// put: e added after the entries of m, its value now its key's. A key given
// twice: the last value wins, at the position of the first, as in Helm
// values.
func (m *omap) put(e entry) {
	m.index[e.key] = len(m.entries)
	m.entries = append(m.entries, e)
}

// merge: the entries of o, after those of m, each key of o taking o's
// value: over m's when over (the map of a merge key), else when m does not
// have the key (the maps of a merge key's list, the first winning).
func (m *omap) merge(o *omap, over bool) {
	n := len(m.entries)
	m.entries = append(m.entries, o.entries...)
	for k, i := range o.index {
		if _, ok := m.index[k]; over || !ok {
			m.index[k] = n + i
		}
	}
}

// lookup: the value of the key k, and whether m has it.
func (m *omap) lookup(k string) (any, bool) {
	i, ok := m.index[k]
	if !ok {
		return nil, false
	}
	return m.entries[i].val, true
}

// get: the value of the key k, null when m does not have it.
func (m *omap) get(k string) any {
	v, _ := m.lookup(k)
	return v
}

// fields: m as Helm reads it: each key once, in the order of its first
// entry, as the entry holding its value.
func (m *omap) fields() []entry {
	out := make([]entry, 0, len(m.index))
	seen := make(map[string]bool, len(m.index))
	for _, e := range m.entries {
		if !seen[e.key] {
			seen[e.key] = true
			out = append(out, m.entries[m.index[e.key]])
		}
	}
	return out
}

// clash: an error when m has the key k, read from a node other than kn
// that YAML decodes as another value (1 and 1.0, on and "true"): Helm's
// reader keeps both keys until they are text, then the value of either, at
// random.
func (m *omap) clash(k string, kn *yaml.Node) error {
	i, ok := m.index[k]
	if !ok {
		return nil
	}
	prev := m.entries[i].node
	a, b := aliased(prev), aliased(kn)
	x, _ := scalar(a)
	y, _ := scalar(b)
	if x == y {
		return nil
	}
	return fmt.Errorf("line %d: the keys %s (line %d) and %s are both %q to Helm, which keeps the value of either, at random: write one", kn.Line, asWritten(a), prev.Line, asWritten(b), k)
}

// asWritten: a scalar as written: quoted when it is.
func asWritten(n *yaml.Node) string {
	if n.Style&(yaml.DoubleQuotedStyle|yaml.SingleQuotedStyle) != 0 {
		return strconv.Quote(n.Value)
	}
	return n.Value
}

// aliased: the node an alias refers to, or n.
func aliased(n *yaml.Node) *yaml.Node {
	if n.Kind == yaml.AliasNode {
		return n.Alias
	}
	return n
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
	// A scalar: decoded as YAML 1.1 types (int, bool...), then as values
	// hold them (value.Normalize): every number a float64, text UTF-8 (a
	// !!binary may not be). .nan and .inf are refused: Helm cannot read them
	// back from the final values.
	case yaml.ScalarNode:
		v, err := scalar(n)
		if err != nil {
			return nil, err
		}
		switch t := v.(type) {
		case nil, bool:
			return t, nil
		case string:
			if !utf8.ValidString(t) {
				return nil, fmt.Errorf("a string that is not UTF-8: %q", t)
			}
			return t, nil
		case int:
			return float64(t), nil
		case int64:
			return float64(t), nil
		case uint64:
			return float64(t), nil
		case float64:
			if math.IsNaN(t) || math.IsInf(t, 0) {
				return nil, fmt.Errorf("line %d: not a finite number: %s (quote it for the text)", n.Line, n.Value)
			}
			return t, nil
		}
		return value.Normalize(v)
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
// keep the written one. So do two keys that are the same text, not the
// same YAML value (clash).
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
			for _, f := range merged.fields() {
				if w, ok := keys[f.key]; ok {
					return nil, fmt.Errorf("line %d: %s is written before the merge key << that replaces it: write it after <<", w.Line, w.Value)
				}
				if err := m.clash(f.key, f.node); err != nil {
					return nil, err
				}
			}
			m.merge(merged, true)
			continue
		}
		k, err := mapKey(kn)
		if err != nil {
			return nil, err
		}
		if err := m.clash(k, kn); err != nil {
			return nil, err
		}
		v, err := r.fromNode(vn)
		if err != nil {
			return nil, err
		}
		name := kn.Value
		if kn.Kind == yaml.AliasNode {
			name = kn.Alias.Value
		}
		m.put(entry{k, name, kn, v})
		keys[k] = kn
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
		for _, f := range m.fields() {
			if err := out.clash(f.key, f.node); err != nil {
				return nil, err
			}
		}
		out.merge(m, false)
	}
	return out, nil
}

// scalar: a scalar node decoded as Helm decodes values (YAML 1.1). yaml.v3
// resolves plain scalars as YAML 1.1 does (ints with 0777, 0x, 0b, _...,
// floats, nulls) except booleans: a plain, untagged y, yes, on, n, no, off
// (and their case variants) is a boolean; and a signed YAML 1.2 octal,
// 0o+7 or 0o-7, which is text. Timestamps stay strings, as in Helm. Quoted
// scalars and explicit tags (!!str yes) are left as they are, but !!bool,
// which takes the YAML 1.1 booleans too (!!bool yes).
func scalar(n *yaml.Node) (any, error) {
	if b, ok := value.YAML11Bools[n.Value]; ok && n.ShortTag() == "!!bool" {
		return b, nil
	}
	if t := strings.ReplaceAll(n.Value, "_", ""); n.Style == 0 && n.Tag == "!!int" && (strings.HasPrefix(t, "0o+") || strings.HasPrefix(t, "0o-")) {
		return n.Value, nil
	}
	var v any
	if err := n.Decode(&v); err != nil {
		return nil, err
	}
	if _, ok := v.(time.Time); ok {
		return n.Value, nil
	}
	if s, ok := v.(string); ok && n.Style == 0 && n.Tag == "!!str" {
		if b, ok := value.YAML11Bools[s]; ok {
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

// Session: what the evaluations of one compile share, those of its
// documents (the mapping, the templates) and of its conditions: its
// context, which cancels them; a total CEL cost, when limited; the caller's
// host functions; and the CEL environments, one per set of variable names,
// each built once with those functions, which do not change during the
// session. Not safe for concurrent use: one per compile.
type Session struct {
	ctx   context.Context
	limit uint64 // 0: no limit
	used  uint64 // counted under a limit only
	left  uint64 // under a limit, what the running evaluation may cost: limit - used
	funcs []cel.EnvOption
	progs []cel.ProgramOption // how its programs run (NewSession)
	envs  map[string]*cel.Env // by variable names, sorted (env)
}

// NewSession: the session of a compile: cancelled with ctx (nil: never), its
// expressions' total CEL cost at most limit (0: no limit), its documents
// calling the host functions funcs besides the language's own (str, first,
// and those of functions.go). Its programs, each built once per expression
// (compile), check the context every interruptEvery iterations; under a
// limit, they track their cost against left: CEL's cost tracker of an
// evaluation is a shallow copy of the program's, its Limit pointer shared,
// so a compiled program is limited to what remains when it runs (exec).
func NewSession(ctx context.Context, limit uint64, funcs ...cel.EnvOption) *Session {
	if ctx == nil {
		ctx = context.Background()
	}
	s := &Session{ctx: ctx, limit: limit, funcs: funcs, envs: map[string]*cel.Env{}}
	s.progs = []cel.ProgramOption{cel.InterruptCheckFrequency(interruptEvery)}
	if limit > 0 {
		s.progs = append(s.progs, cel.CostTracking(nil), cel.CostTrackerOptions(func(t *interpreter.CostTracker) error {
			t.Limit = &s.left
			return nil
		}))
	}
	return s
}

// interruptEvery: how many iterations of a comprehension run between two
// checks of the context.
const interruptEvery = 100

// library: a CEL extension of the language at its pinned version; name is
// the function enabling it, as the CEL libraries table of the service
// definition spec names it.
type library struct {
	name    string
	version uint32
	enable  func(version uint32) cel.EnvOption
}

// pinnedLibraries: the CEL extensions of the language, each at a pinned
// version. A cel-go release adds functions to a library under a new
// version, which would otherwise reach every mapping with the upgrade: a
// version is raised on purpose, with the library's functions in the docs
// (TestLibraryVersions compares this table with the docs' one). Those of
// cel-go v0.32.0: strings 5 (the cost of its functions, format's precision
// at most 100), encoders 1 (json.encode, and its cost), optional types 2.
var pinnedLibraries = []library{
	{"ext.Strings", 5, func(v uint32) cel.EnvOption { return ext.Strings(ext.StringsVersion(v)) }},
	{"ext.Lists", 3, func(v uint32) cel.EnvOption { return ext.Lists(ext.ListsVersion(v)) }},
	{"ext.Bindings", 1, func(v uint32) cel.EnvOption { return ext.Bindings(ext.BindingsVersion(v)) }},
	{"ext.TwoVarComprehensions", 0, func(v uint32) cel.EnvOption { return ext.TwoVarComprehensions(ext.TwoVarComprehensionsVersion(v)) }},
	{"ext.Encoders", 1, func(v uint32) cel.EnvOption { return ext.Encoders(ext.EncodersVersion(v)) }},
	{"cel.OptionalTypes", 2, func(v uint32) cel.EnvOption { return cel.OptionalTypes(cel.OptionalTypesVersion(v)) }},
}

// libraries: the options enabling the CEL extensions, at their pinned
// versions (pinnedLibraries).
func libraries() []cel.EnvOption {
	opts := make([]cel.EnvOption, 0, len(pinnedLibraries))
	for _, l := range pinnedLibraries {
		opts = append(opts, l.enable(l.version))
	}
	return opts
}

// env: the CEL environment for these variable names, sorted, cached: one
// per set of names, built once per session.
func (s *Session) env(names []string) (*cel.Env, error) {
	key := strings.Join(names, ",")
	if env, ok := s.envs[key]; ok {
		return env, nil
	}
	// CEL extensions, then the functions documents may call: the language's,
	// then the caller's.
	opts := append(libraries(),
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
	)
	opts = append(opts, hostFunctions()...)
	opts = append(opts, s.funcs...)
	// Every variable is dynamic: values are plain YAML data.
	for _, n := range names {
		opts = append(opts, cel.Variable(n, cel.DynType))
	}
	env, err := cel.NewEnv(opts...)
	if err != nil {
		return nil, err
	}
	s.envs[key] = env
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

// expr: an expression of a document, compiled once, in the environment of
// the names in scope at its place, to the program its evaluations run.
type expr struct {
	src string // as written between ${{ and }}, trimmed, for messages
	prg cel.Program
}

// compile: the expression src, as written (CEL's errors give its line and
// column), compiled in the environment of the variable names, sorted. An
// error names the expression, as an evaluation error does: ${{ <src> }}:
// <error>.
func (s *Session) compile(src string, names []string) (*expr, error) {
	env, err := s.env(names)
	if err != nil {
		return nil, err
	}
	e := &expr{src: strings.TrimSpace(src)}
	ast, iss := env.Compile(src)
	if iss.Err() != nil {
		return nil, fmt.Errorf("${{ %s }}: %w", e.src, iss.Err())
	}
	if e.prg, err = env.Program(ast, s.progs...); err != nil {
		return nil, fmt.Errorf("${{ %s }}: %w", e.src, err)
	}
	return e, nil
}

// eval: one CEL expression, compiled and evaluated in scope.
func (s *Session) eval(src string, scope M) (any, error) {
	e, err := s.compile(src, slices.Sorted(maps.Keys(scope)))
	if err != nil {
		return nil, err
	}
	return s.run(e, scope)
}

// run: the value of the expression e in scope, as data, evaluated within
// the session (exec); an error names the expression.
func (s *Session) run(e *expr, scope M) (any, error) {
	out, err := s.exec(e.prg, scope)
	var v any
	if err == nil {
		// The value as data: what the values cannot hold fails the expression.
		v, err = toData(out, true)
	}
	if err != nil {
		return nil, fmt.Errorf("${{ %s }}: %w", e.src, err)
	}
	return v, nil
}

// exec: the program prg evaluated in scope, within the session: its
// context checked before and during the evaluation; under a limit, the
// program may cost what remains (left), and its cost is counted.
func (s *Session) exec(prg cel.Program, scope M) (ref.Val, error) {
	if err := s.ctx.Err(); err != nil {
		return nil, err
	}
	if s.limit > 0 {
		if s.used >= s.limit {
			return nil, s.exceeded()
		}
		s.left = s.limit - s.used
	}
	out, details, err := prg.ContextEval(s.ctx, scope)
	if details != nil && details.ActualCost() != nil {
		s.used += *details.ActualCost()
	}
	var cancelled interpreter.EvalCancelledError
	switch {
	case err == nil:
		return out, nil
	case errors.As(err, &cancelled) && cancelled.Cause == interpreter.CostLimitExceeded:
		return nil, s.exceeded()
	case s.ctx.Err() != nil:
		// Interrupted (CEL's "operation interrupted"): the context's error.
		return nil, s.ctx.Err()
	}
	return nil, err
}

// exceeded: the error of a compile past its cost limit.
func (s *Session) exceeded() error {
	return fmt.Errorf("the expressions of the compile exceed its CEL cost limit (%d)", s.limit)
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
// or -1. It skips nested braces, and the text CEL does not read as code:
// strings (stringEnd), a "}}" in them included, and // comments to the end
// of the line, their quotes and braces (// it's {x}), but not a }}, which
// closes the expression in a comment too (${{ x // note }}).
func closing(s string, from int) int {
	depth := 0
	for j := from; j < len(s); j++ {
		switch c := s[j]; {
		case c == '\'' || c == '"':
			if j = stringEnd(s, from, j); j < 0 {
				return -1
			}
		case strings.HasPrefix(s[j:], "//"):
			for j += 2; j < len(s) && s[j] != '\n'; j++ {
				if depth == 0 && strings.HasPrefix(s[j:], "}}") {
					return j
				}
			}
		case c == '{':
			depth++
		case c == '}':
			if depth == 0 && strings.HasPrefix(s[j:], "}}") {
				return j
			}
			depth--
		}
	}
	return -1
}

// stringEnd: the index of the last quote of the CEL string whose first
// quote is s[q], or -1: quoted '...' or "...", or triple-quoted, between
// three quotes ("""..."""); a \ escapes the next character, unless the
// string is raw, its quote after an r or R starting a token (r'\d',
// br"\x"), at from or after a character no name holds.
func stringEnd(s string, from, q int) int {
	quote := s[q : q+1]
	if strings.HasPrefix(s[q:], quote+quote+quote) {
		quote += quote + quote
	}
	p := q - 1
	raw := p >= from && (s[p] == 'r' || s[p] == 'R')
	if raw && p > from && (s[p-1] == 'b' || s[p-1] == 'B') {
		p--
	}
	raw = raw && (p == from || !isNameByte(s[p-1]))
	for j := q + len(quote); j < len(s); j++ {
		switch {
		case s[j] == '\\' && !raw:
			j++
		case strings.HasPrefix(s[j:], quote):
			return j + len(quote) - 1
		}
	}
	return -1
}

// isNameByte: c may be part of a CEL name: a letter, a digit or _.
func isNameByte(c byte) bool {
	return c == '_' || '0' <= c && c <= '9' || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z'
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

// parseDoc: a YAML + CEL document (a mapping, a template), its maps
// ordered. It is one YAML document: a second one fails rather than being
// ignored. An empty or null document (comments or --- at most) has no
// section. Its lines are read as Helm reads those of a values file
// (helmLines).
func parseDoc(b []byte) (*omap, error) {
	dec := yaml.NewDecoder(bytes.NewReader(helmLines(b)))
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

// helmLines: b as Helm reads the lines of a values file (Kubernetes'
// YAMLReader, chart.splitDocuments): each one ends with "\n", a "\r"
// before it dropped. So a | or > block scalar ending a file without a final
// line break keeps its newline, and "\r\r\n" is one line break. UTF-16
// text, after its byte order mark, is left as it is.
func helmLines(b []byte) []byte {
	if len(b) == 0 || bytes.HasPrefix(b, []byte{0xfe, 0xff}) || bytes.HasPrefix(b, []byte{0xff, 0xfe}) {
		return b
	}
	var out bytes.Buffer
	for line := range bytes.SplitSeq(bytes.TrimSuffix(b, []byte("\n")), []byte("\n")) {
		out.Write(bytes.TrimSuffix(line, []byte("\r")))
		out.WriteByte('\n')
	}
	return out.Bytes()
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

// Doc: a document (a mapping, a template) loaded in a session: its
// sections built, bound to its context: its let bindings evaluated and its
// errors section passed.
type Doc struct {
	s        *Session
	sections map[string]node // by name, those the document has
	scope    M
}

// Load: the document b on the context scope, in the session s, its
// sections those named: its maps read in order as YAML 1.1, then built
// into a tree (builder.document: its top level, its directives in every
// branch, each expression compiled once in its scope), a top-level key
// other than let, errors and a section failing; then its let bindings
// evaluated in order, then its errors section, every message collected
// before failing.
func (s *Session) Load(b []byte, scope M, sections ...string) (*Doc, error) {
	top, err := parseDoc(b)
	if err != nil {
		return nil, err
	}
	t, err := builder{s}.document(top, slices.Sorted(maps.Keys(scope)), sections)
	if err != nil {
		return nil, err
	}
	bound, err := t.bind(s, scope)
	if err != nil {
		return nil, err
	}
	return &Doc{s: s, sections: t.sections, scope: bound}, nil
}

// bind: the scope of the sections of t: the context vars, then the let
// bindings, in order (each sees the previous ones); then the errors
// section, every message collected before failing (messages).
func (t *tree) bind(s *Session, vars M) (M, error) {
	scope := make(M, len(vars)+len(t.let))
	maps.Copy(scope, vars)
	if err := t.let.bind(s, scope); err != nil {
		return nil, err
	}
	var msgs []string
	for i, it := range t.errors {
		vs, err := it.items(s, scope)
		if err != nil {
			return nil, atKey("errors", atIndex(i, err))
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

// Section: the section k of the document evaluated; nil when the document
// has none (a key Load was not given as a section, it refuses). An error
// is reported at its path under k (output: app: env[2]: ...).
func (d *Doc) Section(k string) (any, error) {
	n := d.sections[k]
	if n == nil {
		return nil, nil
	}
	v, _, err := n.eval(d.s, d.scope)
	if err != nil {
		return nil, atKey(k, err)
	}
	return v, nil
}

// EvalTemplate: a template (YAML + CEL) on the context scope, in the session
// s (Load): its let bindings and errors, then its one section, values.
func (s *Session) EvalTemplate(b []byte, scope M) (M, error) {
	d, err := s.Load(b, scope, "values")
	if err != nil {
		return nil, err
	}
	v, err := d.Section("values")
	if err != nil {
		return nil, err
	}
	return value.AsMap(v), nil
}

// Condition: the condition cond, ${{ <CEL> }} (CheckCondition), compiled
// and evaluated on the context scope in the session s. Its value must be
// true or false: unlike $if, it is not tested as a Helm condition.
func (s *Session) Condition(cond string, scope M) (bool, error) {
	src, err := expression(cond, "condition")
	if err != nil {
		return false, err
	}
	v, err := s.eval(src, scope)
	if err != nil {
		return false, err
	}
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("${{ %s }}: must be true or false, got %s", strings.TrimSpace(src), shape(v))
	}
	return b, nil
}
