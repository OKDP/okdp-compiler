// Package compile reads a document from one file or several (Source) and
// evaluates it in a cel2yaml session: the language of a mapping (YAML where
// ${{ <CEL> }} computes a value, github.com/okdp/cel2yaml) with its top
// level. It knows neither Helm charts nor OKDP: the caller opens a session
// with its own host functions (cel2yaml.NewSession), then evaluates each
// document on its context with the names of its sections (Eval: the
// document checked and its expressions compiled, in every branch, then its
// sections evaluated).
//
// Top level, before any section:
//
//	vars (ordered bindings)
//	errors (a list of messages, {$if: <condition>, $value: <text>}: any
//	message fails the compile)
//
// Any other top-level key is a section the caller names (Eval), or a key
// another reader reads (Source.Skip). A file may also be one section whole
// (Source.Section).
package compile

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/okdp/cel2yaml"
	"go.yaml.in/yaml/v3"

	"github.com/okdp/okdp-compiler/value"
)

// M: a values map (value.M).
type M = value.M

// Source: a file of a document (Eval).
type Source struct {
	Name    string   // the file: the first segment of its errors
	Data    []byte   // its YAML
	Section string   // "": its top-level keys are vars, errors and sections; else the whole file is this section
	Skip    []string // top-level keys another reader reads, neither sections nor refused (Section "")
}

// Doc: a document evaluated (Eval).
type Doc struct {
	Sections M                   // the values of its sections, by name, those its sources give
	Written  map[string][]string // the keys each of its sections writes at its top level, by name, whether the section is kept or not ($if): those of its map as written, $$key as $key, not those its $merge or a $for gives
}

// document: a document built: its vars bindings, its errors section, and its
// sections by name, those it has, with the keys each writes (literals);
// and the source of each of them, by key (vars, errors, a section), whose
// errors start with its name (at).
type document struct {
	vars     []binding
	errors   *cel2yaml.Template
	sections map[string]*cel2yaml.Template
	written  map[string][]string
	srcs     map[string]Source
}

// binding: a name of vars, as written, and its template; at, its path in
// errors ("vars <name>").
type binding struct {
	name, at string
	t        *cel2yaml.Template
}

