// Package fsio is the edges of a compile: the package read from a
// filesystem (LoadDefinition), and the files a compile is written as
// (Document, Files). Each stage reads the other inputs through the fs.FS
// the caller gives it (okdp.Inputs).
package fsio

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"reflect"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/okdp/okdp-compiler/internal/chart"
	"github.com/okdp/okdp-compiler/internal/okdp"
)

// LoadDefinition reads the package at the root of fsys: service.yaml,
// checked, and mapping.cel.yaml; each compile reads its other files.
func LoadDefinition(fsys fs.FS) (*okdp.Definition, error) {
	b, err := fs.ReadFile(fsys, "service.yaml")
	if err != nil {
		return nil, err
	}
	def := &okdp.Definition{Files: fsys}
	if err := decodeService(b, &def.Service); err != nil {
		return nil, err
	}
	if err := def.Service.Check(); err != nil {
		return nil, err
	}
	if def.Mapping, err = fs.ReadFile(fsys, "mapping.cel.yaml"); err != nil {
		return nil, err
	}
	return def, nil
}

// decodeService: service.yaml into svc, strictly: a key that names no
// field fails (a misspelt protect would otherwise leave its paths
// unprotected), and so does a second YAML document (it would be ignored).
// An empty file is a service without a name (Service.Check).
func decodeService(b []byte, svc *okdp.Service) error {
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(svc); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return decodeError(err)
	}
	var next yaml.Node
	if err := dec.Decode(&next); err == nil {
		return fmt.Errorf("service.yaml: line %d: a second YAML document: service.yaml is one document", next.Line)
	} else if !errors.Is(err, io.EOF) {
		return fmt.Errorf("service.yaml: %w", err)
	}
	return nil
}

// unknownField: yaml.v3's error for a key that names no field of the
// struct it decodes into (KnownFields): its line, the key, the struct.
var unknownField = regexp.MustCompile(`^line (\d+): field (.*) not found in type (\S+)$`)

// serviceStructs: the structs of service.yaml, whose fields the error of an
// unknown field lists.
var serviceStructs = []reflect.Type{reflect.TypeFor[okdp.Service](), reflect.TypeFor[chart.Spec](), reflect.TypeFor[chart.Ref]()}

// decodeError: an error decoding service.yaml, prefixed with its name; each
// error of a yaml.v3 TypeError on a line of its own, an unknown field's
// with the fields of its struct.
func decodeError(err error) error {
	var te *yaml.TypeError
	if !errors.As(err, &te) {
		return fmt.Errorf("service.yaml: %w", err)
	}
	errs := make([]error, len(te.Errors))
	for i, e := range te.Errors {
		if m := unknownField.FindStringSubmatch(e); m != nil {
			e = fmt.Sprintf("line %s: unknown field %s", m[1], m[2])
			if f := serviceFields(m[3]); f != nil {
				e += " (allowed: " + strings.Join(f, ", ") + ")"
			}
		}
		errs[i] = errors.New("service.yaml: " + e)
	}
	return errors.Join(errs...)
}

// serviceFields: the fields of the struct of service.yaml that yaml.v3
// names typ, as written in the file; nil for another type.
func serviceFields(typ string) []string {
	for _, t := range serviceStructs {
		if t.String() != typ {
			continue
		}
		var names []string
		for f := range t.Fields() {
			if name, _, _ := strings.Cut(f.Tag.Get("yaml"), ","); name != "" {
				names = append(names, name)
			}
		}
		return names
	}
	return nil
}
