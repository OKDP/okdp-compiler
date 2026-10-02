// Package fsio is the edges of a compile: the package read from a
// filesystem (LoadDefinition), and the files a compile is written as
// (Document, Files). Each stage reads the other inputs through the fs.FS
// the caller gives it (okdp.Inputs).
package fsio

import (
	"fmt"
	"io/fs"

	"gopkg.in/yaml.v3"

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
	if err := yaml.Unmarshal(b, &def.Service); err != nil {
		return nil, fmt.Errorf("service.yaml: %w", err)
	}
	if err := def.Service.Check(); err != nil {
		return nil, err
	}
	if def.Mapping, err = fs.ReadFile(fsys, "mapping.cel.yaml"); err != nil {
		return nil, err
	}
	return def, nil
}
