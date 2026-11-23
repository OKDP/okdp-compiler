// Context: the instance of a compile (newInstance): its parameters, the
// platform context (global.okdp) and its connections, which together give
// what the mapping sees.

package okdp

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"sort"

	"github.com/okdp/okdp-compiler/chart"
	ruleset "github.com/okdp/okdp-compiler/internal/rules"
	"github.com/okdp/okdp-compiler/value"
)

// instance is what a compile knows of its instance. The first stages
// build it once (newInstance) and the others read it.
type instance struct {
	rules              *RuleSet // Inputs.Rules
	release, namespace string   // Inputs.Release, Inputs.Namespace
	name               string   // Inputs.Name, or the release when it is empty
	svc                *Service // the package: Chart.yaml's name and version
	// values are the merged parameters as the user wrote them, with global,
	// connections and the upstream values.
	values M
	// okdp holds the settings: global.okdp of the values layers with the
	// host's settings (Inputs.Settings), checked against
	// platform.schema.json, with its defaults applied.
	okdp  M
	deps  []Dependency // the instances the references point to, in parameter path order
	scope M            // what the mapping sees
	// transfers are the credentials read into Secrets of the instance's own
	// namespace (pullSecret): those of its claims of another namespace, and
	// those of its connections in the platform's secret store.
	transfers []transfer
	// store is the rule set's account of the platform's secret store
	// (storeOf), evaluated once by the first stage that asks (account).
	store struct {
		read bool
		v    M
		err  error
	}
}

// newInstance runs stages 1 to 3 and returns the instance of a compile of
// the package files.
//
//  1. Parameters. The values layers are merged in order, as Helm merges -f
//     files, over the defaults of the package's parameter schema, and
//     checked against it (parameters).
//  2. Settings. The settings are global.okdp of the values layers alone,
//     never of the parameters, which the defaults of a package schema
//     reach, with the host's settings (Inputs.Settings), which no value
//     holds.
//  3. Connections. The connection references of the parameters are
//     resolved in a copy; the merged values stay as the user wrote them.
//     The references that do not resolve are reported together.
//
// The mapping sees release (name, namespace), values (the resolved
// parameters), global as written, platform (what the compiler derives from
// global.okdp), service, labels, selectorLabels and claims.
func newInstance(ctx context.Context, files fs.FS, in Inputs, svc *Service) (*instance, error) {
	schema, values, settings, err := in.memo.paramsOf().get(ctx, files, in, svc)
	if err != nil {
		return nil, err
	}
	inst := &instance{
		rules:     in.rules(),
		release:   in.Release,
		namespace: in.Namespace,
		name:      cmp.Or(in.Name, in.Release),
		svc:       svc,
		values:    values,
		okdp:      settings,
	}
	named, err := namedClaims(values)
	if err != nil {
		return nil, err
	}
	params := value.AsMap(value.Copy(values))
	r := resolver{
		rules:     in.rules(),
		contracts: contractCacheOf(in),
		conns:     value.AsMap(values["connections"]),
		claims:    named,
		classes:   value.AsMap(inst.okdp["provisioning"]),
		published: in.Published,
		bindings:  in.Bindings,
		store:     storeName(inst.okdp),
		provider:  len(value.AsMap(value.Dig(inst.okdp, "secrets.provider"))) > 0,
		release:   in.Release,
		namespace: in.Namespace,
		draft:     schema.Doc["$schema"],
	}
	if refs(schema.Doc, params, "", "", r.reference); r.errs != nil {
		err := errors.Join(r.errs...)
		if r.missing != nil {
			err = &missingOutputs{outputs: r.missing, err: err}
		}
		return nil, err
	}
	// global, connections and claims are not parameters: global is exposed
	// apart, connections and claims are resolved.
	delete(params, "global")
	delete(params, "connections")
	delete(params, "claims")
	sort.Slice(r.deps, func(i, j int) bool { return r.deps[i].Field < r.deps[j].Field })
	inst.deps, inst.transfers = r.deps, r.transfers
	// platform, as the mapping sees it, derives from the platform's settings.
	release := M{"name": in.Release, "namespace": in.Namespace}
	platform, err := inst.rules.Scope(ctx, M{"settings": inst.okdp, "release": release})
	if err != nil {
		return nil, err
	}
	inst.scope = M{
		"service":        M{"name": svc.Name, "version": svc.Version},
		"labels":         inst.rules.Labels(svc.Name, in.Release, "", false),
		"selectorLabels": inst.rules.Labels(svc.Name, in.Release, "", true),
		"release":        release,
		"values":         params,
		"claims":         claimsScope(in.Claims),
		"global":         value.AsMap(value.Copy(values["global"])), // as written
		"platform":       platform,
	}
	return inst, nil
}

// parameters runs stage 1. It returns the parameter schema of the
// package, the parameters (the values layers merged over the schema's
// defaults, and checked) and the platform's settings (platformSettings).
//
// global.okdp of the parameters is the values layers', as written,
// whatever the package's schema gives: the mapping and the charts read the
// settings of the inputs, never a package's, and never the host's
// settings (Inputs.Settings), which the compile reads apart.
func parameters(ctx context.Context, files fs.FS, in Inputs, svc *Service) (*chart.Schema, M, M, error) {
	schema, err := chart.ReadParameterSchema(files)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := requirements(schema.Doc); err != nil {
		return nil, nil, nil, err
	}
	l, err := valuesLayers(in)
	if err != nil {
		return nil, nil, nil, err
	}
	values, err := chart.Parameters(schema, svc.Name, l.layers)
	if err != nil {
		return nil, nil, nil, err
	}
	g, _ := values["global"].(M)
	switch okdp, set := value.AsMap(l.written["global"])["okdp"]; {
	case set && g == nil:
		values["global"] = M{"okdp": value.Copy(okdp)}
	case set:
		g["okdp"] = value.Copy(okdp)
	case g != nil:
		delete(g, "okdp")
	}
	settings, err := platformSettings(in.rules(), l.platform)
	if err != nil {
		return nil, nil, nil, err
	}
	return schema, values, settings, nil
}

// platformSettings returns global.okdp of platform, the values layers'
// with the host's settings (valuesLayers), checked against the platform
// schema with its defaults applied. A value of another type is refused
// rather than read as not set: proxy: false would read as no proxy. A platform that requires a rule set the
// compile was not given is refused (requiredRuleSets).
func platformSettings(rules *RuleSet, platform M) (M, error) {
	settings, err := ruleset.SchemaValues(rules.PlatformSchema(), "the values (Inputs.Values) don't meet the specifications of the schema of global.okdp", value.AsMap(value.Copy(platform)))
	if err != nil {
		return nil, err
	}
	okdp := value.AsMap(value.Dig(settings, "global.okdp"))
	if err := requiredRuleSets(rules, okdp); err != nil {
		return nil, err
	}
	return okdp, nil
}

// requiredRuleSets checks that rules holds every set that
// global.okdp.ruleSets of okdp, the platform's settings, requires, of a
// version its constraint accepts. Without it, a tree whose settings a set
// reads would compile without that set's rules.
func requiredRuleSets(rules *RuleSet, okdp M) error {
	for _, v := range value.AsList(okdp["ruleSets"]) {
		m := value.AsMap(v)
		if err := rules.Require(value.Str(m["name"]), value.Str(m["version"])); err != nil {
			return fmt.Errorf("global.okdp.ruleSets: %w", err)
		}
	}
	return nil
}
