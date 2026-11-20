// Load: reading a package from a filesystem.

package okdp

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"reflect"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/okdp/okdp-compiler/chart"
)

// LoadDefinition reads and checks the package at the root of fsys:
// Chart.yaml, then okdp.yaml and the values file when the package has
// them. A values.yaml that holds expressions is still read, with a
// warning (Definition.Warnings). It is LoadDefinitionWith the mandatory
// rule set.
func LoadDefinition(fsys fs.FS) (*Definition, error) { return LoadDefinitionWith(fsys, nil) }

// LoadDefinitionWith is LoadDefinition for the compiles of rules: a
// top-level key of okdp.yaml that Package does not declare is one of the
// sections of rules (RuleSet.Sections), or an error. Nil rules stand for
// the mandatory set.
func LoadDefinitionWith(fsys fs.FS, rules *RuleSet) (*Definition, error) {
	b, err := fs.ReadFile(fsys, chart.MetadataFile)
	if err != nil {
		return nil, err
	}
	def := &Definition{Files: fsys}
	if def.Chart, err = chart.ReadMetadata(b); err != nil {
		return nil, err
	}
	if def.Okdp, err = readOptional(fsys, "okdp.yaml"); err != nil {
		return nil, err
	}
	if def.Package, err = decodePackageWith(def.Okdp, rulesOr(rules).Sections()); err != nil {
		return nil, err
	}
	if _, err := def.service(); err != nil {
		return nil, err
	}
	cel, err := readOptional(fsys, valuesFile)
	if err != nil {
		return nil, err
	}
	plain, err := readOptional(fsys, plainValuesFile)
	if err != nil {
		return nil, err
	}
	switch {
	case cel != nil && plain != nil:
		return nil, errors.New("values.yaml and values.cel.yaml: a package has one of them")
	case cel != nil:
		def.Values, def.ValuesFile = cel, valuesFile
	case plain != nil:
		def.Values, def.ValuesFile = plain, plainValuesFile
		if holdsExpressions(plain) {
			def.Warnings = append(def.Warnings, "values.yaml holds expressions or directives (${{ }}, $-keys): name it values.cel.yaml")
		}
	}
	return def, nil
}

// holdsExpressions reports whether the values file b holds something
// Helm would not read as the same values: a string that holds "${{" or
// a key that starts with "$". It returns false when b is not YAML: the
// compile reports that.
func holdsExpressions(b []byte) bool {
	var doc yaml.Node
	return yaml.Unmarshal(b, &doc) == nil && expressionIn(&doc)
}

func expressionIn(n *yaml.Node) bool {
	if n.Kind == yaml.ScalarNode {
		return strings.Contains(n.Value, "${{")
	}
	for i, c := range n.Content {
		if n.Kind == yaml.MappingNode && i%2 == 0 {
			if strings.HasPrefix(c.Value, "$") {
				return true
			}
		} else if expressionIn(c) {
			return true
		}
	}
	return false
}

// readOptional returns nil when fsys has no file name.
func readOptional(fsys fs.FS, name string) ([]byte, error) {
	b, err := fs.ReadFile(fsys, name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return b, err
}

// decodePackage decodes the package keys of okdp.yaml strictly, for the
// mandatory rule set (decodePackageWith).
func decodePackage(b []byte) (Package, error) {
	return decodePackageWith(b, Mandatory().Sections())
}

// decodePackageWith decodes the package keys of okdp.yaml strictly: an
// unknown key fails. Otherwise a misspelt require would leave the
// platform values unchecked. A top-level key of sections, the rule set's
// sections of a mapping, is known: the mapping reads it. The charts'
// values (output) and the keys of Chart.yaml fail with their own message.
func decodePackageWith(b []byte, sections []string) (Package, error) {
	var p Package
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return p, fmt.Errorf("okdp.yaml: %w", err)
	}
	// The decoder's error for each section key, which is not one.
	var known []string
	if len(doc.Content) == 1 && doc.Content[0].Kind == yaml.MappingNode {
		top := doc.Content[0].Content
		for i := 0; i < len(top); i += 2 {
			switch k := top[i].Value; k {
			case "output":
				return p, errors.New("okdp.yaml: output: the charts' values are in values.cel.yaml (or values.yaml)")
			case "name", "version", "charts":
				return p, fmt.Errorf("okdp.yaml: %s: the name, version and charts are Chart.yaml's", k)
			}
			if slices.Contains(sections, top[i].Value) {
				known = append(known, fmt.Sprintf("line %d: field %s not found in type %s", top[i].Line, top[i].Value, reflect.TypeFor[Package]()))
			}
		}
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	err := dec.Decode(&p)
	if te, ok := errors.AsType[*yaml.TypeError](err); ok {
		te.Errors = slices.DeleteFunc(te.Errors, func(e string) bool { return slices.Contains(known, e) })
		if len(te.Errors) == 0 {
			err = nil
		}
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return p, fmt.Errorf("okdp.yaml: %w", err)
	}
	return p, nil
}
