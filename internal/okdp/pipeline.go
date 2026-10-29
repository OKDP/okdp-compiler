// Pipeline: what a compile reads besides the package (Inputs), what it
// produces (Result), and the order of its stages (Compile).

package okdp

import (
	"context"
	"fmt"
	"io/fs"
	"slices"

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
	Charts    fs.FS              // chart cache, for the checks only (nil: none): the upstream charts unpacked, <repository>/<chart>-<version>/<chart>/ (helm pull --untar), <repository> the URL without its scheme; the final values are checked against their charts' defaults and schemas, and are the same without it
	Contracts fs.FS              // the contract schemas: <contract>.schema.json
	Context   []ContextLayer     // the context's values layers: global, global.okdp and connections among them, an instance's own connections at a level of its own; merged by level, lowest first, a level's layers in order (helm -f); a layer changes none of a lower level's values (contextLayers)
	Values    []map[string]any   // instance values layers, in order, after Context: parameters and upstream values, no global but the keys the parameter schema declares, no connections (valuesLayers)
	Release   string             // the Helm release: a DNS-1123 label of 53 characters at most
	Namespace string             // the release's namespace: a DNS-1123 label
	Published map[string]Info    // what the other instances of the namespace publish (their okdp.info.yaml), by release: a connection reference may name one of their outputs; nil when unknown: only connections and claims resolve
	Claims    []Claim            // the claims bound to the instance, of contracts its package provisions: the mapping's claims; it publishes an output of each's contract named its ID (CompileAll sets them)
	Bindings  map[string]Binding // the provider each claim of a reference is bound to, by parameter path, and the output it publishes for it (CompileAll sets them); nil: no claim resolves
	CostLimit uint64             // the total CEL cost the expressions of the compile may use, the mapping's and the compiler's templates' (0: no limit): past it, the compile fails; a server bounds what a package's expressions compute over the values an instance gives
}

// ContextLayer: a values layer of the context (Inputs.Context), see
// chart.ParseValues, at its level: 0, the platform's, then each level adds
// to those below it (a project's, an instance's own connections...).
type ContextLayer struct {
	Level  uint
	Values map[string]any
}

