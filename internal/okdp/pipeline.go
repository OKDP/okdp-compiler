// Package okdp compiles an OKDP service instance into the values of its
// upstream charts with a YAML + CEL mapping (package compile), plus the OKDP
// objects of the instance (its descriptor ConfigMap, the credentials Secrets
// of the connections it provides, and the objects section of the mapping).
// It holds OKDP's rules: the platform context derived from global.okdp, the
// connections and their contracts, the descriptor, the OAuth clients, the
// okdp chart; and the order of the stages (Compile).
//
// The values are read, merged and checked against the charts' schemas as
// Helm does (package chart; testdata/helm holds what `helm template`
// computes on the same inputs), and the mapping's host functions reproduce
// Helm and okdp-lib-chart functions.
//
// Each chart of service.yaml names the upstream chart that renders its values
// (chart.Spec.Source: repository, chart, version), and Result.Charts maps
// every compiled chart to it: a deployer renders the upstream chart from its
// repository. The compiler reads only its defaults and schema, from the chart
// cache (Inputs.Charts); one upstream chart may render several charts of
// service.yaml, each with its own values.
//
// A mapping's oidcClients section declares the service's OAuth clients
// (oidc.go): when the platform registers them by dynamic client
// registration, the compiler adds one chart per client, rendered by the
// oidc-dcr chart (oidc-dcr for the main client, <name>-oidc-dcr for a named
// one), which service.yaml does not list; their values come from a template
// in the mapping language embedded in the compiler (oidc-dcr.cel.yaml).
//
// An instance may set any value of a chart whose service.yaml entry accepts
// it (upstream), under upstream.<chart> of its values. The mapping sees them
// (values.upstream.<chart>), so that a rule of its errors section covers
// them; the compiler then merges them over the computed values, refusing
// the protect paths and appending to the append lists (chart.FinalValues). A path names
// a key that contains dots in brackets, and a family of keys with a final
// glob: annotations["nginx.ingress.kubernetes.io/auth-*"]. A chart rendered
// by the same upstream chart as another may receive the upstream values of
// the other (upstream: {from: <chart>}), with its protect and append: one
// set of values for both renders.
//
// Everything is read through fs.FS and in-memory values, so a caller (a
// server, a CLI) can compile from a checkout, an embedded
// filesystem or a git tree without touching the disk, through the public
// package at the module's root (compiler).
package okdp

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"slices"

	"github.com/okdp/okdp-compiler/internal/chart"
	"github.com/okdp/okdp-compiler/internal/compile"
	"github.com/okdp/okdp-compiler/internal/helmfn"
	"github.com/okdp/okdp-compiler/internal/value"
)

// M: a values map (value.M).
type M = value.M

// Inputs: what one compile reads besides the package.
type Inputs struct {
	Charts    fs.FS            // chart cache: the upstream charts, <repository>/<chart>-<version>.tgz (helm pull) or <repository>/<chart>-<version>/, <repository> the URL without its scheme
	Contracts fs.FS            // the contract schemas: <contract>.schema.json
	Platform  []map[string]any // platform values layers, in order (helm -f): global, global.okdp among them; see chart.ParseValues; Go values of any type, read as Helm values (value.Normalize)
	Project   []map[string]any // project values layers, in order, after Platform: what the instance's project (its namespace) adds to the platform's values, never changing one (valuesLayers)
	Values    []map[string]any // instance values layers, in order, after Project: no global but the keys the parameter schema declares (valuesLayers)
	Release   string           // the Helm release: a DNS-1123 label of 53 characters at most
	Namespace string           // the release's namespace: a DNS-1123 label
}

// Result: what a compile produces.
type Result struct {
	// Values: chart -> the final values the upstream chart receives.
	Values map[string]map[string]any
	// Charts: chart -> the upstream chart that renders its values (`helm
	// template <Charts[chart]> -f <Values[chart]>`): its service.yaml
	// source, the platform's oidc-dcr chart for an OAuth client, the
	// platform's objects chart for okdp.
	Charts map[string]chart.Ref
	// ClientCharts: the keys of Values and Charts that are the charts of the
	// OAuth clients (dcr mode: oidc-dcr, <name>-oidc-dcr), sorted; none in
	// another mode. They are installed before the others, their Job writing
	// the Secret the others read (fsio.Install).
	ClientCharts []string
	// Objects: OKDP's own objects, rendered whole: the descriptor ConfigMap,
	// the credentials Secrets of the provided connections, then the objects
	// section of the mapping; also the values of the okdp chart
	// (ObjectsChart).
	Objects []map[string]any
	// Dependencies: the instances the connection references point to, by
	// parameter path. References to external connections are not listed.
	Dependencies []Dependency
	// Release, Namespace: the Helm release the values are compiled for, and
	// its namespace (Inputs.Release, Inputs.Namespace).
	Release, Namespace string
	// Service, Version: the name and version of service.yaml, those of the
	// umbrella chart.
	Service, Version string
}

// Option: a setting of CompileContext.
type Option func(*settings)

// settings: what the options of a compile set.
type settings struct {
	costLimit      uint64 // 0: no limit
	withoutObjects bool
}

// WithCostLimit: the total CEL cost the expressions of one compile may use,
// the conditions of require, the mapping's and the OAuth client templates'
// (0, the default: no limit).
// Past the limit, the compile fails.
func WithCostLimit(limit uint64) Option {
	return func(s *settings) { s.costLimit = limit }
}

