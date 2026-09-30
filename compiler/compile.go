// Package compiler compiles an OKDP service instance into the values of its
// upstream charts with a YAML + CEL mapping, plus the OKDP objects of the
// instance (its descriptor ConfigMap, the credentials Secrets of the
// connections it provides, and the objects section of the mapping).
//
// The values are read, merged and checked against the charts' schemas as
// Helm does (values.go, merge.go, schema.go; testdata/helm holds what
// `helm template` computes on the same inputs), and the mapping's host
// functions reproduce Helm and okdp-lib-chart functions.
//
// A mapping's oidcClients section declares the service's OAuth clients
// (oidc.go): when the platform registers them by dynamic client
// registration, the compiler adds one chart per client, compiled from
// vendor/oidc-dcr (oidc-dcr for the main client, <name>-oidc-dcr for a named
// one), which service.yaml does not list; their values come from a template
// in the mapping language embedded in the compiler (oidc-dcr.cel.yaml). A
// chart of service.yaml may also name the vendored chart it compiles from
// (ChartSpec.Chart); Result.Charts maps every compiled chart to the vendored
// chart that renders it.
//
// An instance may set any value of a chart whose service.yaml entry accepts
// it (upstream): in its files (Inputs.Instance, upstream.<chart>.yaml by
// default), then inline (upstream.<chart> of its values). The compiler reads
// both before the mapping runs and shows them merged as values.upstream.<chart>,
// so that a rule of the mapping's errors section covers a value whatever its
// source; it then merges them over the computed values, refusing the protect
// paths and appending to the append lists (merge.go). A path names a key that
// contains dots in brackets, and a family of keys with a final glob:
// annotations["nginx.ingress.kubernetes.io/auth-*"] (paths.go). A chart
// rendered twice from one vendored chart may receive the upstream values of
// the other (upstream: {from: <chart>}), with its protect and append: one set
// of values for both renders.
//
// Everything is read through fs.FS and in-memory values, so a caller (the
// control plane server, a CLI) can compile from a checkout, an embedded
// filesystem or a git tree without touching the disk:
//
//	def, err := compiler.LoadDefinition(os.DirFS("packages/myservice"))
//	res, err := compiler.Compile(def, compiler.Inputs{
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
	"maps"
	"path"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// Definition: a package, as LoadDefinition reads it: service.yaml, its
// mapping, and the package's other files.
type Definition struct {
	Service Service
	Mapping []byte // mapping.cel.yaml
	Files   fs.FS  // the package: values.yaml, values.schema.json, the base files
}

// LoadDefinition reads the package at the root of fsys: service.yaml and
// mapping.cel.yaml; each compile reads its other files.
func LoadDefinition(fsys fs.FS) (*Definition, error) {
	b, err := fs.ReadFile(fsys, "service.yaml")
	if err != nil {
		return nil, err
	}
	def := &Definition{Files: fsys}
	if err := yaml.Unmarshal(b, &def.Service); err != nil {
		return nil, fmt.Errorf("service.yaml: %w", err)
	}
	if err := def.Service.check(); err != nil {
		return nil, err
	}
	if def.Mapping, err = fs.ReadFile(fsys, "mapping.cel.yaml"); err != nil {
		return nil, err
	}
	return def, nil
}

// Inputs: what one compile reads besides the package.
type Inputs struct {
	Instance  fs.FS            // instance files: the upstream values files of service.yaml (optional)
	Contracts fs.FS            // okdp-lib-chart/contracts: <contract>.schema.json
	Values    []map[string]any // instance values layers, in order (helm -f); see ParseValues
	Release   string
	Namespace string
}

// Service: service.yaml, the engine-neutral part of a package.
type Service struct {
	Name       string               `yaml:"name"`       // the service: labels, descriptor, parameter schema errors
	Version    string               `yaml:"version"`    // the package's version: descriptor
	AppVersion string               `yaml:"appVersion"` // the application's version: label app.kubernetes.io/version
	Roles      []string             `yaml:"roles"`      // catalog: what the service provides (not read by the compiler)
	DependsOn  []string             `yaml:"dependsOn"`  // catalog: the platform services it needs (not read by the compiler)
	Charts     map[string]ChartSpec `yaml:"charts"`
	Require    []string             `yaml:"require"` // global.okdp keys the service needs
}

