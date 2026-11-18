// Pipeline: what a compile reads besides the package (Inputs), what it
// produces (Result), and its stages in order (Compile).

package okdp

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"time"

	"github.com/okdp/cel2yaml"

	"github.com/okdp/okdp-compiler/chart"
	"github.com/okdp/okdp-compiler/internal/merge"
	ruleset "github.com/okdp/okdp-compiler/internal/rules"
	"github.com/okdp/okdp-compiler/value"
)

// M is a values map (value.M).
type M = value.M

// Inputs is what one compile reads besides the package. A values layer of
// Context or Values may hold any Go value a values file can hold
// (map[string]string, int32, a struct with json tags); another one (a
// func, a NaN) fails the compile.
type Inputs struct {
	Contracts fs.FS // the contract schemas, <contract>.schema.json
	// Context holds the context's values layers (global, global.okdp,
	// connections). They merge by level, lowest first, and in order within a
	// level, as helm -f does. A layer cannot change a value of a lower level.
	Context []ContextLayer
	// Values holds the instance's values layers, merged in order after
	// Context. They may set no connections, and of global only the keys the
	// parameter schema declares.
	Values  []map[string]any
	Release string // the Helm release, a DNS-1123 label of at most 53 characters
	// Name is the instance's name, a DNS-1123 label. It names the outputs the
	// mapping leaves unnamed and prefixes the IDs of the instance's claims.
	// CompileAll sets it. Empty means the release.
	Name      string
	Namespace string // the release's namespace, a DNS-1123 label
	// Published is what the other instances of the namespace publish, by
	// release: a reference may name one of their outputs. Nil means unknown:
	// only connections and claims resolve.
	Published map[string]Info
	// Claims are the claims bound to the instance. The mapping reads them and
	// must publish an output of each claim's contract named after its ID.
	// CompileAll sets them.
	Claims []Claim
	// Claimers lists the namespaces that may claim of this instance from
	// another namespace: namespace names, or "*" for any. Nil falls back to
	// the claimers of the package's provisions entry (Instance.Claimers).
	// A class of the platform (level 0) needs no such consent.
	// LoadRepository reads it from instance.yaml.
	Claimers []string
	// Bindings gives, by parameter path, the provider each claim of a
	// reference is bound to. CompileAll sets them. Nil means no claim resolves.
	Bindings map[string]Binding
	// CostLimit bounds the total CEL cost of the compile's expressions, the
	// mapping's and the compiler's own; 0 means no limit. The compile fails
	// past it. The limit is shared: an expression may spend what is left.
	CostLimit uint64
	Timeout   time.Duration // bounds this compile, on top of ctx's deadline; 0 means no limit
	// AllowPlainSecrets accepts credentials written in clear: a provides
	// secret without generate, and a Secret object with data or stringData.
	// By default they fail: no credential goes to the deployments repository.
	AllowPlainSecrets bool
	Rules             *RuleSet // nil applies the mandatory rules alone
	// Finalize makes the final values of the charts from what the compile
	// computed. Nil uses Helm's merge, with no policy over the upstream values.
	Finalize Finalize
	// CheckGlobals is the host's policy on the keys of global a project
	// sets. It runs before the mapping. Nil means none.
	CheckGlobals func(context.Context, GlobalsInput) error

	memo *instanceMemo // what the compiles of a set read once (Memo); nil for this compile's own
}

// FinalInput is what a Finalize makes the final values from. A host also
// checks each chart key against its chart, Specs[key].Source, which it
// fetches itself: the compiler reads no upstream chart.
type FinalInput struct {
	Files             fs.FS       // the package
	Specs             chart.Specs // the charts of the compile by key, the compiler's own among them
	ValuesFile        string      // the values file Output was read from, for errors
	Output            M           // what the mapping computed, by chart key
	Values            M           // the instance's merged values, with their global and upstream values
	Rules             *RuleSet    // the rule set of the compile
	Settings          M           // the platform's settings, global.okdp
	Release           string      // the Helm release name the charts render with
	Namespace         string
	Privileged        bool // the project is one of the platform's own
	AllowPlainSecrets bool // Inputs.AllowPlainSecrets
}

// GlobalLayer is the global one layer of a project sets. Name identifies
// the layer in errors.
type GlobalLayer struct {
	Name   string
	Global M
}

