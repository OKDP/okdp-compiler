// Service: the package as a compile reads it: service.yaml (Service), its
// mapping and its other files (Definition).

package compiler

import (
	"errors"
	"fmt"
	"io/fs"

	"go.yaml.in/yaml/v3"

	"github.com/okdp/okdp-compiler/internal/chart"
	"github.com/okdp/okdp-compiler/internal/compile"
	"github.com/okdp/okdp-compiler/internal/value"
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
	Name      string       `yaml:"name"`      // the service: labels, descriptor, parameter schema errors
	Version   string       `yaml:"version"`   // the package's version: descriptor
	Roles     []string     `yaml:"roles"`     // catalog: what the service provides (not read by the compiler)
	DependsOn []string     `yaml:"dependsOn"` // catalog: the platform services it needs (not read by the compiler)
	Charts    chart.Specs  `yaml:"charts"`
	Require   Requirements `yaml:"require"` // the global.okdp keys the service needs: in every case, or under a condition
}

// Requirements: the require section of service.yaml, the platform values
// the service needs, in order.
type Requirements []Requirement

// Requirement: a platform value the service needs, a key under global.okdp
// that must be set and not empty: in every case (no If), or when its
// condition holds; checked on the mapping's context (checkRequirements).
type Requirement struct {
	Key string `yaml:"key"` // dotted, under global.okdp: ingress.suffix
	If  string `yaml:"$if"` // the condition, ${{ <CEL> }} on the mapping's context, true or false; "": in every case
}

// UnmarshalYAML: an entry of require, a key or {key: <key>, $if: ${{
// <condition> }}}, no other field; both are checked with the service
// (Requirements.check).
func (r *Requirement) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.MappingNode {
		return n.Decode(&r.Key)
	}
	for i := 0; i < len(n.Content); i += 2 {
		if k := n.Content[i]; k.Value != "key" && k.Value != "$if" {
			return &yaml.TypeError{Errors: []string{fmt.Sprintf("line %d: field %s not found in type compiler.Requirement", k.Line, k.Value)}}
		}
	}
	type fields Requirement // without UnmarshalYAML
	return n.Decode((*fields)(r))
}

// entry: an entry of require, for messages: require[<i>], followed by
// (<key>) once its key is read.
func entry(i int, key string) string {
	if key == "" {
		return fmt.Sprintf("require[%d]", i)
	}
	return fmt.Sprintf("require[%d] (%s)", i, key)
}

// check: every key set, every condition one expression; for entries built
// in memory too.
func (rs Requirements) check() error {
	for i, r := range rs {
		if r.Key == "" {
			return fmt.Errorf("service.yaml: %s: an empty key: a key under global.okdp (ingress.suffix)", entry(i, ""))
		}
		if r.If == "" {
			continue
		}
		if err := compile.CheckCondition(r.If); err != nil {
			return fmt.Errorf("service.yaml: %s: $if: %w", entry(i, r.Key), err)
		}
	}
	return nil
}

// required: the error of a platform value key that is missing or empty
// (null, false, "", 0, [] or {}) in okdpG, global.okdp; nil when it is set.
// It names the input the value comes from (platformValue), not a file.
func required(okdpG M, key string) error {
	if value.Truthy(value.Dig(okdpG, key)) {
		return nil
	}
	return fmt.Errorf("global.okdp.%s is required. %s", key, platformValue)
}

// platformValue: the end of the error of a missing platform value: the
// input that sets it, the platform's values layers, by the binary's flag
// and the library's field.
const platformValue = "It is a platform value: set it in the platform's values (--platform, Inputs.Platform)"

// meta: what the mapping and OKDP's objects see of the package.
func (s *Service) meta() serviceMeta {
	return serviceMeta{Name: s.Name, Version: s.Version}
}

// Check: what service.yaml may not hold, refused when it is loaded
// (LoadDefinition) and before every compile: no name or version, a
// require entry without its key or with a condition that is not one
// expression, the chart of OKDP's own objects (the compiler adds it, in
// every compile: okdpChart), then what its charts may not hold
// (chart.Specs.Check).
func (s *Service) Check() error {
	switch {
	case s.Name == "":
		return errors.New("service.yaml: name is required")
	case s.Version == "":
		return errors.New("service.yaml: version is required")
	}
	if err := s.Require.check(); err != nil {
		return err
	}
	if err := okdpChart.listed(s.Charts, nil); err != nil {
		return err
	}
	return s.Charts.Check()
}
