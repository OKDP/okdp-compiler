// Service: the package as a compile reads it. Definition holds its
// files; Service holds what a compile builds from them.

package okdp

import (
	"fmt"
	"io/fs"

	"go.yaml.in/yaml/v3"

	"github.com/okdp/okdp-compiler/chart"
)

// Definition is a package as LoadDefinition reads it.
type Definition struct {
	Chart      chart.Metadata // Chart.yaml, also the umbrella's metadata (Result.Chart)
	Package    Package        // the package keys of okdp.yaml
	Okdp       []byte         // okdp.yaml, or nil without one
	Values     []byte         // the values file (the mapping's output section), or nil without one
	ValuesFile string         // values.cel.yaml or values.yaml; "" means values.cel.yaml
	Files      fs.FS          // the package: config.schema.json, the defaults files
	// Warnings holds what LoadDefinition warns of. Each compile returns
	// them (Result.Warnings).
	Warnings []string
}

// Package holds the package keys of okdp.yaml.
type Package struct {
	// Require lists the global.okdp keys the service needs, dotted
	// (tls.clusterIssuer). Each must be set and not empty.
	Require   []string `yaml:"require"`
	Roles     []string `yaml:"roles"`     // catalog data, not read by the compiler
	DependsOn []string `yaml:"dependsOn"` // catalog data, not read by the compiler
	// Defaults maps a dependency key to a values file of the package:
	// OKDP's defaults for the chart.
	Defaults map[string]string `yaml:"defaults"`
	// Upstream lists the dependencies that accept upstream values from
	// an instance, with their protect and append paths.
	Upstream chart.Upstream `yaml:"upstream"`
	// Provisions lists the contracts the package provisions connections
	// of, one connection per claim bound to an instance.
	Provisions []Provision `yaml:"provisions"`
}

// Provision is an entry of the provisions of okdp.yaml, written as the
// contract's name or as {contract, claimers}. Claimers lists the
// namespaces whose instances may claim from an instance of the package
// across namespaces, "*" for any. An instance that lists its own
// (Inputs.Claimers) replaces them. The default is none (consent).
type Provision struct {
	Contract string   `yaml:"contract"`
	Claimers []string `yaml:"claimers"`
}

// UnmarshalYAML decodes an entry strictly: another key or form fails.
func (p *Provision) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		p.Claimers = nil
		return n.Decode(&p.Contract)
	case yaml.MappingNode:
		for i := 0; i < len(n.Content); i += 2 {
			if k := n.Content[i].Value; k != "contract" && k != "claimers" {
				return fmt.Errorf("provisions (line %d): unknown key %q: contract and claimers only", n.Content[i].Line, k)
			}
		}
		type plain Provision
		return n.Decode((*plain)(p))
	}
	return fmt.Errorf("provisions (line %d): an entry is a contract name, or {contract, claimers}", n.Line)
}

// claimers returns the claimers of the provisions entry of contract.
func (p Package) claimers(contract string) []string {
	for _, e := range p.Provisions {
		if e.Contract == contract {
			return e.Claimers
		}
	}
	return nil
}

// packageKeys lists the okdp.yaml keys of Package, which the mapping skips.
var packageKeys = []string{"require", "roles", "dependsOn", "defaults", "upstream", "provisions"}

// Service is the engine-neutral part of a package, as a compile reads it
// (Definition.service).
type Service struct {
	Name       string      // the name of Chart.yaml; the labels and the schema errors use it
	Version    string      // from Chart.yaml, also the umbrella's
	Require    []string    // okdp.yaml: the global.okdp keys the service needs
	Charts     chart.Specs // Chart.yaml's dependencies, with the defaults and upstream of okdp.yaml
	Provisions []string    // okdp.yaml: the contracts the package provisions connections of
}

// platformValue ends the error of a missing platform value. It names
// the input that sets it: the context's values layers (Inputs.Context).
const platformValue = "It is a context value: the platform's or a project's (Inputs.Context)"

// service builds the Service of d and checks it. It runs when the
// package is loaded and before every compile, because a Definition may
// be built in memory.
func (d *Definition) service() (*Service, error) {
	if err := d.Chart.Check(); err != nil {
		return nil, err
	}
	specs, err := chart.NewSpecs(d.Chart, d.Package.Defaults, d.Package.Upstream)
	if err != nil {
		return nil, err
	}
	p := d.Package
	if err := checkProvisions(p.Provisions); err != nil {
		return nil, err
	}
	return &Service{Name: d.Chart.Name, Version: d.Chart.Version, Require: p.Require, Charts: specs, Provisions: p.contracts()}, nil
}

func (p Package) contracts() []string {
	var out []string
	for _, e := range p.Provisions {
		out = append(out, e.Contract)
	}
	return out
}