// meta: what the mapping and OKDP's objects see of the package.
func (s *Service) meta() serviceMeta {
	return serviceMeta{Name: s.Name, Version: s.Version, AppVersion: s.AppVersion}
}

// ChartSpec: one upstream chart of a service, by key: the key names its
// values (the mapping's output, Result.Values).
type ChartSpec struct {
	Chart    string   `yaml:"chart"`    // the vendored chart, vendor/<chart> (its defaults and schema, and what renders the values); default: the key
	Base     string   `yaml:"base"`     // a values file of the package, base/<key>.yaml by convention (OKDP's default values)
	Protect  []string `yaml:"protect"`  // instance upstream values: protected paths (paths.go: dotted keys, ["literal.key"], ["prefix*"])
	Append   []string `yaml:"append"`   // instance upstream values: appended lists (same paths)
	Upstream Upstream `yaml:"upstream"` // instance upstream values accepted, and their files
}

// Upstream: the user-defined values a chart accepts. true reads
// upstream.<chart>.yaml of the instance when it exists; a path, or a list of
// paths, reads these files instead (they must exist), merged in order. The
// instance's inline upstream.<chart> values come after the files.
//
// {from: <chart>} gives the chart the upstream values of another chart of
// service.yaml, with that chart's protect and append: for two renders of
// one upstream chart (an alias, ChartSpec.Chart) that an instance sets with
// one set of values (polaris-admin's bootstrap and principals Jobs). The
// mapping used to merge them into the second render by hand, which could
// not see the instance files, so the files had to be refused.
type Upstream struct {
	Enabled bool
	Files   []string // nil: upstream.<chart>.yaml, if it exists
	From    string   // the chart whose upstream values this one receives (Enabled is false)
}

// upstreamOf: the chart whose instance upstream values the chart key
// receives: itself when it accepts them, the chart its upstream.from names,
// or "" when it receives none.
func (s *Service) upstreamOf(key string) string {
	switch spec := s.Charts[key]; {
	case spec.Upstream.From != "":
		return spec.Upstream.From
	case spec.Upstream.Enabled:
		return key
	}
	return ""
}

// check: what service.yaml may not hold, refused when it is loaded
// (LoadDefinition) and before every compile: no name or version, a chart
// alias that is not the name of a vendored chart, a protect or append path
// that does not follow the grammar of paths.go (a malformed path would
// otherwise surface only when an instance sets a value under it, or never,
// leaving a value unprotected), an upstream.from that does not name a chart
// accepting upstream values of its own.
func (s *Service) check() error {
	switch {
	case s.Name == "":
		return errors.New("service.yaml: name is required")
	case s.Version == "":
		return errors.New("service.yaml: version is required")
	}
	for _, key := range slices.Sorted(maps.Keys(s.Charts)) {
		spec := s.Charts[key]
		if c := spec.Chart; c != "" && (c != path.Base(c) || c == "." || c == ".." || strings.TrimSpace(c) != c) {
			return fmt.Errorf("service.yaml: charts.%s.chart %q: not the name of a vendored chart (vendor/<chart>)", key, c)
		}
		if from := spec.Upstream.From; from != "" {
			target, ok := s.Charts[from]
			switch {
			case from == key:
				return fmt.Errorf("service.yaml: charts.%s.upstream.from: the chart itself (upstream: true accepts its own values)", key)
			case !ok:
				return fmt.Errorf("service.yaml: charts.%s.upstream.from %q: not a chart of service.yaml", key, from)
			case !target.Upstream.Enabled:
				return fmt.Errorf("service.yaml: charts.%s.upstream.from %q: that chart accepts no upstream values of its own (upstream: true, or files)", key, from)
			// One set of values, one set of rules: what the instance may set
			// is decided once, on the chart that owns the values.
			case len(spec.Protect) > 0 || len(spec.Append) > 0:
				return fmt.Errorf("service.yaml: charts.%s: protect and append come with upstream.from, from charts.%s: list them there", key, from)
			}
		}
		for _, l := range []struct {
			name  string
			paths []string
		}{{"protect", spec.Protect}, {"append", spec.Append}} {
			for i, p := range l.paths {
				if _, err := parsePath(p); err != nil {
					return fmt.Errorf("service.yaml: charts.%s.%s[%d] %q: %w", key, l.name, i, p, err)
				}
			}
		}
	}
	return nil
}

