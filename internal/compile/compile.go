// Package compile reads a document from one file or several (Source) and
// evaluates it in a cel2yaml session. A document is written in the
// language of a mapping: YAML where ${{ <CEL> }} computes a value
// (github.com/okdp/cel2yaml). This package adds the top level of the
// document, and knows neither Helm charts nor OKDP.
//
// Top level, before any section:
//
//	vars (ordered bindings)
//	errors (a list of messages, {$if: <condition>, $value: <text>}: any
//	message fails the compile)
//
// Any other top-level key is a section the caller names, or a key another
// reader reads (Source.Skip). A file may also be one section whole.
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

// M is a values map (value.M).
type M = value.M

// Source is one file of a document (Eval).
type Source struct {
	Name    string   // the file name: the first segment of its errors
	Data    []byte   // its YAML
	Section string   // when set, the whole file is this section
	Skip    []string // top-level keys left to another reader, when Section is ""
}

// Doc is an evaluated document (Eval).
type Doc struct {
	Sections M // the values of the sections the sources give, by name
	// Written lists, by section name, the keys the section writes at its
	// top level, with $$key read as $key. It leaves out the keys a $merge
	// or a $for gives. A section is listed whether its $if keeps it or not.
	Written map[string][]string
}

// document is a built document. srcs maps vars, errors and each section
// to the source that gives it, whose name starts their errors.
type document struct {
	vars     []binding
	errors   *cel2yaml.Template
	sections map[string]*cel2yaml.Template
	written  map[string][]string
	srcs     map[string]Source
}

// binding is one name of vars. at is its path in errors: "vars <name>".
type binding struct {
	name, at string
	t        *cel2yaml.Template
}

// Eval evaluates the document that the sources srcs make together on the
// context scope, in the session s. sections names the sections of the
// document; Eval returns those it has. It first builds the whole document, so a wrong directive or expression
// fails in every branch, even one that is not taken. It then evaluates
// vars, the errors section, whose messages it collects before it fails,
// and the sections. An error starts with the name of its source, then
// its path (okdp.yaml: objects[1]: ...).
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

// Prepared is a document built once (Prepare) and evaluated on many
// inputs, in its session. It is not safe for concurrent use.
type Prepared struct {
	s        *cel2yaml.Session
	t        *document
	sections []string
}

// Problem is one item of the errors section of a document: a message,
// and the path of the field it is about when the item names one.
type Problem struct{ Path, Message string }

// Prepare builds the document of srcs: its vars, its errors and the named
// sections. It compiles the expressions once, with names in scope.
func Prepare(s *cel2yaml.Session, names, sections []string, srcs ...Source) (*Prepared, error) {
	t, err := build(s, srcs, slices.Sorted(slices.Values(names)), sections)
	if err != nil {
		return nil, err
	}
	return &Prepared{s, t, sections}, nil
}

// Eval evaluates the document on scope and returns the items of its
// errors section, in order. When there is none, it also returns the
// values of the sections, by name. A failure to evaluate the document is
// an error, not a problem. A host function may call Eval while another
// document of the session is being evaluated.
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

// bind returns the scope of the sections of t: the context outer plus
// the vars bindings. It then evaluates the errors section, and returns
// its messages joined, an error each.
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
			refused = append(refused, errors.New(msg))
		}
	}
	if len(refused) > 0 {
		return nil, errors.Join(refused...)
	}
	return scope, nil
}

// vars2scope returns a copy of the context outer with the vars bindings
// added in order. Each binding sees the previous ones.
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

// at places err under k, then under the name of the source of k. It
// leaves k out for vars and for a source that is the section k.
func (t *document) at(k string, err error) error {
	src := t.srcs[k]
	if k != "vars" && src.Section == "" {
		err = cel2yaml.AtKey(k, err)
	}
	return fmt.Errorf("%s: %w", src.Name, err)
}

// build builds the document of the sources srcs. names are the names of
// its context, sorted. A source that is a section gives it whole. The
// top level of any other source is a map of vars, errors, sections and
// skipped keys. At most one source gives vars, errors and each section.
// vars is compiled first: its names are in scope of the rest.
func build(s *cel2yaml.Session, srcs []Source, names, sections []string) (*document, error) {
	allowed := append([]string{"vars", "errors"}, sections...)
	t := &document{sections: map[string]*cel2yaml.Template{}, written: map[string][]string{}, srcs: map[string]Source{}}
	read := map[string]*yaml.Node{} // vars, errors and each section, as read
	from := map[string]int{}        // the position of the source of each
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

// bindings compiles the vars map v, each value with the names before it
// in scope. A name bound twice (y and "y") takes the later binding. It
// also returns the names in scope after vars.
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

// isNull reports whether n is null as YAML reads it, or absent.
func isNull(n *yaml.Node) bool {
	return n == nil || n.Kind == yaml.ScalarNode && n.ShortTag() == "!!null"
}

// literals returns the keys that the map n of a document writes as
// written, each once, with $$key read as $key. A map with $for or
// $value, and any value that is not a map, gives none.
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