// Result: what a compile produces.
type Result struct {
	// Values: chart -> the final values the upstream chart receives (helm
	// -f): OKDP's defaults, the computed values and the instance's upstream
	// values, not the chart's own defaults, which Helm applies.
	Values map[string]map[string]any
	// Charts: chart -> the upstream chart that renders its values (`helm
	// template <Charts[chart]> -f <Values[chart]>`): its dependency of
	// Chart.yaml, the platform's oidc-dcr chart for an OAuth client.
	Charts map[string]chart.Ref
	// Objects: OKDP's own objects, rendered whole: the credentials Secrets
	// of the provided connections, then the objects section of the mapping,
	// then the PushSecrets and ExternalSecrets of the credentials of claims
	// across namespaces;
	// the umbrella's objects file, which it renders as written, in its
	// release (helm.Write). nil when there is none.
	Objects []map[string]any
	// Info: what the instance publishes, its instance and provides
	// sections, checked (okdp.info.yaml, helm.Write).
	Info Info
	// Dependencies: the instances the connection references point to, by
	// one of their outputs (Inputs.Published) or a claim (Inputs.Bindings),
	// by parameter path. References to external
	// connections are not listed.
	Dependencies []Dependency
	// Release, Namespace: the Helm release the values are compiled for, and
	// its namespace (Inputs.Release, Inputs.Namespace).
	Release, Namespace string
	// Service, Version: the name and version of Chart.yaml, those of the
	// umbrella chart.
	Service, Version string
	// Chart: the package's Chart.yaml, as read (Definition.Chart): the
	// umbrella chart's metadata, its dependencies in their order.
	Chart chart.Metadata
	// Warnings: what the compile warns of without failing, those of the
	// package (Definition.Warnings): a values.yaml that holds expressions;
	// with a chart cache, an instance list appended to that leaves out
	// items of the chart's default list.
	Warnings []string
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
	// Checked again: a Definition may be built in memory.
	svc, err := def.service()
	if err != nil {
		return nil, err
	}
	if err := checkClaims(svc, in.Claims); err != nil {
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
	// The chart the platform renders the OAuth client charts with.
	dcrSource, err := platformCharts(inst.okdp)
	if err != nil {
		return nil, err
	}
	// 5. Mapping.
	res, written, err := runMapping(session, inst.scope, def.sources()...)
	if err != nil {
		return nil, err
	}
	// 6. OAuth clients (oidcClients, oidc.go): checked in every mode; in dcr
	// mode, the values of the oidc-dcr chart of each.
	clients, dcrValues, err := oidcClients(session, inst, value.AsList(res["oidcClients"]))
	if err != nil {
		return nil, err
	}
	// 7. Provides: the outputs, checked against the contracts, and their
	// credentials Secrets; an output for each claim (claimed).
	outputs, credentials, err := provided(session, inst, in.Contracts, value.AsList(res["provides"]))
	if err != nil {
		return nil, err
	}
	if err := claimed(in.Claims, outputs); err != nil {
		return nil, err
	}
	pushes, err := pushSecrets(inst, in.Contracts, in.Claims, outputs)
	if err != nil {
		return nil, err
	}
	// 8. Objects: the credentials Secrets, then the mapping's objects,
	// then those of the credentials of claims across namespaces: pushed for
	// the claims of another namespace, read for its own of another.
	out := &Result{Objects: credentials, Info: published(value.AsMap(res["instance"]), outputs), Dependencies: inst.deps, Release: in.Release, Namespace: in.Namespace, Service: svc.Name, Version: svc.Version, Chart: def.Chart, Warnings: slices.Clone(def.Warnings)}
	for _, o := range value.AsList(res["objects"]) {
		out.Objects = append(out.Objects, value.AsMap(o))
	}
	out.Objects = append(out.Objects, pushes...)
	for _, x := range inst.transfers {
		out.Objects = append(out.Objects, pullSecret(inst, x))
	}
	// 9. Final values, first the dependencies of Chart.yaml the umbrella
	// renders (rendered): all but those the values file leaves out. Then
	// the compiler's own charts: the chart of each OAuth client, compiled in
	// dcr mode; neither Chart.yaml nor the values file may list them. Added
	// to the dependencies of Chart.yaml, without changing them.
	output := rendered(svc.Charts, written, value.AsMap(res["output"]))
	specs, err := addOwnCharts(def.Chart.Dependencies, svc.Charts, def.valuesName(), output, clientCharts(clients, dcrValues, dcrSource))
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Then the final values of each chart rendered; with a chart cache,
	// checked against its upstream chart (10. Upstream charts).
	var warnings []string
	if out.Values, out.Charts, warnings, err = chart.FinalValues(in.Charts, def.Files, specs, def.valuesName(), output, inst.values); err != nil {
		return nil, err
	}
	out.Warnings = append(out.Warnings, warnings...)
	return out, nil
}

// rendered: the computed values of the dependencies the umbrella renders,
// by key, Helm's rule: every dependency of Chart.yaml (specs), but one
// whose key the values file writes (written, its literal keys, or a key of
// output, as a top-level $merge or $for gives it) that is absent from
// output ($if) or null; one it does not write computed {}: its chart's
// defaults, OKDP's defaults and its upstream values still apply. A key of
// output that is not a dependency is kept, null or not, for addOwnCharts
// and chart.FinalValues to refuse. output is not changed.
func rendered(specs chart.Specs, written []string, output M) M {
	out := M{}
	for k, v := range output {
		if _, dep := specs[k]; v != nil || !dep {
			out[k] = v
		}
	}
	for k := range specs {
		if _, ok := output[k]; !ok && !slices.Contains(written, k) {
			out[k] = M{}
		}
	}
	return out
}