// GlobalsInput is what Inputs.CheckGlobals checks the keys of global a
// project sets against.
type GlobalsInput struct {
	Rules      *RuleSet
	Settings   M
	Namespace  string
	Privileged bool
	Layers     []GlobalLayer
}

// Finalize returns the final values of each key of in.Output, the chart
// that renders each, and warnings. It runs inside the compile of the
// instance, so its error refuses the instance like any other stage's.
type Finalize func(ctx context.Context, in FinalInput) (values map[string]map[string]any, charts map[string]chart.Ref, warnings []string, err error)

// ContextLayer is one values layer of the context (Inputs.Context), read
// as chart.ParseValues reads a file. Level 0 is the platform's; each higher
// level (a project's, an instance's own connections) adds to those below.
type ContextLayer struct {
	Level  uint
	Values map[string]any
}

// Result is what a compile produces.
type Result struct {
	// Values maps each chart key to the final values its upstream chart
	// receives (helm -f): OKDP's defaults, the computed values and the
	// instance's upstream values, without the chart's own defaults.
	Values map[string]map[string]any
	// Charts maps each chart key to the upstream chart that renders its
	// values: helm template <Charts[key]> -f <Values[key]>.
	Charts   map[string]chart.Ref
	Computed M // what the mapping computed for each chart key, before Finalize
	// Objects are OKDP's own objects, which the umbrella renders as written,
	// in this order: the credentials Secrets of the provided connections, the
	// objects section of the mapping, the PushSecrets and ExternalSecrets of
	// claims across namespaces, the ExternalSecrets of the connections whose
	// credentials are in the platform's secret store. Nil when there is none.
	Objects       []map[string]any
	KeepSync      ruleset.KeepSync // what the writer tells Argo CD about the objects Helm keeps
	ObjectsSource chart.Ref        // the chart that renders Objects (global.okdp.charts.objects)
	Info          Info             // what the instance publishes (okdp.info.yaml)
	// Dependencies are the instances the connection references point to,
	// through an output or a claim, in parameter path order. External
	// connections are not listed.
	Dependencies       []Dependency
	Release, Namespace string         // Inputs.Release and Inputs.Namespace
	Service, Version   string         // the name and version of Chart.yaml
	Chart              chart.Metadata // the package's Chart.yaml, with its dependencies in order
	Cost               uint64         // the CEL cost of the expressions; 0 without Inputs.CostLimit
	Warnings           []string       // what the compile reports without failing
	// SecretStore is the platform's secret store as the targets write it in
	// each project's namespace, when global.okdp.secrets gives a provider.
	// Nil when the platform names a ClusterSecretStore or no store.
	SecretStore *SecretStore
	// Privileged says the namespace is one of the platform's own projects
	// (global.okdp.policy.privileged). Its instances may install
	// cluster-scoped objects, write to other namespaces and set any upstream
	// value, all of which the compile refuses to other projects.
	Privileged bool
	Groups     map[string]Role // the project's groups and their roles (access of its project file)
	Settings   M               // global.okdp, validated and with defaults applied
	Claims     []Claim         // Inputs.Claims
}

// run is the state of one compile. Each stage is a method that reads what
// the earlier stages left and adds its own.
type run struct {
	ctx context.Context
	def *Definition
	in  Inputs

	svc          *Service        // the package's service (checkInputs)
	inst         *instance       // parameters, settings and connections (platformInstance)
	groups       map[string]Role // the project's groups (platformInstance)
	session      *cel2yaml.Session
	objectsChart chart.Ref // the chart that renders the instance's objects (mapping)
	res          M         // the mapping's result (mapping)
	written      []string  // the literal top-level keys of the values file (mapping)

	outputs     []Output // what the instance provides (provides)
	credentials []M      // the credentials Secrets of the outputs (provides)
	pushes      []M      // the PushSecrets of claims of another namespace (provides)

	out *Result // built by objects, completed by finalValues
}

// Compile runs the mapping of def on one instance. ctx cancels it between
// stages and inside the evaluation of an expression. It keeps no state
// between calls, so it is safe for concurrent use. ctx, def and the file
// systems are required: a nil one panics.
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
	c := &run{ctx: ctx, def: def, in: in}
	stages := []func() error{
		c.checkInputs,      // before anything is read
		c.platformInstance, // 1-4: parameters, platform context, connections, platform requirements
		c.mapping,          // 5-6: the mapping, and the objects the rule set adds
		c.provides,         // 7: the outputs and their credentials
		c.objects,          // 8: the objects and the platform's policy over them
		c.finalValues,      // 9-10: the final values, checked against the upstream charts
	}
	for _, stage := range stages {
		if err := stage(); err != nil {
			return nil, err
		}
	}
	c.out.Cost = c.session.Used()
	return c.out, nil
}

