// Package compile is the "values.yaml with expressions" language: YAML
// shaped like the values it computes, where ${{ <CEL> }} computes a value.
// It knows neither Helm charts nor OKDP: the caller opens a session with its
// own host functions (NewSession), then evaluates each document, read from
// one file or several (Source), on its context with the names of its
// sections (Session.Eval: the document checked and its expressions
// compiled, in every branch, then its sections evaluated).
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
//	$vars: {name: ..., ...}       with $for: bindings per element, in order
//	- {$spread: ${{ list }}}      list items spliced in
//	- {$if: ${{ cond }}, ...}     the item is left out when cond is false
//
// Top level, before any section:
//
//	vars (ordered bindings)
//	errors (a list of messages, {$if: <condition>, $value: <text>}: any
//	message fails the compile)
//
// Any other top-level key is a section the caller names (Session.Eval), or
// a key another reader reads (Source.Skip). A file may also be one section
// whole (Source.Section).
package compile

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
	"cel.dev/cel-go/ext"
	"go.yaml.in/yaml/v3"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/okdp/okdp-compiler/internal/value"
)

// M: a values map (value.M).
type M = value.M

// omap: an ordered YAML map: its entries in the order written, their keys
// as written, and an index of its keys (the last of a key written twice).
type omap struct {
	entries []entry
	index   map[string]int // key -> its entry
}

