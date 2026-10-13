// Pipeline: what a compile reads besides the package (Inputs), what it
// produces (Result), and the order of its stages (Compile).

package okdp

import (
	"context"
	"fmt"
	"io/fs"

	"github.com/okdp/okdp-compiler/internal/chart"
	"github.com/okdp/okdp-compiler/internal/compile"
	"github.com/okdp/okdp-compiler/internal/value"
)

// M: a values map (value.M).
type M = value.M

// Inputs: what one compile reads besides the package. Its values layers,
// those of Context and Values, may hold Go values of other types than
// map[string]any and []any (map[string]string, []string, int32, a struct
// by its json tags): each is read as the values it would be once written to
// a values file and read by Helm; what values cannot hold (a func, a NaN)
// fails the compile, its layer and path named.
type Inputs struct {
	Charts    fs.FS            // chart cache: the upstream charts unpacked, <repository>/<chart>-<version>/<chart>/ (helm pull --untar), <repository> the URL without its scheme
	Contracts fs.FS            // the contract schemas: <contract>.schema.json
	Context   []ContextLayer   // the context's values layers: global, global.okdp and connections among them; merged by level, lowest first, a level's layers in order (helm -f); a layer changes none of a lower level's values (contextLayers)
	Values    []map[string]any // instance values layers, in order, after Context: no global but the keys the parameter schema declares (valuesLayers)
	Release   string           // the Helm release: a DNS-1123 label of 53 characters at most
	Namespace string           // the release's namespace: a DNS-1123 label
	CostLimit uint64           // the total CEL cost the expressions of the compile may use, the mapping's and the compiler's templates' (0: no limit): past it, the compile fails; a server bounds what a package's expressions compute over the values an instance gives
}

// ContextLayer: a values layer of the context (Inputs.Context), see
// chart.ParseValues, at its level: 0, the platform's, then each level adds
// to those below it (a project's, a team's...).
type ContextLayer struct {
	Level  uint
	Values map[string]any
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
	// ctx, def and the file systems are required: nil panics.
	switch {
	case !chart.IsRelease(in.Release):
		return nil, fmt.Errorf("Inputs.Release %q: a release name, lowercase letters, digits and \"-\", starting and ending with a letter or a digit, %d characters at most (Helm's limit)", in.Release, chart.ReleaseMax)
	case !chart.IsDNSLabel(in.Namespace):
		return nil, fmt.Errorf("Inputs.Namespace %q: a namespace name, lowercase letters, digits and \"-\", starting and ending with a letter or a digit, 63 characters at most", in.Namespace)
	}
	svc := &def.Service
	// Checked again: a Definition may be built in memory.
	if err := svc.Check(); err != nil {
		return nil, err
	}
	// 1-3. Parameters, platform context, connections: the instance of the
	// compile, which the stages below read (context.go).
	inst, err := newInstance(def.Files, in, svc)
	if err != nil {
		return nil, err
	}
	// 4. Platform requirements: the global.okdp keys the service requires,
	// in order, each set and not empty (not null, false, "", 0, [] or {}).
	for _, k := range svc.Require {
		if !value.Truthy(value.Dig(inst.okdp, k)) {
			return nil, fmt.Errorf("global.okdp.%s is required. %s", k, platformValue)
		}
	}
	// The compile's CEL session, with OKDP's host functions for the
	// instance: the mapping and the compiler's templates are evaluated in
	// it, cancelled with ctx, their cost counted together;
	// okdpGeneratedSecret evaluates its template in it too.
	var session *compile.Session
	session = compile.NewSession(ctx, in.CostLimit, functions(inst, &session)...)
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
	clients, dcrValues, err := oidcClients(session, inst, value.AsList(res["oidcClients"]))
	if err != nil {
		return nil, err
	}
	// 7. Descriptor: the descriptor ConfigMap, checked against the
	// contracts, and the credentials Secrets of the outputs.
	desc, credentials, err := descriptor(session, inst, in.Contracts, value.AsMap(res["instance"]), value.AsList(res["provides"]))
	if err != nil {
		return nil, err
	}
	// 8. Objects: the descriptor, the credentials Secrets, then the
	// mapping's objects.
	out := &Result{Objects: append([]M{desc}, credentials...), Dependencies: inst.deps, Release: in.Release, Namespace: in.Namespace, Service: svc.Name, Version: svc.Version}
	for _, o := range value.AsList(res["objects"]) {
		out.Objects = append(out.Objects, value.AsMap(o))
	}
	// 9. Final values, first the compiler's own charts: the chart of each
	// OAuth client, compiled in dcr mode, and the okdp chart, which renders
	// the objects unless the caller deploys them otherwise; neither
	// service.yaml nor the mapping's output may list them. Added to those of
	// service.yaml, without changing them.
	ownCharts := append(clientCharts(clients, dcrValues, dcrSource), objectsChart(out.Objects, objectsSource))
	output := value.AsMap(res["output"])
	specs, err := addOwnCharts(svc.Charts, output, ownCharts)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Then the final values of each chart of the output, checked against
	// its upstream chart's schema (10. Upstream schemas); charts left out by
	// the mapping ($if) are not rendered.
	if out.Values, out.Charts, err = chart.FinalValues(in.Charts, def.Files, specs, output, inst.values); err != nil {
		return nil, err
	}
	return out, nil
}