// checkInputs validates the inputs before anything is read, and reads the
// package's service.
func (c *run) checkInputs() error {
	in := c.in
	switch {
	case in.Rules != nil && !in.Rules.Loaded():
		return fmt.Errorf("Inputs.Rules: %w", ruleset.ErrNotLoaded)
	case !chart.IsRelease(in.Release):
		return fmt.Errorf("Inputs.Release %q: a release name, lowercase letters, digits and \"-\", starting and ending with a letter or a digit, %d characters at most (Helm's limit)", in.Release, chart.ReleaseMax)
	case checkNamespace(c.ctx, in.rules(), "Inputs.Namespace", in.Namespace) != nil:
		return checkNamespace(c.ctx, in.rules(), "Inputs.Namespace", in.Namespace)
	case in.Name != "" && !chart.IsDNSLabel(in.Name):
		return fmt.Errorf("Inputs.Name %q: an instance name, lowercase letters, digits and \"-\", starting and ending with a letter or a digit, 63 characters at most", in.Name)
	}
	// Checked again here because a Definition may be built in memory.
	svc, err := c.def.service()
	if err != nil {
		return err
	}
	if err := checkClaims(svc, in.Claims); err != nil {
		return err
	}
	if err := CheckClaimers("Inputs.Claimers", in.Claimers); err != nil {
		return err
	}
	c.svc = svc
	if c.in.memo == nil {
		c.in.memo = NewMemo().forInstance(0, c.def, c.in)
	}
	return nil
}

// platformInstance builds the instance (newInstance), then applies what
// the platform asks of it.
func (c *run) platformInstance() error {
	in, rules := c.in, c.in.rules()
	inst, err := newInstance(c.ctx, c.def.Files, in, c.svc)
	if err != nil {
		return err
	}
	// The platform's reserved namespaces come from its settings. A
	// reservation wins over a privilege.
	if err := checkPlatformNamespace(c.ctx, rules, "Inputs.Namespace", in.Namespace, inst.okdp); err != nil {
		return err
	}
	// A release must not be named like an object the platform writes in
	// the namespace.
	if err := checkRelease(c.ctx, rules, "Inputs.Release", in.Release, inst.okdp); err != nil {
		return err
	}
	// A project file with bad access refuses every instance of the project.
	groups, err := projectAccess(rules, in.Context, in.Namespace)
	if err != nil {
		return err
	}
	// 4. Platform requirements. Null, false, "", 0, [] and {} count as not
	// set.
	for _, k := range c.svc.Require {
		if !value.Truthy(value.Dig(inst.okdp, k)) {
			return fmt.Errorf("global.okdp.%s is required. %s", k, platformValue)
		}
	}
	c.inst, c.groups = inst, groups
	return nil
}

// mapping runs the package's mapping. The objects the rule set adds join
// the mapping's own, so that the policy over the objects reads them too.
func (c *run) mapping() error {
	// One CEL session serves the whole compile. The mapping, the compiler's
	// templates and okdpGeneratedSecret's template are all evaluated in it,
	// so ctx cancels them and their cost is counted together.
	c.session = cel2yaml.NewSession(c.in.CostLimit, functions(c.ctx, c.inst, &c.session)...)
	// The charts the platform renders the compiler's own charts with.
	sources, err := platformCharts(c.in.rules(), c.inst.okdp)
	if err != nil {
		return err
	}
	c.objectsChart = sources["objects"]
	c.res, c.written, err = runMapping(c.ctx, c.in.rules(), c.session, c.inst.scope, c.def.sources()...)
	if err != nil {
		return err
	}
	own, err := ownObjects(c.ctx, c.session, c.inst, c.res)
	if err != nil {
		return err
	}
	if len(own) > 0 {
		c.res["objects"] = append(value.AsList(c.res["objects"]), own...)
	}
	return nil
}

