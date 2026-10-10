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
//	key: {$if: ..., $value: x, $else: y}   x when cond is true, else y
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
//	errors (rules, {$if: <condition>, message: <text>}: any message fails
//	the compile)
//
// Any other top-level key is a section the caller names. A template
// (Session.EvalTemplate) has one section, values.
package compile

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
	"cel.dev/cel-go/ext"
	"cel.dev/cel-go/interpreter"
	"go.yaml.in/yaml/v3"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/okdp/okdp-compiler/internal/value"
)

// M: a values map (value.M).
type M = value.M

// omap: an ordered YAML map: its entries in the order written, each key
// once, and an index of its keys as Helm reads them (YAML 1.1). The maps
// whose keys are binding names (let, $let) are read by entry: a binding y
// keeps its name, where YAML 1.1 makes it the key "true". Bindings are
// sequential, each seeing the previous ones.
type omap struct {
	entries []entry
	index   map[string]int // key -> its entry
}

// entry: a key of a map, as Helm reads it (key) and as written (name), and
// its value.
type entry struct {
	key, name string
	val       any
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

// fields: the entries of m, in order.
func (m *omap) fields() []entry { return m.entries }

// unread: the error of an anchor, which an alias follows, or of a merge
// key (<<): the reader refuses them rather than reproduce Helm's reading.
const unread = "line %d: anchors, aliases and merge keys are not read: bind the value in let"

// fromNode: a YAML node as data, maps as omap to keep their key order.
// Scalars and keys are read as Helm reads values (YAML 1.1, see scalar and
// mapKey): the mapping is YAML shaped like the upstream values, a snippet
// copied from a values.yaml means the same in both. A key written twice
// fails, two keys Helm reads as one (on and "true") included.
func fromNode(n *yaml.Node) (any, error) {
	if n.Anchor != "" {
		return nil, fmt.Errorf(unread, n.Line)
	}
	switch n.Kind {
	case yaml.DocumentNode:
		return fromNode(n.Content[0])
	case yaml.MappingNode:
		m := &omap{index: map[string]int{}}
		for i := 0; i < len(n.Content); i += 2 {
			kn := n.Content[i]
			k, err := mapKey(kn)
			if err != nil {
				return nil, err
			}
			if _, ok := m.index[k]; ok {
				return nil, fmt.Errorf("line %d: %s: the key %q is written twice", kn.Line, kn.Value, k)
			}
			v, err := fromNode(n.Content[i+1])
			if err != nil {
				return nil, err
			}
			m.index[k] = len(m.entries)
			m.entries = append(m.entries, entry{k, kn.Value, v})
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
	}
	// A scalar: decoded as YAML 1.1 types (int, bool...), then as values hold
	// them: every number a float64, text UTF-8 (a !!binary may not be).
	v, err := scalar(n)
	switch t := v.(type) {
	case int:
		return float64(t), err
	case int64:
		return float64(t), err
	case uint64:
		return float64(t), err
	case string:
		if !utf8.ValidString(t) {
			return nil, fmt.Errorf("a string that is not UTF-8: %q", t)
		}
	}
	return v, err
}

// scalar: a scalar node decoded as Helm decodes values (YAML 1.1). yaml.v3
// resolves plain scalars as YAML 1.1 does (ints with 0777, 0x, 0b, _...,
// floats, nulls) except booleans: a plain, untagged y, yes, on, n, no, off
// (and their case variants) is a boolean; and a signed YAML 1.2 octal,
// 0o+7 or 0o-7, which is text. Timestamps stay strings, as in Helm. Quoted
// scalars and explicit tags (!!str yes) are left as they are, but !!bool,
// which takes the YAML 1.1 booleans too (!!bool yes). .nan and .inf are
// refused: Helm cannot read them back from the final values.
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
	switch t := v.(type) {
	case time.Time:
		return n.Value, nil
	case string:
		if b, ok := value.YAML11Bools[t]; ok && n.Style == 0 && n.Tag == "!!str" {
			return b, nil
		}
	case float64:
		if math.IsNaN(t) || math.IsInf(t, 0) {
			return nil, fmt.Errorf("line %d: not a finite number: %s (quote it for the text)", n.Line, n.Value)
		}
	}
	return v, nil
}

// mapKey: a map key as Helm reads it: decoded as a YAML 1.1 scalar, then
// turned into a string as sigs.k8s.io/yaml does (on: is "true", 1.0: is
// "1", 0x10: is "16"). A null or a non-scalar key is an error, as in Helm.
func mapKey(n *yaml.Node) (string, error) {
	switch {
	case n.Anchor != "" || n.ShortTag() == "!!merge":
		return "", fmt.Errorf(unread, n.Line)
	case n.Kind != yaml.ScalarNode:
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
	case float64: // at float32 precision, as sigs.k8s.io/yaml: 1e100 is .inf
		return strings.NewReplacer("+Inf", ".inf", "-Inf", "-.inf").Replace(strconv.FormatFloat(t, 'g', -1, 32)), nil
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
// the function enabling it, as the CEL libraries table of the functions
// spec names it.
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
			Unary(func(v any) (any, error) { return value.Str(v), nil }))),
		cel.Function("first", cel.Overload("first_list", []*cel.Type{cel.ListType(cel.DynType)}, cel.DynType,
			Unary(func(v any) (any, error) {
				l, ok := v.([]any)
				if !ok {
					return nil, fmt.Errorf("first: the argument must be a list, got %s", value.Short(v))
				}
				for _, x := range l {
					if value.Truthy(x) {
						return x, nil
					}
				}
				return "", nil
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

// toData: a CEL value as data, the values' model, as cel-go converts it to
// JSON (google.protobuf.Value): maps with string keys, []any lists, float64
// numbers, strings, booleans, null. An integer beyond 2^53 is its digits,
// a NaN or infinite number "NaN" or "[-]Infinity", bytes their base64, a
// timestamp or a duration its text (CEL's string()), an optional its value.
// A map key that is not a string, an optional without a value, a type fail.
func toData(v ref.Val) (any, error) {
	pb, err := v.ConvertToNative(types.JSONValueType)
	if err != nil {
		return nil, err
	}
	return pb.(*structpb.Value).AsInterface(), nil
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
		// The value as data: what has no data form fails the expression.
		v, err = toData(out)
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
// section. As in a values file Helm reads, its last line ends with a line
// break: a | or > block scalar ending the file keeps its newline.
func parseDoc(b []byte) (*omap, error) {
	dec := yaml.NewDecoder(strings.NewReader(strings.TrimSuffix(string(b), "\n") + "\n"))
	var doc, next yaml.Node
	if err := dec.Decode(&doc); errors.Is(err, io.EOF) {
		return &omap{}, nil
	} else if err != nil {
		return nil, err
	}
	if err := dec.Decode(&next); err == nil {
		return nil, fmt.Errorf("line %d: a second YAML document: the mapping is one document", next.Line)
	} else if !errors.Is(err, io.EOF) {
		return nil, err
	}
	root, err := fromNode(&doc)
	if err != nil {
		return nil, err
	}
	switch top := root.(type) {
	case nil:
		return &omap{}, nil
	case *omap:
		return top, nil
	}
	return nil, fmt.Errorf("the mapping must be a YAML map")
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
// section, every message collected before failing: each value it gives is
// a rule, {message: <text>}, left out by its $if when it holds no error.
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
			m, _ := v.(M)
			msg, _ := m["message"].(string)
			if len(m) != 1 || msg == "" {
				return nil, atKey("errors", atIndex(i, fmt.Errorf("a rule is {$if: <condition>, message: <text>}, got %s", value.Short(v))))
			}
			msgs = append(msgs, msg)
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
