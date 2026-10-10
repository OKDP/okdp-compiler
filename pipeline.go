// Pipeline: what a compile reads besides the package (Inputs), what it
// produces (Result), and the order of its stages (Compile).

package compiler

import (
	"context"
	"errors"
	"fmt"
	"io/fs"

	"github.com/okdp/okdp-compiler/internal/chart"
	"github.com/okdp/okdp-compiler/internal/compile"
	"github.com/okdp/okdp-compiler/internal/helmfn"
	"github.com/okdp/okdp-compiler/internal/value"
)

// M: a values map (value.M).
type M = value.M

// Inputs: what one compile reads besides the package. Its values layers,
// Platform, Project and Values, may hold Go values of other types than
// map[string]any and []any (map[string]string, []string, int32, a struct
// by its json tags): each is read as the values it would be once written to
// a values file and read by Helm; what values cannot hold (a func, a NaN)
// fails the compile, its layer and path named.
type Inputs struct {
	Charts    fs.FS            // chart cache: the upstream charts, <repository>/<chart>-<version>.tgz (helm pull) or <repository>/<chart>-<version>/, <repository> the URL without its scheme
	Contracts fs.FS            // the contract schemas: <contract>.schema.json
	Platform  []map[string]any // platform values layers, in order (helm -f): global, global.okdp among them; see chart.ParseValues; Go values of any type, read as Helm values (value.Normalize)
	Project   []map[string]any // project values layers, in order, after Platform: what the instance's project (its namespace) adds to the platform's values, never changing one (valuesLayers)
	Values    []map[string]any // instance values layers, in order, after Project: no global but the keys the parameter schema declares (valuesLayers)
	Release   string           // the Helm release: a DNS-1123 label of 53 characters at most
	Namespace string           // the release's namespace: a DNS-1123 label
	CostLimit uint64           // the total CEL cost the expressions of the compile may use, the conditions of require, the mapping's and the OAuth client templates' (0: no limit): past it, the compile fails; a server bounds what a package's expressions compute over the values an instance gives
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

// Compile runs the mapping of def on one instance, cancelled with ctx
// (between stages, and within the evaluation of an expression). It is safe
// for concurrent use: it keeps no state between calls.
func Compile(ctx context.Context, def *Definition, in Inputs) (*Result, error) {
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
	session := compile.NewSession(ctx, in.CostLimit, functions(inst)...)
	// 4. Platform requirements: the platform values the service requires.
	if err := checkRequirements(session, svc.Require, inst); err != nil {
		return nil, err
	}
	// The charts the platform renders the compiler's own charts with.
	objectsSource, dcrSource, err := platformCharts(inst.okdp)
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
	// mapping's objects.
	out := &Result{Objects: append([]M{desc}, credentials...), Dependencies: inst.deps, Release: in.Release, Namespace: in.Namespace, Service: svc.Name, Version: svc.Version}
	for _, o := range res.objects {
		out.Objects = append(out.Objects, value.AsMap(o))
	}
	// 9. Final values, first the compiler's own charts: the chart of each
	// OAuth client, compiled in dcr mode, and the okdp chart, which renders
	// the objects unless the caller deploys them otherwise; neither
	// service.yaml nor the mapping's output may list them. Added to those of
	// service.yaml, without changing them.
	ownCharts := append(clientCharts(clients, dcrValues, dcrSource), objectsChart(out.Objects, objectsSource))
	specs, err := addOwnCharts(svc.Charts, res.output, ownCharts)
	if err != nil {
		return nil, err
	}
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