// provides checks the outputs and builds their credentials Secrets,
// requires an output for each claim bound to the instance, and builds the
// PushSecrets of the claims made from another namespace.
func (c *run) provides() error {
	if err := c.provided(); err != nil {
		return err
	}
	if err := claimed(c.in.Claims, c.outputs); err != nil {
		return err
	}
	pushes, err := pushSecrets(c.ctx, c.inst, c.in.memo.contracts, c.in.Claims, c.outputs)
	c.pushes = pushes
	return err
}

// objects starts the Result and collects the instance's objects, in the
// order Result.Objects documents. It then applies the platform's policy:
// a project that is not privileged keeps its objects in its namespace and
// serves only the hosts it may serve, no Secret holds credentials in
// clear, and no two objects share a kind, namespace and name.
func (c *run) objects() error {
	in, inst, rules := c.in, c.inst, c.in.rules()
	store, err := secretStore(c.ctx, rules, inst.okdp)
	if err != nil {
		return err
	}
	out := &Result{
		Objects:       c.credentials,
		ObjectsSource: c.objectsChart,
		Info:          published(value.AsMap(c.res["instance"]), c.outputs),
		Dependencies:  inst.deps,
		Release:       in.Release,
		Namespace:     in.Namespace,
		Service:       c.svc.Name,
		Version:       c.svc.Version,
		Chart:         c.def.Chart,
		Warnings:      slices.Clone(c.def.Warnings),
		Privileged:    privileged(inst.okdp, in.Namespace),
		SecretStore:   store,
		Groups:        c.groups,
		Settings:      inst.okdp,
		Claims:        in.Claims,
	}
	for _, o := range value.AsList(c.res["objects"]) {
		out.Objects = append(out.Objects, value.AsMap(o))
	}
	out.Objects = append(out.Objects, c.pushes...)
	for _, x := range inst.transfers {
		o, err := pullSecret(c.ctx, inst, x)
		if err != nil {
			return err
		}
		out.Objects = append(out.Objects, o)
	}
	if !out.Privileged {
		if err := CheckObjects(c.ctx, rules, c.svc.Name, in.Namespace, out.Objects); err != nil {
			return err
		}
		if err := CheckIngressHosts(c.ctx, rules, c.svc.Name, in.Namespace, inst.okdp, out.Objects); err != nil {
			return err
		}
	}
	if !in.AllowPlainSecrets {
		if err := checkPlainSecrets(c.ctx, rules, c.svc.Name, out.Objects); err != nil {
			return err
		}
	}
	if err := checkDuplicateObjects(c.svc.Name, in.Namespace, out.Objects); err != nil {
		return err
	}
	out.KeepSync = rules.Keep()
	c.out = out
	return nil
}

// finalValues selects the charts the umbrella renders (rendered) and
// makes their final values, with Inputs.Finalize or Helm's merge. Neither
// Chart.yaml nor the values file may list the chart of the instance's
// objects: the umbrella's writer adds it.
func (c *run) finalValues() error {
	in, def, out := c.in, c.def, c.out
	specs := c.svc.Charts
	output := rendered(specs, c.written, value.AsMap(c.res["output"]))
	if err := checkObjectsChart(def.Chart.Dependencies, def.valuesName(), output); err != nil {
		return err
	}
	if err := c.ctx.Err(); err != nil {
		return err
	}
	out.Computed = value.AsMap(value.Copy(output))
	var warnings []string
	var err error
	if in.Finalize != nil {
		out.Values, out.Charts, warnings, err = in.Finalize(c.ctx, FinalInput{
			Files:             def.Files,
			Specs:             specs,
			ValuesFile:        def.valuesName(),
			Output:            output,
			Values:            c.inst.values,
			Rules:             in.rules(),
			Settings:          c.inst.okdp,
			Release:           in.Release,
			Namespace:         in.Namespace,
			Privileged:        out.Privileged,
			AllowPlainSecrets: in.AllowPlainSecrets,
		})
	} else {
		var f merge.Final
		f, err = merge.FinalValues(def.Files, specs, def.valuesName(), output, c.inst.values, nil)
		out.Values, out.Charts = f.Values, f.Charts
	}
	if err != nil {
		return err
	}
	out.Warnings = append(out.Warnings, warnings...)
	return nil
}

// rendered returns the computed values of the dependencies the umbrella
// renders, by key. As in Helm, every dependency of Chart.yaml is rendered
// unless the values file switches it off: one it writes with an absent
// ($if) or null value is dropped, and one it does not write gets {}. A key
// of output that is no dependency is kept, so that later checks refuse it.
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