// vendored: the vendored chart of the chart key: its chart alias, or the
// key itself.
func (c ChartSpec) vendored(key string) string {
	if c.Chart != "" {
		return c.Chart
	}
	return key
}

// UnmarshalYAML: true or false, a path or a list of paths, or {from:
// <chart>}.
func (u *Upstream) UnmarshalYAML(n *yaml.Node) error {
	switch {
	case n.Kind == yaml.MappingNode:
		if len(n.Content) != 2 || n.Content[0].Value != "from" || n.Content[1].Kind != yaml.ScalarNode || n.Content[1].Tag != "!!str" || n.Content[1].Value == "" {
			break
		}
		u.From = n.Content[1].Value
		return nil
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
	return fmt.Errorf("line %d: upstream must be true, false, a path, a list of paths or {from: <chart>}", n.Line)
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
	// Charts: chart -> the vendored chart that renders its values
	// (`helm template vendor/<Charts[chart]> -f <Values[chart]>`): the chart
	// itself, or the vendored chart its service.yaml entry names (`chart`).
	Charts map[string]string
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
	case def.Files == nil:
		return nil, errors.New("Definition.Files is required")
	case in.Contracts == nil:
		return nil, errors.New("Inputs.Contracts is required")
	case in.Release == "":
		return nil, errors.New("Inputs.Release is required")
	case in.Namespace == "":
		return nil, errors.New("Inputs.Namespace is required")
	}
	svc := &def.Service
	// Checked again: a Definition may be built in memory.
	if err := svc.check(); err != nil {
		return nil, err
	}
	// What the mapping sees: values, platform context, resolved connections.
	ctx, values, ups, deps, err := buildContext(def.Files, in, svc)
	if err != nil {
		return nil, err
	}
	res, err := runCEL(def.Mapping, ctx)
	if err != nil {
		return nil, err
	}
	// The OAuth clients (oidcClients, oidc.go): checked in every mode; in dcr
	// mode, the oidc-dcr chart of each, added to the charts of service.yaml
	// and to the mapping's output.
	clients, dcrValues, err := oidcClients(ctx, res.clients)
	if err != nil {
		return nil, err
	}
	if svc, err = clientCharts(def.Files, svc, clients, dcrValues, res.output); err != nil {
		return nil, err
	}
	// The descriptor ConfigMap, checked against the contracts, and the
	// credentials Secrets of the outputs; then the mapping's objects.
	desc, credentials, err := descriptor(in, ctx, res.instance, res.provides)
	if err != nil {
		return nil, err
	}
	out := &Result{Computed: res.output, Values: map[string]map[string]any{}, Charts: map[string]string{}, Objects: []map[string]any{desc}, Dependencies: deps}
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
	// schema violations are reported together. In key order: the first
	// error is the same from one compile to the next, also when a refused
	// upstream value reaches two charts (upstream.from).
	violations := map[string]string{}
	for _, chart := range slices.Sorted(maps.Keys(res.output)) {
		v := res.output[chart]
		if _, ok := svc.Charts[chart]; !ok {
			return nil, fmt.Errorf("mapping output %q: not a chart of service.yaml", chart)
		}
		f, bad, err := finalValues(def.Files, svc, chart, asMap(v), values, ups)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", chart, err)
		}
		out.Values[chart] = f
		vendored := svc.Charts[chart].vendored(chart)
		out.Charts[chart] = vendored
		if vendored == chart {
			violations["vendor/"+chart] = bad
		} else {
			violations[chart+" (vendor/"+vendored+")"] = bad
		}
	}
	if err := schemaError("compiled values", violations); err != nil {
		return nil, err
	}
	return out, nil
}
