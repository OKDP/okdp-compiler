// Service: the package as a compile reads it: Chart.yaml, okdp.yaml and
// its values file, and its other files (Definition), and what a compile
// builds of them (Service).

package okdp

import (
	"io/fs"

	"go.yaml.in/yaml/v3"

	"github.com/okdp/okdp-compiler/internal/chart"
)

// Definition: a package, as LoadDefinition reads it: Chart.yaml, okdp.yaml
// (its package keys, and its mapping), its values file, values.cel.yaml or
// values.yaml, and the package's other files.
type Definition struct {
	Chart      chart.Metadata // Chart.yaml: the service's name, version and upstream charts, the umbrella's metadata (Result.Chart)
	Package    Package        // okdp.yaml's package keys
	Okdp       []byte         // okdp.yaml, read by the mapping: vars, errors and the OKDP sections, its package keys skipped; nil: none
	Values     []byte         // the values file, read by the mapping as its output section: the charts' values, by dependency key; nil: none
	ValuesFile string         // the values file's name, values.cel.yaml or values.yaml, which its errors start with; "": values.cel.yaml
	Files      fs.FS          // the package: config.schema.json, the defaults files
	Warnings   []string       // what LoadDefinition warns of, the package read all the same: a values.yaml that holds expressions; given with each compile (Result.Warnings)
}

// Package: okdp.yaml's package keys, what the compiler reads of the
// package besides Chart.yaml and the mapping.
type Package struct {
	Require   []string          `yaml:"require"`   // the global.okdp keys the service needs, dotted (ingress.suffix), each set and not empty
	Roles     []string          `yaml:"roles"`     // catalog: what the service provides (not read by the compiler)
	DependsOn []string          `yaml:"dependsOn"` // catalog: the platform services it needs (not read by the compiler)
	Defaults  map[string]string `yaml:"defaults"`  // dependency key -> a values file of the package, <key>.defaults.yaml by convention: OKDP's defaults for the chart
	Upstream  chart.Upstream    `yaml:"upstream"`  // the dependencies that accept instance upstream values, with their protect and append paths

	// The mapping's keys, read by the mapping (Definition.Okdp), declared
	// so that okdp.yaml is read strictly: any other key fails.
	Vars        yaml.Node `yaml:"vars"`
	Errors      yaml.Node `yaml:"errors"`
	Instance    yaml.Node `yaml:"instance"`
	Provides    yaml.Node `yaml:"provides"`
	Objects     yaml.Node `yaml:"objects"`
	OIDCClients yaml.Node `yaml:"oidcClients"`
}

// packageKeys: the keys of okdp.yaml its package reader reads (Package),
// skipped by the mapping.
var packageKeys = []string{"require", "roles", "dependsOn", "defaults", "upstream"}

// Service: the engine-neutral part of a package, as a compile reads it
// (Definition.service).
type Service struct {
	Name      string      // Chart.yaml's name, the service: labels, parameter schema errors
	Version   string      // Chart.yaml's version, the package's: the umbrella's
	Roles     []string    // okdp.yaml: catalog, not read by the compiler
	DependsOn []string    // okdp.yaml: catalog, not read by the compiler
	Require   []string    // okdp.yaml: the global.okdp keys the service needs
	Charts    chart.Specs // Chart.yaml's dependencies, with okdp.yaml's defaults and upstream
}

// platformValue: the end of the error of a missing platform value: the
// input that sets it, the context's values layers, by the binary's flag
// and the library's field.
const platformValue = "It is a context value: set it with --context (Inputs.Context)"

// service: the Service of d, checked when it is loaded (LoadDefinition)
// and before every compile (a Definition may be built in memory): what
// Chart.yaml may not hold (chart.Metadata.Check), then what okdp.yaml's
// defaults and upstream may not hold (chart.NewSpecs).
func (d *Definition) service() (*Service, error) {
	if err := d.Chart.Check(); err != nil {
		return nil, err
	}
	specs, err := chart.NewSpecs(d.Chart, d.Package.Defaults, d.Package.Upstream)
	if err != nil {
		return nil, err
	}
	p := d.Package
	return &Service{Name: d.Chart.Name, Version: d.Chart.Version, Roles: p.Roles, DependsOn: p.DependsOn, Require: p.Require, Charts: specs}, nil
}