// entry: a key of a map and its value.
type entry struct {
	key string
	val any
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

// fromNode: a YAML node as data, maps as omap to keep their key order,
// scalars as yaml.v3 decodes them (YAML 1.2: yes and on are text), then as
// values hold them: every number a float64, text UTF-8 (value.Normalize).
// A timestamp stays text, as in Helm; an alias is refused.
func fromNode(n *yaml.Node) (any, error) {
	switch n.Kind {
	case yaml.DocumentNode:
		return fromNode(n.Content[0])
	case yaml.AliasNode:
		return nil, fmt.Errorf("line %d: aliases are not read: bind the value in vars", n.Line)
	case yaml.MappingNode:
		m := &omap{index: map[string]int{}}
		for i := 0; i < len(n.Content); i += 2 {
			v, err := fromNode(n.Content[i+1])
			if err != nil {
				return nil, err
			}
			m.index[n.Content[i].Value] = len(m.entries)
			m.entries = append(m.entries, entry{n.Content[i].Value, v})
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
	if n.ShortTag() == "!!timestamp" {
		return n.Value, nil
	}
	var v any
	err := n.Decode(&v)
	if err == nil {
		v, err = value.Normalize(v)
	}
	if err != nil {
		return nil, fmt.Errorf("line %d: %w", n.Line, err)
	}
	return v, nil
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
	funcs []cel.EnvOption
	progs []cel.ProgramOption // how its programs run (NewSession)
	envs  map[string]*cel.Env // by variable names, sorted (compile)
}

// NewSession: the session of a compile: cancelled with ctx, its
// expressions' total CEL cost at most limit (0: no limit), its documents
// calling the host functions funcs besides the language's own
// (functions.go). Its programs, each built once per expression
// (compile), check the context every interruptEvery iterations; under a
// limit, each evaluation may cost the limit, its cost counted (exec).
func NewSession(ctx context.Context, limit uint64, funcs ...cel.EnvOption) *Session {
	s := &Session{ctx: ctx, limit: limit, funcs: funcs, envs: map[string]*cel.Env{}}
	s.progs = []cel.ProgramOption{cel.InterruptCheckFrequency(interruptEvery)}
	if limit > 0 {
		s.progs = append(s.progs, cel.CostLimit(limit))
	}
	return s
}

// interruptEvery: how many iterations of a comprehension run between two
// checks of the context.
const interruptEvery = 100

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
// column), compiled in the environment of the variable names, sorted: one
// per set of names, built once per session. An error names the expression,
// as an evaluation error does: ${{ <src> }}: <error>.
func (s *Session) compile(src string, names []string) (*expr, error) {
	key := strings.Join(names, ",")
	env, ok := s.envs[key]
	if !ok {
		// CEL extensions, each at a pinned version: a cel-go release adds
		// functions to a library under a new version, which would otherwise
		// reach every mapping with the upgrade; a version is raised on purpose,
		// with the docs' CEL libraries table. Then the functions documents may
		// call: the language's, then the caller's. Every variable is dynamic:
		// values are plain YAML data.
		opts := append([]cel.EnvOption{ext.Strings(ext.StringsVersion(5)), ext.Lists(ext.ListsVersion(3)), ext.Bindings(ext.BindingsVersion(1)),
			ext.TwoVarComprehensions(ext.TwoVarComprehensionsVersion(0)), ext.Encoders(ext.EncodersVersion(1)), cel.OptionalTypes(cel.OptionalTypesVersion(2))},
			hostFunctions()...)
		opts = append(opts, s.funcs...)
		for _, n := range names {
			opts = append(opts, cel.Variable(n, cel.DynType))
		}
		var err error
		if env, err = cel.NewEnv(opts...); err != nil {
			return nil, err
		}
		s.envs[key] = env
	}
	e := &expr{src: strings.TrimSpace(src)}
	ast, iss := env.Compile(src)
	err := iss.Err()
	if err == nil {
		e.prg, err = env.Program(ast, s.progs...)
	}
	if err != nil {
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
	v, err := s.exec(e.prg, scope)
	if err != nil {
		return nil, fmt.Errorf("${{ %s }}: %w", e.src, err)
	}
	return v, nil
}

// exec: the program prg evaluated in scope, as data, within the session:
// its context checked before and during the evaluation; under a limit, the
// evaluation may cost the limit, and the session fails once the total cost
// of its evaluations passes it.
func (s *Session) exec(prg cel.Program, scope M) (any, error) {
	if err := s.ctx.Err(); err != nil {
		return nil, err
	}
	out, details, err := prg.ContextEval(s.ctx, scope)
	if details != nil && details.ActualCost() != nil {
		s.used += *details.ActualCost()
	}
	switch {
	case s.limit > 0 && s.used > s.limit:
		return nil, fmt.Errorf("the expressions of the compile exceed its CEL cost limit (%d)", s.limit)
	case err != nil && s.ctx.Err() != nil:
		return nil, s.ctx.Err() // interrupted: the context's error
	case err != nil:
		return nil, err
	}
	// The value as data: what has no data form fails the expression.
	return toData(out)
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
// or -1. It skips nested braces, and quoted strings ('...', "...", a \
// escaping the next character), a "}}" in them included.
func closing(s string, from int) int {
	depth := 0
	for j := from; j < len(s); j++ {
		switch c := s[j]; {
		case c == '\'' || c == '"':
			for j++; j < len(s) && s[j] != c; j++ {
				if s[j] == '\\' {
					j++
				}
			}
		case c == '{':
			depth++
		case c == '}' && depth == 0 && strings.HasPrefix(s[j:], "}}"):
			return j
		case c == '}':
			depth--
		}
	}
	return -1
}

// atKey: err, under the key k (a map key, a section, "vars <name>"): its
// path in the document, down to the expression (output: app: env[2]:
// value: ...); a list position follows the key before it without a
// separator.
func atKey(k string, err error) error {
	if strings.HasPrefix(err.Error(), "[") {
		return fmt.Errorf("%s%w", k, err)
	}
	return fmt.Errorf("%s: %w", k, err)
}

// atIndex: err, at the position i of a list.
func atIndex(i int, err error) error { return atKey(fmt.Sprintf("[%d]", i), err) }

// parseDoc: a YAML + CEL file (a mapping, a template, a section), its maps
// ordered: its first YAML document, null when it is empty. As in a values
// file Helm reads, its last line ends with a line break: a | or > block
// scalar ending the file keeps its newline.
func parseDoc(b []byte) (any, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(strings.TrimSuffix(string(b), "\n")+"\n"), &doc); err != nil || doc.Kind == 0 {
		return nil, err
	}
	return fromNode(&doc)
}

// Source: a file of a document (Session.Eval).
type Source struct {
	Name    string   // the file: the first segment of its errors
	Data    []byte   // its YAML
	Section string   // "": its top-level keys are vars, errors and sections; else the whole file is this section
	Skip    []string // top-level keys another reader reads, neither sections nor refused (Section "")
}

// Doc: a document evaluated (Session.Eval).
type Doc struct {
	Sections M                   // the values of its sections, by name, those its sources give
	Written  map[string][]string // the keys each of its sections writes at its top level, by name, whether the section is kept or not ($if): those of its map as written, $$key as $key, not those its $merge or a $for gives
}

// Eval: the document of the sources srcs (a mapping, a template) on the
// context scope, in the session s, its sections those named: each source
// read as YAML 1.2, its maps in order, then the whole built into one tree
// (builder.document: the top level of each source, or the section it is;
// its directives in every branch, each expression compiled once in its
// scope), a top-level key other than vars, errors, a section and those its
// source skips failing, as vars, errors or a section two sources give; then
// its vars bindings evaluated in order, then its errors section, every
// message collected before failing; then its sections, in order, by name,
// those it has, with the keys each writes (Doc.Written). The bindings of
// vars are seen by every source. An error starts with the name of its
// source, then its path, under the section (okdp.yaml: objects[1]: ...)
// but in a source that is the section (values.cel.yaml: app: env[2]: ...).
func (s *Session) Eval(scope M, sections []string, srcs ...Source) (*Doc, error) {
	t, err := builder{s}.document(srcs, slices.Sorted(maps.Keys(scope)), sections)
	if err != nil {
		return nil, err
	}
	if scope, err = t.bind(s, scope); err != nil {
		return nil, err
	}
	d := &Doc{Sections: M{}, Written: t.written}
	for _, k := range sections {
		if n, ok := t.sections[k]; ok {
			if d.Sections[k], _, err = n.eval(s, scope); err != nil {
				return nil, t.at(k, err)
			}
		}
	}
	return d, nil
}

// bind: the scope of the sections of t: the context (outer), then the vars
// bindings, in order (each sees the previous ones); then the errors
// section, a list, every message collected before failing: each item it
// gives that is not empty, as text (value.Str), a message, without the
// name of its file.
func (t *tree) bind(s *Session, outer M) (M, error) {
	scope := make(M, len(outer)+len(t.vars))
	maps.Copy(scope, outer)
	if err := t.vars.bind(s, scope); err != nil {
		return nil, t.at("vars", err)
	}
	rules, _, err := t.errors.eval(s, scope)
	if err != nil {
		return nil, t.at("errors", err)
	}
	var msgs []string
	for _, r := range value.AsList(rules) {
		if msg := value.Str(r); msg != "" {
			msgs = append(msgs, msg)
		}
	}
	if len(msgs) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(msgs, "\n"))
	}
	return scope, nil
}
