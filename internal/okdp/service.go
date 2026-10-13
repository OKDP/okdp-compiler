// Service: the package as a compile reads it: service.yaml (Service), its
// mapping and its other files (Definition).

package okdp

import (
	"errors"
	"io/fs"

	"github.com/okdp/okdp-compiler/internal/chart"
)

// Definition: a package, as LoadDefinition reads it: service.yaml, its
// mapping, and the package's other files.
type Definition struct {
	Service Service
	Mapping []byte // values.cel.yaml
	Files   fs.FS  // the package: config.schema.json, the defaults files
}

// Service: service.yaml, the engine-neutral part of a package.
type Service struct {
	Name      string      `yaml:"name"`      // the service: labels, descriptor, parameter schema errors
	Version   string      `yaml:"version"`   // the package's version: descriptor
	Roles     []string    `yaml:"roles"`     // catalog: what the service provides (not read by the compiler)
	DependsOn []string    `yaml:"dependsOn"` // catalog: the platform services it needs (not read by the compiler)
	Charts    chart.Specs `yaml:"charts"`
	Require   []string    `yaml:"require"` // the global.okdp keys the service needs, dotted (ingress.suffix), each set and not empty
}

// platformValue: the end of the error of a missing platform value: the
// input that sets it, the context's values layers, by the binary's flag
// and the library's field.
const platformValue = "It is a context value: set it with --context (Inputs.Context)"

// Check: what service.yaml may not hold, refused when it is loaded
// (LoadDefinition) and before every compile: no name or version, the chart
// of OKDP's own objects (the compiler adds it, in every compile:
// okdpChart), then what its charts may not hold (chart.Specs.Check).
func (s *Service) Check() error {
	switch {
	case s.Name == "":
		return errors.New("service.yaml: name is required")
	case s.Version == "":
		return errors.New("service.yaml: version is required")
	}
	if err := okdpChart.listed(s.Charts, nil); err != nil {
		return err
	}
	return s.Charts.Check()
}
