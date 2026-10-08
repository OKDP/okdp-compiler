// Tree: a document compiled once, when it is loaded (Session.Load), into a
// tree of typed nodes: each map by the directives that give its form, each
// string scanned, each expression compiled in the environment of the names
// in scope at its place. Building the tree is the check of the language: a
// misspelled or misplaced directive, an expression that does not compile,
// fail in every branch, those no evaluation takes included. Evaluation then
// runs the tree, a method per node type, without scanning or compiling.
package compile

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/okdp/okdp-compiler/internal/value"
)

// node: a node of the tree, evaluated on a scope holding the names it was
// built with: its value, keep false when it is left out ($if false).
type node interface {
	eval(s *Session, scope M) (v any, keep bool, err error)
}

// item: a list item, evaluated: the values it gives, none ($if false),
// one, or several ($for, $spread).
type item interface {
	items(s *Session, scope M) ([]any, error)
}

// constant: a scalar without expression: a number, a boolean, null, or a
// string, $${{ read as ${{.
type constant struct{ v any }

// eval: the scalar.
func (c constant) eval(*Session, M) (any, bool, error) { return c.v, true, nil }

// eval: a lone expression, the whole scalar: its value, of its type.
func (e *expr) eval(s *Session, scope M) (any, bool, error) {
	v, err := s.run(e, scope)
	return v, true, err
}

// text: a string of literal parts and expressions, interpolated: each
// expression's value written as text (value.Str).
type text []part

// part: a literal text, or an expression (e).
type part struct {
	lit string
	e   *expr
}

// eval: the text, its expressions evaluated in order.
func (t text) eval(s *Session, scope M) (any, bool, error) {
	var b strings.Builder
	for _, p := range t {
		if p.e == nil {
			b.WriteString(p.lit)
			continue
		}
		v, err := s.run(p.e, scope)
		if err != nil {
			return nil, false, err
		}
		b.WriteString(value.Str(v))
	}
	return b.String(), true, nil
}

// list: a YAML list, its items in order; an error at the position of the
// item as written.
type list []item

// eval: the values of the items, spliced.
func (l list) eval(s *Session, scope M) (any, bool, error) {
	out := []any{}
	for i, it := range l {
		vs, err := it.items(s, scope)
		if err != nil {
			return nil, false, atIndex(i, err)
		}
		out = append(out, vs...)
	}
	return out, true, nil
}

// one: a list item that is one node: its value, none when it is left out.
type one struct{ n node }

// items: the node's value, kept.
func (o one) items(s *Session, scope M) ([]any, error) {
	v, keep, err := o.n.eval(s, scope)
	if err != nil || !keep {
		return nil, err
	}
	return []any{v}, nil
}

// spread: a list item {$spread: ${{ <list> }}}: the items of the list,
// none when the value is not a list.
type spread struct{ e *expr }

// items: the list's items.
func (sp spread) items(s *Session, scope M) ([]any, error) {
	v, err := s.run(sp.e, scope)
	if err != nil {
		return nil, err
	}
	return value.AsList(v), nil
}

// plainMap: a map of keys, each evaluated in order, then the map of its
// $merge, when it has one, merged over them.
type plainMap struct {
	fields []field
	merge  *expr // nil: no $merge
}

// field: a key of a plain map: as read (key, which errors name) and as
// written to the values (out: a key starting with $$ has one $ less), and
// its node.
type field struct {
	key, out string
	n        node
}

// eval: the map: its keys kept, then $merge.
func (m plainMap) eval(s *Session, scope M) (any, bool, error) {
	out := M{}
	for _, f := range m.fields {
		v, keep, err := f.n.eval(s, scope)
		if err != nil {
			return nil, false, atKey(f.key, err)
		}
		if keep {
			out[f.out] = v
		}
	}
	if m.merge != nil {
		v, err := s.run(m.merge, scope)
		if err != nil {
			return nil, false, err
		}
		out = value.Merge(out, value.AsMap(v))
	}
	return out, true, nil
}

// ifNode: a map with $if: left out when its condition is not true (as Helm
// tests a condition), else the map in its form (plain, $for or $value).
type ifNode struct {
	cond *expr
	then node
}

// eval: the condition, then the map.
func (n ifNode) eval(s *Session, scope M) (any, bool, error) {
	c, err := s.run(n.cond, scope)
	if err != nil {
		return nil, false, err
	}
	if !value.Truthy(c) {
		return nil, false, nil
	}
	return n.then.eval(s, scope)
}

