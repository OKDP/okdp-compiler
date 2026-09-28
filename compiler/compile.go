// Package compiler compiles an OKDP service instance into the values of its
// upstream charts with a YAML + CEL mapping, plus the OKDP objects of the
// instance (its descriptor ConfigMap).
//
// Everything is read through fs.FS and in-memory values, so a caller (the
// control plane server, a CLI) can compile from a checkout, an embedded
// filesystem or a git tree without touching the disk:
//
//	def, err := compiler.LoadDefinition(os.DirFS("example"))
//	res, err := compiler.Compile(def, compiler.Inputs{
//		Chart:     os.DirFS("charts/services/myservice"),
//		Contracts: os.DirFS("okdp-lib-chart/contracts"),
//		Values:    []map[string]any{instanceValues},
//		Release:   "demo-myservice",
//		Namespace: "demo",
//	})
package compiler

import (
	"errors"
	"fmt"
	"io/fs"

	"gopkg.in/yaml.v3"
)

// Definition: a service definition, service.yaml and its mapping.
type Definition struct {
	Service Service
	Mapping []byte // mapping.cel.yaml
}

// LoadDefinition reads service.yaml and mapping.cel.yaml at the root of fsys.
func LoadDefinition(fsys fs.FS) (*Definition, error) {
	b, err := fs.ReadFile(fsys, "service.yaml")
	if err != nil {
		return nil, err
	}
	def := &Definition{}
	if err := yaml.Unmarshal(b, &def.Service); err != nil {
		return nil, fmt.Errorf("service.yaml: %w", err)
	}
	if def.Mapping, err = fs.ReadFile(fsys, "mapping.cel.yaml"); err != nil {
		return nil, err
	}
	return def, nil
}

// Inputs: what one compile reads besides the service definition.
type Inputs struct {
	Chart     fs.FS            // wrapper chart: Chart.yaml, values.yaml, values.schema.json, vendor/, vendor-values/
	Contracts fs.FS            // okdp-lib-chart/contracts: <contract>.schema.json
	Values    []map[string]any // instance values layers, in order (helm -f); see ParseValues
	Release   string
	Namespace string
}

// Service: service.yaml, the engine-neutral part of a service definition.
type Service struct {
	Charts  map[string]ChartSpec `yaml:"charts"`
	Require []string             `yaml:"require"` // global.okdp keys the service needs
}

// ChartSpec: one upstream chart of a service.
type ChartSpec struct {
	Fixed    string   `yaml:"fixed"`    // vendor-values file of the wrapper (fixed values)
	Values   string   `yaml:"values"`   // name of the values ConfigMap to compare with
	Protect  []string `yaml:"protect"`  // instance upstream values: protected paths
	Append   []string `yaml:"append"`   // instance upstream values: appended lists
	Upstream *bool    `yaml:"upstream"` // instance upstream values accepted
}

// Result: what a compile produces.
type Result struct {
	// Computed: chart -> the values the mapping computed, before the merge
	// with the upstream defaults, the fixed values and the instance values.
	Computed map[string]any
	// Values: chart -> the final values the upstream chart receives.
	Values map[string]map[string]any
	// Objects: OKDP's own objects, rendered whole (the descriptor ConfigMap).
	Objects []map[string]any
}

// Compile runs the mapping of def on one instance. It is safe for
// concurrent use: it keeps no state between calls.
func Compile(def *Definition, in Inputs) (*Result, error) {
	switch {
	case def == nil:
		return nil, errors.New("no service definition")
	case in.Chart == nil:
		return nil, errors.New("Inputs.Chart is required")
	case in.Contracts == nil:
		return nil, errors.New("Inputs.Contracts is required")
	case in.Release == "":
		return nil, errors.New("Inputs.Release is required")
	case in.Namespace == "":
		return nil, errors.New("Inputs.Namespace is required")
	}
	svc := &def.Service
	// What the mapping sees: values, platform context, resolved connections.
	ctx, values, err := buildContext(in, svc)
	if err != nil {
		return nil, err
	}
	res, err := runCEL(def.Mapping, ctx)
	if err != nil {
		return nil, err
	}
	// The descriptor ConfigMap, checked against the contracts.
	desc, err := descriptor(in, ctx, res.instance, res.provides)
	if err != nil {
		return nil, err
	}
	out := &Result{Computed: res.output, Values: map[string]map[string]any{}, Objects: []map[string]any{desc}}
	// Charts left out by the mapping ($if) are not rendered.
	for chart, v := range res.output {
		if _, ok := svc.Charts[chart]; !ok {
			return nil, fmt.Errorf("mapping output %q: not a chart of service.yaml", chart)
		}
		f, err := finalValues(in, svc, chart, asMap(v), values)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", chart, err)
		}
		out.Values[chart] = f
	}
	return out, nil
}