// Eval: the document of the sources srcs (a mapping, a template) on the
// context scope, in the session s, its sections those named: each source
// read as YAML 1.2, its maps in order, then the whole built (build: the top
// level of each source, or the section it is; its directives in every
// branch, each expression compiled once in its scope), a top-level key
// other than vars, errors, a section and those its source skips failing, as
// vars, errors or a section two sources give; then its vars bindings
// evaluated in order, then its errors section, every message collected
// before failing; then its sections, in order, by name, those it has, with
// the keys each writes (Doc.Written). The bindings of vars are seen by every
// source. An error starts with the name of its source, then its path, under
// the section (okdp.yaml: objects[1]: ...) but in a source that is the
// section (values.cel.yaml: app: env[2]: ...). The evaluations are one
// document of the session (cel2yaml.Session.Document).
func Eval(ctx context.Context, s *cel2yaml.Session, scope M, sections []string, srcs ...Source) (*Doc, error) {
	var d *Doc
	err := s.Document(func() error {
		t, err := build(s, srcs, slices.Sorted(maps.Keys(scope)), sections)
		if err != nil {
			return err
		}
		if scope, err = t.bind(ctx, scope); err != nil {
			return err
		}
		d = &Doc{Sections: M{}, Written: t.written}
		for _, k := range sections {
			if n, ok := t.sections[k]; ok {
				v, _, err := n.Eval(ctx, scope)
				if err != nil {
					return t.at(k, err)
				}
				d.Sections[k] = v
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return d, nil
}

// Prepared: a document of vars and errors built once (Prepare), evaluated on
// many inputs, in its session: not safe for concurrent use.
type Prepared struct {
	s        *cel2yaml.Session
	t        *document
	sections []string
}

// Problem: an item of the errors of a document (Prepared.Problems): a
// message, and the path of the field it is about, when the item names one.
type Problem struct{ Path, Message string }

// Prepare: the document of srcs, its vars, its errors and the sections
// named, its expressions compiled once with the names in scope.
func Prepare(s *cel2yaml.Session, names, sections []string, srcs ...Source) (*Prepared, error) {
	t, err := build(s, srcs, slices.Sorted(slices.Values(names)), sections)
	if err != nil {
		return nil, err
	}
	return &Prepared{s, t, sections}, nil
}

// Eval: the document on scope: the items of its errors, in order, those
// that are not empty, a text or a map of path and message; without any, the
// values of its sections, by name. A failure to evaluate it is an error,
// not a problem. It may be called while another document of the session is
// being evaluated, by a host function. Cancelled with ctx.
func (p *Prepared) Eval(ctx context.Context, scope M) (M, []Problem, error) {
	var out []Problem
	sections := M{}
	err := p.s.Document(func() error {
		bound, err := p.t.vars2scope(ctx, scope)
		if err != nil {
			return err
		}
		items, _, err := p.t.errors.Eval(ctx, bound)
		if err != nil {
			return p.t.at("errors", err)
		}
		for _, i := range value.AsList(items) {
			if m, ok := i.(M); ok {
				out = append(out, Problem{Path: value.Str(m["path"]), Message: value.Str(m["message"])})
			} else if msg := value.Str(i); msg != "" {
				out = append(out, Problem{Message: msg})
			}
		}
		if len(out) > 0 {
			return nil
		}
		for _, k := range p.sections {
			if n, ok := p.t.sections[k]; ok {
				v, _, err := n.Eval(ctx, bound)
				if err != nil {
					return p.t.at(k, err)
				}
				sections[k] = v
			}
		}
		return nil
	})
	return sections, out, err
}

// bind: the scope of the sections of t: the context (outer), then the vars
// bindings, in order (each sees the previous ones); then the errors
// section, a list, every message collected before failing: each item it
// gives that is not empty, as text (value.Str), a message, without the
// name of its file (a Refusal each, joined).
func (t *document) bind(ctx context.Context, outer M) (M, error) {
	scope, err := t.vars2scope(ctx, outer)
	if err != nil {
		return nil, err
	}
	rules, _, err := t.errors.Eval(ctx, scope)
	if err != nil {
		return nil, t.at("errors", err)
	}
	var refused []error
	for _, r := range value.AsList(rules) {
		if msg := value.Str(r); msg != "" {
			refused = append(refused, Refusal{msg})
		}
	}
	if len(refused) > 0 {
		return nil, errors.Join(refused...)
	}
	return scope, nil
}

// Refusal: a message of the errors section of a document (Eval, joined with
// errors.Join when it gives several, one a line).
type Refusal struct{ Message string }

// Error: the message.
func (r Refusal) Error() string { return r.Message }

// vars2scope: the context outer, then the vars bindings, in order, each
// seeing the previous ones.
func (t *document) vars2scope(ctx context.Context, outer M) (M, error) {
	scope := make(M, len(outer)+len(t.vars))
	maps.Copy(scope, outer)
	for _, b := range t.vars {
		v, _, err := b.t.Eval(ctx, scope)
		if err != nil {
			return nil, t.at("vars", cel2yaml.AtKey(b.at, err))
		}
		scope[b.name] = v
	}
	return scope, nil
}

// at: err, an error of k (vars, errors, a section): under k, but for vars,
// whose errors name their binding, and for a source that is the section k;
// then under the name of its source.
func (t *document) at(k string, err error) error {
	src := t.srcs[k]
	if k != "vars" && src.Section == "" {
		err = cel2yaml.AtKey(k, err)
	}
	return fmt.Errorf("%s: %w", src.Name, err)
}

// build: the document of the sources srcs, where the names of its context
// (sorted) are in scope, its sections those named. A source that is a
// section gives it whole; the top level of another is a map, each key vars,
// errors, one of the sections or a key it skips, the later of a key written
// twice winning. vars, errors and each section are given by one source at
// most. vars, a map of bindings, is built first, its names then in scope of
// errors and of the sections, in order, each with the keys it writes
// (literals).
func build(s *cel2yaml.Session, srcs []Source, names, sections []string) (*document, error) {
	allowed := append([]string{"vars", "errors"}, sections...)
	t := &document{sections: map[string]*cel2yaml.Template{}, written: map[string][]string{}, srcs: map[string]Source{}}
	read := map[string]*yaml.Node{} // vars, errors, a section: as read
	from := map[string]int{}        // its source, by position
	for i, src := range srcs {
		root, err := cel2yaml.Parse(src.Data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", src.Name, err)
		}
		var keys []*yaml.Node // key, value, key, value...
		switch {
		case src.Section != "":
			keys = []*yaml.Node{{Kind: yaml.ScalarNode, Value: src.Section}, root}
		case isNull(root):
		case root.Kind == yaml.MappingNode:
			keys = root.Content
		default:
			return nil, fmt.Errorf("%s: the mapping must be a YAML map", src.Name)
		}
		for j := 0; j < len(keys); j += 2 {
			key, val := keys[j].Value, keys[j+1]
			j2, given := from[key]
			switch {
			case src.Section == "" && slices.Contains(src.Skip, key):
				continue
			case !slices.Contains(allowed, key):
				return nil, fmt.Errorf("%s: %s: unknown section (allowed: %s)", src.Name, key, strings.Join(allowed, ", "))
			case given && j2 != i:
				return nil, fmt.Errorf("%s: %s: %s gives it too: vars, errors and each section are given by one file", src.Name, key, srcs[j2].Name)
			}
			read[key], from[key], t.srcs[key] = val, i, src
		}
	}
	var err error
	if t.vars, names, err = bindings(s, read["vars"], names); err != nil {
		return nil, t.at("vars", err)
	}
	if t.errors, err = s.Template(read["errors"], names); err != nil {
		return nil, t.at("errors", err)
	}
	for _, k := range sections {
		if v, ok := read[k]; ok {
			if t.sections[k], err = s.Template(v, names); err != nil {
				return nil, t.at(k, err)
			}
			t.written[k] = literals(v)
		}
	}
	return t, nil
}

// bindings: the vars map v, built: its names as written (a name bound
// twice, y and "y", the later wins), each value compiled with the names
// before it in scope. Null has none. The names in scope after them are
// returned too.
func bindings(s *cel2yaml.Session, v *yaml.Node, names []string) ([]binding, []string, error) {
	if isNull(v) {
		return nil, names, nil
	}
	if v.Kind != yaml.MappingNode {
		d, err := cel2yaml.Data(v)
		if err != nil {
			return nil, nil, err
		}
		return nil, nil, cel2yaml.AtKey("vars", fmt.Errorf("must be a map of bindings, got %s", cel2yaml.Shape(d)))
	}
	out := make([]binding, 0, len(v.Content)/2)
	for i := 0; i < len(v.Content); i += 2 {
		name := v.Content[i].Value
		at := "vars " + name
		t, err := s.Template(v.Content[i+1], names)
		if err != nil {
			return nil, nil, cel2yaml.AtKey(at, err)
		}
		out = append(out, binding{name, at, t})
		names = cel2yaml.WithName(names, name)
	}
	return out, names, nil
}

// isNull: n is null as YAML reads it: absent, empty, ~ or null.
func isNull(n *yaml.Node) bool {
	return n == nil || n.Kind == yaml.ScalarNode && n.ShortTag() == "!!null"
}

// literals: the keys a map of the document writes as written, in order,
// each once, $$key as $key: those of a plain map, with or without $if;
// none for a map with $for or $value, or another value.
func literals(n *yaml.Node) []string {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	var keys []string
	for i := 0; i < len(n.Content); i += 2 {
		if k := n.Content[i].Value; k == "$for" || k == "$value" {
			return nil
		}
	}
	for i := 0; i < len(n.Content); i += 2 {
		k := n.Content[i].Value
		if cel2yaml.Literal(k) && !slices.Contains(keys, strings.TrimPrefix(k, "$")) {
			keys = append(keys, strings.TrimPrefix(k, "$"))
		}
	}
	return keys
}
