// Pipeline: what a compile reads besides the package (Inputs), what it
// produces (Result), and the order of its stages (Compile).

package okdp

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"time"

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
	Name      string             // the instance's name, a DNS-1123 label: the name of the outputs the mapping names none of, of its claims' IDs (CompileAll sets it, Instance.Name); "": the release
	Namespace string             // the release's namespace: a DNS-1123 label
	Published map[string]Info    // what the other instances of the namespace publish (their okdp.info.yaml), by release: a connection reference may name one of their outputs; nil when unknown: only connections and claims resolve
	Claims    []Claim            // the claims bound to the instance, of contracts its package provisions: the mapping's claims; it publishes an output of each's contract named its ID (CompileAll sets them)
	Claimers  []string           // the namespaces whose instances may claim of the instance, a provider, from another namespace, by naming it or through a class of their project's (CompileAll; a class of the platform's, level 0, needs none): namespace names, once each, or "*", any; nil: the claimers of its package's provisions entry of the claim's contract, none by default (Instance.Claimers); LoadRepository's, its instance.yaml's
	Bindings  map[string]Binding // the provider each claim of a reference is bound to, by parameter path, and the output it publishes for it (CompileAll sets them); nil: no claim resolves
	CostLimit uint64             // the total CEL cost the expressions of the compile may use, the mapping's and the compiler's templates' (0: no limit): past it, the compile fails; an expression may cost what is left of it, not the limit again; a server bounds what a package's expressions compute over the values an instance gives
	Timeout   time.Duration      // how long this compile may take (0: no limit), beside ctx's deadline: past it the instance is refused, the others of a tree compiled all the same
	// AllowPlainSecrets: credentials written in clear accepted, a
	// provides secret without generate (a plain Secret of its stringData)
	// and a Secret of the objects with data or stringData; false, they
	// fail the compile: credentials are generated in the cluster
	// (generate, okdpGeneratedSecret) or read from the platform's secret
	// store, never written to the deployments repository.
	AllowPlainSecrets bool

	memo *instanceMemo // what the compiles of a set read once (Memo); nil: this compile reads its own
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
	// across namespaces, and the ExternalSecrets of the connections whose
	// credentials are in the platform's secret store; each that Helm keeps
	// (helm.sh/resource-policy: keep) with Argo CD's sync options alike,
	// Prune=false and Delete=false (keepSynced); the umbrella's objects
	// file, which it renders as written, in its release (helm.Write). nil
	// when there is none.
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
	// Cost: the CEL cost of the compile's expressions (Inputs.CostLimit
	// counts it); 0 without a limit, which counts none.
	Cost uint64
	// Warnings: what the compile warns of without failing, those of the
	// package (Definition.Warnings): a values.yaml that holds expressions;
	// with a chart cache, an instance list appended to that leaves out
	// items of the chart's default list.
	Warnings []string
	// SecretStore: the platform's secret store as the targets write it in
	// each project's namespace, global.okdp.secrets with a provider: a
	// SecretStore of that name, its provider verbatim, authenticating with
	// a ServiceAccount of the namespace; nil when the platform names a
	// ClusterSecretStore, or no store.
	SecretStore *SecretStore
	// Privileged: the namespace is one of the platform's own projects,
	// global.okdp.policy.privileged: its instances install cluster-scoped
	// objects, write other namespaces and set any upstream value, which the
	// compile refuses the other projects' (checkObjects); a target gives
	// its releases the engine's own identity, the other projects' one
	// confined to their namespace.
	Privileged bool
	// Access: the roles of the project's groups, its file's access (the
	// context at level 1, ParseAccess: okdp-project-<namespace> an
	// editor when it gives none), and the prefix the Kubernetes API server
	// gives the groups (global.okdp.access.groupPrefix): the targets write
	// them as Argo CD roles of the project's AppProject and RoleBindings of
	// its namespace.
	Access Access
	// ControlPlane: the control plane's identity,
	// global.okdp.controlPlane.serviceAccount, which the targets bind in
	// the project's namespace to what it needs there; nil when the
	// platform has no control plane.
	ControlPlane *ServiceAccount
	// Network: the network policy of the project's namespace,
	// global.okdp.network with policy true, its claimers the namespaces
	// of the claims bound to the instance (Inputs.Claims), which must
	// reach it; nil when the platform has none.
	Network *Network
}