// WithoutObjectsChart: no okdp chart (ObjectsChart) in the result, for a
// caller that deploys OKDP's own objects (Result.Objects) otherwise;
// global.okdp.charts.objects is then not required.
func WithoutObjectsChart() Option {
	return func(s *settings) { s.withoutObjects = true }
}

// Compile runs the mapping of def on one instance: CompileContext without a
// context or options.
func Compile(def *Definition, in Inputs) (*Result, error) {
	return CompileContext(context.Background(), def, in)
}

// CompileContext runs the mapping of def on one instance, cancelled with ctx
// (between stages, and within the evaluation of an expression), with the
// options opts. It is safe for concurrent use: it keeps no state between
// calls.
func CompileContext(ctx context.Context, def *Definition, in Inputs, opts ...Option) (*Result, error) {
	var set settings
	for _, o := range opts {
		o(&set)
	}
	switch {
	case ctx == nil:
		return nil, errors.New("no context")
	case def == nil:
		return nil, errors.New("no service definition")
	case def.Files == nil:
		return nil, errors.New("Definition.Files is required")
	case in.Charts == nil:
		return nil, errors.New("Inputs.Charts is required")
	case in.Contracts == nil:
		return nil, errors.New("Inputs.Contracts is required")
	case in.Release == "":
		return nil, errors.New("Inputs.Release is required")
	case in.Namespace == "":
		return nil, errors.New("Inputs.Namespace is required")
	case !helmfn.IsRelease(in.Release):
		return nil, fmt.Errorf("Inputs.Release %q: a release name, lowercase letters, digits and \"-\", starting and ending with a letter or a digit, %d characters at most (Helm's limit)", in.Release, helmfn.ReleaseMax)
	case !helmfn.IsDNSLabel(in.Namespace):
		return nil, fmt.Errorf("Inputs.Namespace %q: a namespace name, lowercase letters, digits and \"-\", starting and ending with a letter or a digit, 63 characters at most", in.Namespace)
	}
	svc := &def.Service
	// Checked again: a Definition may be built in memory.
	if err := svc.Check(); err != nil {
		return nil, err
	}
	// 1. Parameters: the values layers, merged and checked against the
	// package's parameter schema; the instance of the compile, which the
	// stages below complete and read (context.go).
	inst, err := parameters(def.Files, in, svc)
	if err != nil {
		return nil, err
	}
	// 2. Platform context: global.okdp checked, its OIDC settings derived.
	if err := platformContext(inst); err != nil {
		return nil, err
	}
	// 3. Connections: the references resolved against the contracts, each
	// read once, by the references and the outputs; then what the mapping
	// sees.
	contracts := newContractCache(in.Contracts)
	if err := connections(inst, contracts); err != nil {
		return nil, err
	}
	// The compile's CEL session, with OKDP's host functions for the
	// instance: the conditions of require, the mapping and the client
	// templates are evaluated in it, cancelled with ctx, their cost counted
	// together.
	session := compile.NewSession(ctx, set.costLimit, functions(inst)...)
	// 4. Platform requirements: the platform values the service requires.
	if err := checkRequirements(session, svc.Require, inst); err != nil {
		return nil, err
	}
	// The charts the platform renders the compiler's own charts with.
	objectsSource, dcrSource, err := platformCharts(inst.okdp, !set.withoutObjects)
	if err != nil {
		return nil, err
	}
	// 5. Mapping.
	res, err := runMapping(session, def.Mapping, inst.scope)
	if err != nil {
		return nil, err
	}
	// 6. OAuth clients (oidcClients, oidc.go): checked in every mode; in dcr
	// mode, the values of the oidc-dcr chart of each.
	clients, dcrValues, err := oidcClients(session, inst, res.clients)
	if err != nil {
		return nil, err
	}
	// 7. Descriptor: the descriptor ConfigMap, checked against the
	// contracts, and the credentials Secrets of the outputs.
	desc, credentials, err := descriptor(inst, contracts, res.instance, res.provides)
	if err != nil {
		return nil, err
	}
	// 8. Objects: the descriptor, the credentials Secrets, then the
	// mapping's objects, no two of them one object of the cluster.
	own := append([]ownObject{{"the descriptor", desc}}, credentials...)
	for i, o := range res.objects {
		own = append(own, ownObject{fmt.Sprintf("objects[%d]", i), value.AsMap(o)})
	}
	if err := uniqueObjects(own, in.Namespace); err != nil {
		return nil, err
	}
	out := &Result{Dependencies: inst.deps, Release: in.Release, Namespace: in.Namespace, Service: svc.Name, Version: svc.Version}
	for _, o := range own {
		out.Objects = append(out.Objects, o.object)
	}
	// 9. Final values, first the compiler's own charts: the chart of each
	// OAuth client, compiled in dcr mode, and the okdp chart, which renders
	// the objects unless the caller deploys them otherwise; neither
	// service.yaml nor the mapping's output may list them. Added to those of
	// service.yaml, without changing them.
	ownCharts := clientCharts(clients, dcrValues, dcrSource)
	if !set.withoutObjects {
		ownCharts = append(ownCharts, objectsChart(out.Objects, objectsSource))
	}
	specs, err := addOwnCharts(svc.Charts, res.output, ownCharts)
	if err != nil {
		return nil, err
	}
	out.ClientCharts = slices.Sorted(maps.Keys(dcrValues))
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Then the final values of each chart of the output, checked against
	// its upstream chart's schema (10. Upstream schemas); charts left out by
	// the mapping ($if) are not rendered.
	if out.Values, out.Charts, err = chart.FinalValues(in.Charts, def.Files, specs, res.output, inst.values); err != nil {
		return nil, err
	}
	return out, nil
}
