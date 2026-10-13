// Load: the package read from a filesystem (LoadDefinition); each stage
// reads the other inputs through the fs.FS the caller gives it (Inputs).

package okdp

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"

	"go.yaml.in/yaml/v3"
)

// LoadDefinition reads the package at the root of fsys: service.yaml,
// checked, and values.cel.yaml; each compile reads its other files.
func LoadDefinition(fsys fs.FS) (*Definition, error) {
	b, err := fs.ReadFile(fsys, "service.yaml")
	if err != nil {
		return nil, err
	}
	def := &Definition{Files: fsys}
	if err := decodeService(b, &def.Service); err != nil {
		return nil, err
	}
	if err := def.Service.Check(); err != nil {
		return nil, err
	}
	if def.Mapping, err = fs.ReadFile(fsys, "values.cel.yaml"); err != nil {
		return nil, err
	}
	return def, nil
}

// decodeService: the first YAML document of service.yaml into svc,
// strictly: a key that names no field fails (a misspelt protect would
// otherwise leave its paths unprotected). An empty file is a service
// without a name (Service.Check).
func decodeService(b []byte, svc *Service) error {
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(svc); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("service.yaml: %w", err)
	}
	return nil
}