// Compile runs the mapping of def on one instance, cancelled with ctx
// (between stages, and within the evaluation of an expression). It is safe
// for concurrent use: it keeps no state between calls.
func Compile(ctx context.Context, def *Definition, in Inputs) (result *Result, err error) {
	if in.Timeout > 0 {
		parent := ctx
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, in.Timeout)
		defer cancel()
		defer func() {
			if err != nil && parent.Err() == nil && errors.Is(err, context.DeadlineExceeded) {
				result, err = nil, fmt.Errorf("the compile takes longer than the instance timeout (%s)", in.Timeout)
			}
		}()
	}
	// ctx, def and the file systems are required: nil panics.
	svc, err := checkInputs(def, in)
	if err != nil {
		return nil, err
	}
	if in.memo == nil {
		in.memo = NewMemo().forInstance(0, def, in)
	}
	// 1-4. Parameters, platform context, connections, platform
	// requirements: the instance of the compile, which the stages below
	// read (context.go).
	inst, groups, err := platformInstance(def, in, svc)
	if err != nil {
		return nil, err
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
	outputs, credentials, pushes, err := providedOutputs(session, inst, in, res)
	if err != nil {
		return nil, err
	}
	// 8. Objects, and the platform's policy over them.
	out, err := instanceObjects(def, in, inst, svc, groups, res, outputs, credentials, pushes)
	if err != nil {
		return nil, err
	}
	// 9-10. Final values of the charts, checked against the upstream charts.
	if err := finalValues(ctx, def, in, inst, svc, out, res, written, clientCharts(clients, dcrValues, dcrSource)); err != nil {
		return nil, err
	}
	out.Cost = session.Used()
	return out, nil
}

// checkInputs: the inputs of a compile of def and its package's service
// (Definition.service), checked before anything is read: the release, the
// namespace and the name, the claims bound to the instance and the
// namespaces that may claim of it.
func checkInputs(def *Definition, in Inputs) (*Service, error) {
	switch {
	case !chart.IsRelease(in.Release):
		return nil, fmt.Errorf("Inputs.Release %q: a release name, lowercase letters, digits and \"-\", starting and ending with a letter or a digit, %d characters at most (Helm's limit)", in.Release, chart.ReleaseMax)
	case checkNamespace("Inputs.Namespace", in.Namespace) != nil:
		return nil, checkNamespace("Inputs.Namespace", in.Namespace)
	case in.Name != "" && !chart.IsDNSLabel(in.Name):
		return nil, fmt.Errorf("Inputs.Name %q: an instance name, lowercase letters, digits and \"-\", starting and ending with a letter or a digit, 63 characters at most", in.Name)
	}
	// Checked again: a Definition may be built in memory.
	svc, err := def.service()
	if err != nil {
		return nil, err
	}
	if err := checkClaims(svc, in.Claims); err != nil {
		return nil, err
	}
	if err := CheckClaimers("Inputs.Claimers", in.Claimers); err != nil {
		return nil, err
	}
	return svc, nil
}

// platformInstance: stages 1 to 4, the instance of a compile
// (newInstance: parameters, platform context, connections), then what the
// platform asks of it: its namespace and its release not reserved, the
// roles of the project's groups, its file's, which are refused for every
// instance of the project (projectAccess), and the global.okdp keys the
// service requires, in order, each set and not empty (not null, false, "",
// 0, [] or {}).
func platformInstance(def *Definition, in Inputs, svc *Service) (*instance, map[string]Role, error) {
	inst, err := newInstance(def.Files, in, svc)
	if err != nil {
		return nil, nil, err
	}
	// The platform's reserved namespaces, of its settings: a reservation
	// wins over a privilege.
	if err := checkPlatformNamespace("Inputs.Namespace", in.Namespace, inst.okdp); err != nil {
		return nil, nil, err
	}
	// A release named like an object the platform writes in the namespace.
	if err := checkRelease("Inputs.Release", in.Release, inst.okdp); err != nil {
		return nil, nil, err
	}
	groups, err := projectAccess(in.Context, in.Namespace)
	if err != nil {
		return nil, nil, err
	}
	// 4. Platform requirements.
	for _, k := range svc.Require {
		if !value.Truthy(value.Dig(inst.okdp, k)) {
			return nil, nil, fmt.Errorf("global.okdp.%s is required. %s", k, platformValue)
		}
	}
	return inst, groups, nil
}

// providedOutputs: stage 7 of the mapping's result res, the outputs the
// instance provides, checked against the contracts, with the credentials
// Secrets of their secret fields, an output for each claim bound to the
// instance (claimed), and the PushSecrets of the credentials of claims of
// another namespace (pushSecrets).
func providedOutputs(session *compile.Session, inst *instance, in Inputs, res M) (outputs []Output, credentials, pushes []M, err error) {
	outputs, credentials, err = provided(session, inst, in.memo.contracts, value.AsList(res["provides"]), in.AllowPlainSecrets)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := claimed(in.Claims, outputs); err != nil {
		return nil, nil, nil, err
	}
	pushes, err = pushSecrets(inst, in.memo.contracts, in.Claims, outputs)
	if err != nil {
		return nil, nil, nil, err
	}
	return outputs, credentials, pushes, nil
}

