// Service: the package as a compile reads it: service.yaml (Service), its
// mapping and its other files (Definition).
package okdp

import (
	"errors"
	"fmt"
	"io/fs"

	"github.com/okdp/okdp-compiler/internal/chart"
)

// Definition: a package, as fsio.LoadDefinition reads it: service.yaml, its
// mapping, and the package's other files.
type Definition struct {
	Service Service
	Mapping []byte // mapping.cel.yaml
	Files   fs.FS  // the package: values.yaml, values.schema.json, the base files
}

// Service: service.yaml, the engine-neutral part of a package.
type Service struct {
	Name       string      `yaml:"name"`       // the service: labels, descriptor, parameter schema errors
	Version    string      `yaml:"version"`    // the package's version: descriptor
	AppVersion string      `yaml:"appVersion"` // the application's version: label app.kubernetes.io/version
	Roles      []string    `yaml:"roles"`      // catalog: what the service provides (not read by the compiler)
	DependsOn  []string    `yaml:"dependsOn"`  // catalog: the platform services it needs (not read by the compiler)
	Charts     chart.Specs `yaml:"charts"`
	Require    []string    `yaml:"require"` // global.okdp keys the service needs
}

// meta: what the mapping and OKDP's objects see of the package.
func (s *Service) meta() serviceMeta {
	return serviceMeta{Name: s.Name, Version: s.Version, AppVersion: s.AppVersion}
}

// Check: what service.yaml may not hold, refused when it is loaded
// (fsio.LoadDefinition) and before every compile: no name or version, the
// chart of OKDP's own objects (the compiler adds it), then what its charts
// may not hold (chart.Specs.Check).
func (s *Service) Check() error {
	switch {
	case s.Name == "":
		return errors.New("service.yaml: name is required")
	case s.Version == "":
		return errors.New("service.yaml: version is required")
	}
	if _, ok := s.Charts[ObjectsChart]; ok {
		return fmt.Errorf("service.yaml: charts.%s: the chart of OKDP's own objects is added by the compiler: remove it from service.yaml", ObjectsChart)
	}
	return s.Charts.Check()
}
