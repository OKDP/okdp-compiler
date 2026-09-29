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
//		Instance:  os.DirFS("instances/demo/myservice"),
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
	"slices"

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
	Instance  fs.FS            // instance files: the upstream values files of service.yaml (optional)
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
	Base     string   `yaml:"base"`     // vendor-values file of the wrapper (OKDP's default values)
	Values   string   `yaml:"values"`   // name of the values ConfigMap to compare with
	Protect  []string `yaml:"protect"`  // instance upstream values: protected paths
	Append   []string `yaml:"append"`   // instance upstream values: appended lists
	Upstream Upstream `yaml:"upstream"` // instance upstream values accepted, and their files
}

// Upstream: the user-defined values a chart accepts. true reads
// upstream.<chart>.yaml of the instance when it exists; a path, or a list of
// paths, reads these files instead (they must exist), merged in order. The
// instance's inline upstream.<chart> values come after the files.
type Upstream struct {
	Enabled bool
	Files   []string // nil: upstream.<chart>.yaml, if it exists
}

// UnmarshalYAML: true or false, a path or a list of paths.
func (u *Upstream) UnmarshalYAML(n *yaml.Node) error {
	switch {
	case n.Kind == yaml.ScalarNode && n.Tag == "!!null":
		return nil
	case n.Kind == yaml.ScalarNode && n.Tag == "!!bool":
		return n.Decode(&u.Enabled)
	case n.Kind == yaml.ScalarNode && n.Tag == "!!str" && n.Value != "":
		u.Enabled, u.Files = true, []string{n.Value}
		return nil
	case n.Kind == yaml.SequenceNode:
		files := []string{}
		if err := n.Decode(&files); err != nil {
			return err
		}
		if slices.Contains(files, "") {
			break
		}
		u.Enabled, u.Files = true, files
		return nil
	}
	return fmt.Errorf("line %d: upstream must be true, false, a path or a list of paths", n.Line)
}

// ObjectsChart: the chart of service.yaml that renders OKDP's own objects
// (okdp-objects, values `objects: []`). When a service declares it, the
// compiler appends Result.Objects to the objects the mapping computes for
// it (output.okdp.objects), and compiles it even when the mapping leaves it
// out: the descriptor ConfigMap, the credentials Secrets and the objects
// section reach the cluster with the upstream charts.
const ObjectsChart = "okdp"

// Result: what a compile produces.
type Result struct {
	// Computed: chart -> the values the mapping computed, before the merge
	// with the upstream defaults, the base values and the instance values.
	Computed map[string]any
	// Values: chart -> the final values the upstream chart receives.
	Values map[string]map[string]any
	// Objects: OKDP's own objects, rendered whole: the descriptor ConfigMap,
	// the credentials Secrets of the provided connections, then the objects
	// section of the mapping. With an okdp chart in service.yaml
	// (ObjectsChart), they are also in its values.
	Objects []map[string]any
	// Dependencies: the instances the connection references point to, by
	// parameter path. References to external connections are not listed.
	Dependencies []Dependency
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
	ctx, values, deps, err := buildContext(in, svc)
	if err != nil {
		return nil, err
	}
	res, err := runCEL(def.Mapping, ctx)
	if err != nil {
		return nil, err
	}
	// The descriptor ConfigMap, checked against the contracts, and the
	// credentials Secrets of the outputs; then the mapping's objects.
	desc, credentials, err := descriptor(in, ctx, res.instance, res.provides)
	if err != nil {
		return nil, err
	}
	out := &Result{Computed: res.output, Values: map[string]map[string]any{}, Objects: []map[string]any{desc}, Dependencies: deps}
	for _, o := range append(credentials, res.objects...) {
		out.Objects = append(out.Objects, asMap(o))
	}
	// The okdp chart (okdp-objects) renders them: appended to the objects the
	// mapping computed for it, and rendered even when the mapping left it out.
	if _, ok := svc.Charts[ObjectsChart]; ok {
		okdp := asMap(res.output[ObjectsChart])
		objects, isList := okdp["objects"].([]any)
		if okdp["objects"] != nil && !isList {
			return nil, fmt.Errorf("mapping output %s.objects: must be a list of objects", ObjectsChart)
		}
		for _, o := range out.Objects {
			objects = append(objects, deepCopy(o))
		}
		okdp["objects"] = objects
		res.output[ObjectsChart] = okdp
	}
	// Charts left out by the mapping ($if) are not rendered. Every chart's
	// schema violations are reported together.
	violations := map[string]string{}
	for chart, v := range res.output {
		if _, ok := svc.Charts[chart]; !ok {
			return nil, fmt.Errorf("mapping output %q: not a chart of service.yaml", chart)
		}
		f, bad, err := finalValues(in, svc, chart, asMap(v), values)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", chart, err)
		}
		out.Values[chart] = f
		violations["vendor/"+chart] = bad
	}
	if err := schemaError("compiled values", violations); err != nil {
		return nil, err
	}
	return out, nil
}