// instanceObjects: stage 8, the Result of the compile with its objects: the
// credentials Secrets, then the mapping's objects, then those of the
// credentials of claims across namespaces: pushed for the claims of another
// namespace, read for its own of another; and read for its connections
// whose credentials are in the platform's secret store. Then the platform's
// policy over them all: those of a project that is not privileged stay in
// its namespace, none cluster-scoped (checkObjects), serving no host the
// platform keeps from them (checkIngressHosts); no Secret of credentials in
// clear, unless allowed (checkPlainSecrets); no two of one kind, namespace
// and name (checkDuplicateObjects); then the sync options of Argo CD of
// those Helm keeps.
func instanceObjects(def *Definition, in Inputs, inst *instance, svc *Service, groups map[string]Role, res M, outputs []Output, credentials, pushes []M) (*Result, error) {
	out := &Result{Objects: credentials, Info: published(value.AsMap(res["instance"]), outputs), Dependencies: inst.deps, Release: in.Release, Namespace: in.Namespace, Service: svc.Name, Version: svc.Version, Chart: def.Chart, Warnings: slices.Clone(def.Warnings), Privileged: privileged(inst.okdp, in.Namespace), SecretStore: secretStore(inst.okdp), Access: Access{Groups: groups, GroupPrefix: groupPrefix(inst.okdp)}, ControlPlane: controlPlane(inst.okdp), Network: network(inst.okdp)}
	if out.Network != nil {
		out.Network.Claimers = claimers(in.Namespace, in.Claims)
	}
	for _, o := range value.AsList(res["objects"]) {
		out.Objects = append(out.Objects, value.AsMap(o))
	}
	out.Objects = append(out.Objects, pushes...)
	for _, x := range inst.transfers {
		out.Objects = append(out.Objects, pullSecret(inst, x))
	}
	if !out.Privileged {
		if err := checkObjects(svc.Name, in.Namespace, out.Objects); err != nil {
			return nil, err
		}
		if err := checkIngressHosts(svc.Name, in.Namespace, inst.okdp, out.Objects); err != nil {
			return nil, err
		}
	}
	if !in.AllowPlainSecrets {
		if err := checkPlainSecrets(svc.Name, out.Objects); err != nil {
			return nil, err
		}
	}
	if err := checkDuplicateObjects(svc.Name, in.Namespace, out.Objects); err != nil {
		return nil, err
	}
	// Whatever wrote it, an object Helm keeps is kept by Argo CD alike.
	keepSynced(out.Objects)
	return out, nil
}

// finalValues: stages 9 and 10 of the mapping's result res (written: the
// literal keys of its values file), set in out: the final values of each
// chart the umbrella renders, first the dependencies of Chart.yaml (rendered):
// all but those the values file leaves out. Then the compiler's own charts
// (own): the chart of each OAuth client, compiled in dcr mode; neither
// Chart.yaml nor the values file may list them. Added to the dependencies of
// Chart.yaml, without changing them. Then the final values of each chart
// rendered; with a chart cache, checked against its upstream chart.
func finalValues(ctx context.Context, def *Definition, in Inputs, inst *instance, svc *Service, out *Result, res M, written []string, own []ownChart) error {
	output := rendered(svc.Charts, written, value.AsMap(res["output"]))
	specs, err := addOwnCharts(def.Chart.Dependencies, svc.Charts, def.valuesName(), output, own)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	var warnings []string
	if out.Values, out.Charts, warnings, err = chart.FinalValues(in.memo.charts, def.Files, specs, def.valuesName(), output, inst.values, upstreamCheck(inst.okdp, in.Namespace, out.Privileged)); err != nil {
		return err
	}
	out.Warnings = append(out.Warnings, warnings...)
	// The hosts of the charts' ingresses, those of the objects checked above.
	if !out.Privileged {
		if err := checkIngressValues(in.Namespace, inst.okdp, out.Values); err != nil {
			return err
		}
	}
	// A literal of the instance's upstream values at a sensitive key, a
	// credential in clear most likely: a warning, unless allowed.
	if !in.AllowPlainSecrets {
		out.Warnings = append(out.Warnings, plainUpstream(value.AsMap(inst.values["upstream"]))...)
	}
	return nil
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
