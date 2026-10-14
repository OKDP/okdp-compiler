// Load: the package read from a filesystem (LoadDefinition); each stage
// reads the other inputs through the fs.FS the caller gives it (Inputs).

package okdp

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/okdp/okdp-compiler/internal/chart"
)

// LoadDefinition reads the package at the root of fsys: Chart.yaml, then
// okdp.yaml and its values file, values.cel.yaml or values.yaml, when it
// has them, checked; each compile reads its other files. A values.yaml
// that holds expressions is read all the same, with a warning
// (Definition.Warnings).
func LoadDefinition(fsys fs.FS) (*Definition, error) {
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
	if def.Package, err = decodePackage(def.Okdp); err != nil {
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

// holdsExpressions: whether the values file b, its first YAML document,
// holds what Helm would not read as the same values: a string holding ${{
// (an expression, a $${{), a key starting with $ (a directive, a $$key),
// at any depth; false when it is not YAML, which the compile reports.
func holdsExpressions(b []byte) bool {
	var doc yaml.Node
	return yaml.Unmarshal(b, &doc) == nil && expressionIn(&doc)
}

// expressionIn: whether the node n, or one under it, holds an expression
// or a $-key (holdsExpressions).
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

// readOptional: the file name of fsys, nil when there is none.
func readOptional(fsys fs.FS, name string) ([]byte, error) {
	b, err := fs.ReadFile(fsys, name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return b, err
}

// decodePackage: the package keys of okdp.yaml, b, its first YAML
// document, strictly: a key that is neither a package key nor a mapping key
// fails (a misspelt require would otherwise leave the platform values
// unchecked); the charts' values (output) and Chart.yaml's keys with their
// own message. An empty file, or none, holds none.
func decodePackage(b []byte) (Package, error) {
	var p Package
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return p, fmt.Errorf("okdp.yaml: %w", err)
	}
	if len(doc.Content) == 1 && doc.Content[0].Kind == yaml.MappingNode {
		top := doc.Content[0].Content
		for i := 0; i < len(top); i += 2 {
			switch k := top[i].Value; k {
			case "output":
				return p, errors.New("okdp.yaml: output: the charts' values are in values.cel.yaml (or values.yaml)")
			case "name", "version", "charts":
				return p, fmt.Errorf("okdp.yaml: %s: the name, version and charts are Chart.yaml's", k)
			}
		}
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&p); err != nil && !errors.Is(err, io.EOF) {
		return p, fmt.Errorf("okdp.yaml: %w", err)
	}
	return p, nil
}