// loop: a $for: its variable, the list it runs over (the expression after
// " in "), the spec as written (for messages), and the $let bindings of
// each element.
type loop struct {
	name, spec string
	list       node
	lets       bindings
}

// run: f on the scope of each element of the list, in order: scope, the
// variable bound to the element, then the $let bindings. A null list has
// no element; a value that is not a list fails.
func (l *loop) run(s *Session, scope M, f func(local M) error) error {
	v, _, err := l.list.eval(s, scope)
	if err != nil {
		return err
	}
	elems, ok := v.([]any)
	if !ok && v != nil {
		return fmt.Errorf("$for %q: not a list", l.spec)
	}
	for _, x := range elems {
		local := with(scope, l.name, x)
		if err := l.lets.bind(s, local); err != nil {
			return err
		}
		if err := f(local); err != nil {
			return err
		}
	}
	return nil
}

// with: a copy of scope with k bound to v.
func with(scope M, k string, v any) M {
	out := make(M, len(scope)+1)
	maps.Copy(out, scope)
	out[k] = v
	return out
}

// forMap: a map with $for: one key per element, $key as text, its value
// $value (null when left out); a later element of the same key wins.
type forMap struct {
	loop
	key, val node
}

// eval: the map of the elements.
func (m *forMap) eval(s *Session, scope M) (any, bool, error) {
	out := M{}
	err := m.run(s, scope, func(local M) error {
		k, _, err := m.key.eval(s, local)
		if err != nil {
			return err
		}
		v, _, err := m.val.eval(s, local)
		if err != nil {
			return err
		}
		out[value.Str(k)] = v
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return out, true, nil
}

// forItem: a list item with $for: the items $each gives per element, a
// list spliced, a value left out by $if none.
type forItem struct {
	loop
	each node
}

// items: the items of the elements.
func (it *forItem) items(s *Session, scope M) ([]any, error) {
	var out []any
	err := it.run(s, scope, func(local M) error {
		v, keep, err := it.each.eval(s, local)
		if err != nil {
			return err
		}
		if l, ok := v.([]any); ok {
			out = append(out, l...)
		} else if keep {
			out = append(out, v)
		}
		return nil
	})
	return out, err
}

// bindings: let or $let: named nodes, each built with the names before it
// in scope.
type bindings []binding

// binding: a name, as written, and its node; at, its path in errors
// ("let <name>", "$let <name>").
type binding struct {
	name, at string
	n        node
}

// bind: the bindings evaluated in order, each added to scope, in place, as
// soon as it is evaluated: a value left out by $if binds null.
func (bs bindings) bind(s *Session, scope M) error {
	for _, b := range bs {
		v, _, err := b.n.eval(s, scope)
		if err != nil {
			return atKey(b.at, err)
		}
		scope[b.name] = v
	}
	return nil
}

// tree: a document built: its let bindings, its errors section, and its
// sections by name, those it has.
type tree struct {
	let      bindings
	errors   list
	sections map[string]node
}

// exprError: an error of an expression found when the tree is built, where
// evaluation found it before: a string that does not scan, an expression
// that does not compile, a $for that is not "<name> in <expression>". It
// is reported at the path of evaluation's errors (under).
type exprError struct{ err error }

// Error: the error.
func (e *exprError) Error() string { return e.err.Error() }

// Unwrap: the error.
func (e *exprError) Unwrap() error { return e.err }

// under: err, an error of the node of a directive d ($key, $value, $each):
// a mistake of the language under d; an expression's error (exprError) at
// the map or item holding d, where its evaluation fails.
func under(d string, err error) error {
	if err == nil || errors.As(err, new(*exprError)) {
		return err
	}
	return atKey(d, err)
}

// builder: builds the tree of a document in a session, whose environments
// compile its expressions.
type builder struct{ s *Session }

// document: the tree of the document top, where the names of its context
// (sorted) are in scope, its sections those named: every top-level key is
// let, errors or one of them. let, a map of bindings, is built first, its
// names then in scope of errors, a list, and of the sections, in order.
func (b builder) document(top *omap, names, sections []string) (*tree, error) {
	for _, f := range top.fields() {
		switch k := f.key; {
		case !literal(k):
			return nil, atKey(k, fmt.Errorf("not a directive of the top level: its keys are sections"))
		case k != "let" && k != "errors" && !slices.Contains(sections, k):
			return nil, atKey(k, fmt.Errorf("unknown section (allowed: %s)", strings.Join(append([]string{"let", "errors"}, sections...), ", ")))
		}
	}
	t := &tree{sections: map[string]node{}}
	var err error
	if t.let, names, err = b.bindings("let", top.get("let"), names); err != nil {
		return nil, err
	}
	switch v := top.get("errors").(type) {
	case nil:
	case []any:
		if t.errors, err = b.list(v, names); err != nil {
			return nil, atKey("errors", err)
		}
	default:
		return nil, atKey("errors", fmt.Errorf("must be a list of rules, got %s", shape(v)))
	}
	for _, k := range sections {
		if v, ok := top.lookup(k); ok {
			if t.sections[k], err = b.node(v, names); err != nil {
				return nil, atKey(k, err)
			}
		}
	}
	return t, nil
}

// node: the node of n, a value of the document, where names are in scope:
// a string scanned and compiled (scalar), a list, a map in the form its
// directives give it, another scalar as it is.
func (b builder) node(n any, names []string) (node, error) {
	switch t := n.(type) {
	case string:
		return b.scalar(t, names)
	case []any:
		return b.list(t, names)
	case *omap:
		return b.mapNode(t, inMap, names)
	}
	return constant{n}, nil
}

// mapNode: the node of a map, in the form its directives give it: $for,
// else $value, else plain, at the place p (a map, a list item).
func (b builder) mapNode(m *omap, p *place, names []string) (node, error) {
	switch {
	case has(m, "$for"):
		return b.forMap(m, names)
	case has(m, "$value"):
		return b.valueMap(m, names)
	}
	return b.plainMap(m, p, names)
}

// list: the node of a list, its items built in order; an error at the
// position of its item.
func (b builder) list(l []any, names []string) (list, error) {
	out := make(list, 0, len(l))
	for i, x := range l {
		it, err := b.item(x, names)
		if err != nil {
			return nil, atIndex(i, err)
		}
		out = append(out, it)
	}
	return out, nil
}

// item: a list item: a map with $for, or $spread, gives items; any other
// node is one, a map in the form its directives give it in a list.
func (b builder) item(x any, names []string) (item, error) {
	var n node
	var err error
	switch m, ok := x.(*omap); {
	case ok && has(m, "$for"):
		return b.forItem(m, names)
	case ok && has(m, "$spread"):
		return b.spread(m, names)
	case ok:
		n, err = b.mapNode(m, inItem, names)
	default:
		n, err = b.node(x, names)
	}
	if err != nil {
		return nil, err
	}
	return one{n}, nil
}

// scalar: the node of a string: its expressions scanned once and compiled;
// a lone one is the whole scalar; without any (one piece of text at most,
// as scan joins the text between expressions), the text it reads.
func (b builder) scalar(str string, names []string) (node, error) {
	pieces, err := scan(str)
	switch {
	case err != nil:
		return nil, &exprError{err}
	case len(pieces) == 0:
		return constant{""}, nil
	case len(pieces) == 1 && !pieces[0].expr:
		return constant{pieces[0].text}, nil
	case len(pieces) == 1:
		e, err := b.compile(pieces[0].text, names)
		if err != nil {
			return nil, err
		}
		return e, nil
	}
	t := make(text, len(pieces))
	for i, p := range pieces {
		if !p.expr {
			t[i].lit = p.text
		} else if t[i].e, err = b.compile(p.text, names); err != nil {
			return nil, err
		}
	}
	return t, nil
}

// compile: the expression src, compiled in the environment of names; its
// error is the expression's (exprError).
func (b builder) compile(src string, names []string) (*expr, error) {
	e, err := b.s.compile(src, names)
	if err != nil {
		return nil, &exprError{err}
	}
	return e, nil
}

// plainMap: a map of keys at the place p (a map, a list item): its keys
// built in order, with $if and $merge.
func (b builder) plainMap(m *omap, p *place, names []string) (node, error) {
	if err := p.check(m); err != nil {
		return nil, err
	}
	cond, err := b.cond(m, names)
	if err != nil {
		return nil, err
	}
	var out plainMap
	if has(m, "$merge") {
		if out.merge, err = b.directive(m, "$merge", "map", names); err != nil {
			return nil, err
		}
	}
	for _, f := range m.fields() {
		if !literal(f.key) {
			continue
		}
		n, err := b.node(f.val, names)
		if err != nil {
			return nil, atKey(f.key, err)
		}
		out.fields = append(out.fields, field{f.key, strings.TrimPrefix(f.key, "$"), n})
	}
	return guard(cond, out), nil
}

// valueMap: a map with $value: the node of $value, with $if.
func (b builder) valueMap(m *omap, names []string) (node, error) {
	if err := inValueMap.check(m); err != nil {
		return nil, err
	}
	cond, err := b.cond(m, names)
	if err != nil {
		return nil, err
	}
	n, err := b.node(m.get("$value"), names)
	if err != nil {
		return nil, under("$value", err)
	}
	return guard(cond, n), nil
}

// forMap: a map with $for: its $key and $value built in the scope of an
// element, with $if.
func (b builder) forMap(m *omap, names []string) (node, error) {
	if err := inForMap.check(m); err != nil {
		return nil, err
	}
	if !has(m, "$key") || !has(m, "$value") {
		return nil, atKey("$for", fmt.Errorf("in a map, needs $key and $value"))
	}
	cond, err := b.cond(m, names)
	if err != nil {
		return nil, err
	}
	l, local, err := b.loop(m, names)
	if err != nil {
		return nil, err
	}
	out := &forMap{loop: l}
	if out.key, err = b.node(m.get("$key"), local); err != nil {
		return nil, under("$key", err)
	}
	if out.val, err = b.node(m.get("$value"), local); err != nil {
		return nil, under("$value", err)
	}
	return guard(cond, out), nil
}

// forItem: a list item with $for: its $each built in the scope of an
// element.
func (b builder) forItem(m *omap, names []string) (item, error) {
	if err := inForItem.check(m); err != nil {
		return nil, err
	}
	if !has(m, "$each") {
		return nil, atKey("$for", fmt.Errorf("in a list item, needs $each"))
	}
	l, local, err := b.loop(m, names)
	if err != nil {
		return nil, err
	}
	out := &forItem{loop: l}
	if out.each, err = b.node(m.get("$each"), local); err != nil {
		return nil, under("$each", err)
	}
	return out, nil
}

// spread: a list item with $spread.
func (b builder) spread(m *omap, names []string) (item, error) {
	if err := inSpread.check(m); err != nil {
		return nil, err
	}
	e, err := b.directive(m, "$spread", "list", names)
	if err != nil {
		return nil, err
	}
	return spread{e}, nil
}

// loop: the $for of m, "<name> in <expression>": the expression built
// where names are in scope, the $let bindings after the variable; and the
// names in scope of an element.
func (b builder) loop(m *omap, names []string) (loop, []string, error) {
	spec := value.Str(m.get("$for"))
	name, src, ok := strings.Cut(spec, " in ")
	if !ok {
		return loop{}, nil, &exprError{fmt.Errorf("$for: %q is not \"<name> in <expression>\"", spec)}
	}
	l := loop{name: strings.TrimSpace(name), spec: spec}
	var err error
	if l.list, err = b.scalar("${{"+src+"}}", names); err != nil {
		return loop{}, nil, err
	}
	local := withName(names, l.name)
	if l.lets, local, err = b.bindings("$let", m.get("$let"), local); err != nil {
		return loop{}, nil, err
	}
	return l, local, nil
}

// cond: the condition of m's $if, nil without one.
func (b builder) cond(m *omap, names []string) (*expr, error) {
	if !has(m, "$if") {
		return nil, nil
	}
	return b.directive(m, "$if", "condition", names)
}

// guard: n, left out when cond is not true; n itself without a condition.
func guard(cond *expr, n node) node {
	if cond == nil {
		return n
	}
	return ifNode{cond, n}
}

// directive: the value of the directive d of m ($if, $merge, $spread),
// one expression, ${{ <what> }}, compiled: a value of another form fails
// at d, an expression that does not compile where it is evaluated, at m.
func (b builder) directive(m *omap, d, what string, names []string) (*expr, error) {
	src, err := expression(m.get(d), what)
	if err != nil {
		return nil, atKey(d, err)
	}
	return b.compile(src, names)
}

// bindings: a let or $let map (kind), built: its names as written, each
// once, none starting with $; each value built with the names before it in
// scope. Null has none. The names in scope after them are returned too.
func (b builder) bindings(kind string, v any, names []string) (bindings, []string, error) {
	if v == nil {
		return nil, names, nil
	}
	m, ok := v.(*omap)
	if !ok {
		return nil, nil, atKey(kind, fmt.Errorf("must be a map of bindings, got %s", shape(v)))
	}
	out := make(bindings, 0, len(m.entries))
	seen := map[string]bool{}
	for _, e := range m.entries {
		at := kind + " " + e.name
		switch {
		case strings.HasPrefix(e.name, "$"):
			return nil, nil, atKey(at, fmt.Errorf("a binding name cannot start with $"))
		case seen[e.name]:
			return nil, nil, atKey(at, fmt.Errorf("bound twice"))
		}
		seen[e.name] = true
		n, err := b.node(e.val, names)
		if err != nil {
			return nil, nil, atKey(at, err)
		}
		out = append(out, binding{e.name, at, n})
		names = withName(names, e.name)
	}
	return out, names, nil
}

// withName: names, sorted, with n.
func withName(names []string, n string) []string {
	i, found := slices.BinarySearch(names, n)
	if found {
		return names
	}
	return slices.Insert(slices.Clip(names), i, n)
}

// place: where a map stands, in the form a directive gives it ($for,
// $value, $spread), or plain: the keys it holds there.
type place struct {
	name  string   // for messages
	keys  bool     // it holds keys
	dirs  []string // its directives
	holds string   // what it holds, for messages
}

// The places of a map: a plain map or list item, or one with $for,
// $value, $spread.
var (
	inMap      = &place{"a map", true, []string{"$if", "$merge"}, "keys, $if, $value, $for (with $key, $value, $let) and $merge"}
	inItem     = &place{"a list item", true, []string{"$if", "$merge"}, "keys, $if, $value, $merge, $for (with $each, $let) and $spread"}
	inForMap   = &place{"a map with $for", false, []string{"$if", "$for", "$key", "$value", "$let"}, "$if, $for, $key, $value and $let only"}
	inValueMap = &place{"a map with $value", false, []string{"$if", "$value"}, "$if and $value only"}
	inForItem  = &place{"a list item with $for", false, []string{"$for", "$each", "$let"}, "$for, $each and $let only"}
	inSpread   = &place{"a list item with $spread", false, []string{"$spread"}, "$spread only"}
)

// directives: every directive of the language.
var directives = []string{"$if", "$value", "$for", "$key", "$let", "$each", "$merge", "$spread"}

// check: every key of m is one p holds: a directive of p, or a key when p
// holds keys; another key starting with $ fails, named unknown when it is
// no directive of the language.
func (p *place) check(m *omap) error {
	for _, f := range m.fields() {
		switch k := f.key; {
		case slices.Contains(p.dirs, k), p.keys && literal(k):
		case !literal(k) && !slices.Contains(directives, k):
			return atKey(k, fmt.Errorf("unknown directive (the key %s is written $%s)", k, k))
		default:
			return atKey(k, fmt.Errorf("not in %s, which holds %s", p.name, p.holds))
		}
	}
	return nil
}

// literal: k is a key, not a directive: it does not start with $, or it
// starts with $$ (the key is written with one $ less).
func literal(k string) bool { return !strings.HasPrefix(k, "$") || strings.HasPrefix(k, "$$") }

// has: m holds the key k.
func has(m *omap, k string) bool {
	_, ok := m.index[k]
	return ok
}

// expression: the source of v, one expression, ${{ ... }}, with nothing
// around it (what: what it computes, for messages).
func expression(v any, what string) (string, error) {
	if s, ok := v.(string); ok {
		p, err := scan(s)
		if err != nil {
			return "", err
		}
		if len(p) == 1 && p[0].expr {
			return p[0].text, nil
		}
	}
	return "", fmt.Errorf("takes one expression, ${{ <%s> }}, got %s", what, shape(v))
}

// CheckCondition: v is a condition as $if takes it, one expression,
// ${{ <condition> }}, with nothing around it; for a condition written
// outside a document (Session.Condition).
func CheckCondition(v any) error {
	_, err := expression(v, "condition")
	return err
}

// shape: a node for a message: a map or a list by its kind, null, else the
// scalar as Go's %#v prints it.
func shape(n any) string {
	switch n.(type) {
	case *omap:
		return "a map"
	case []any:
		return "a list"
	case nil:
		return "null"
	}
	return value.Short(n)
}
