// Context: the instance of a compile, built by the first stages
// (newInstance): its parameters, its platform context (global.okdp checked
// against the embedded platform.schema.json, the OIDC settings derived from
// it) and its connections (the references resolved, connections.go), which
// give what the mapping sees.

package okdp

import (
	"cmp"
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"sort"
	"sync"

	"github.com/okdp/okdp-compiler/internal/chart"
	"github.com/okdp/okdp-compiler/internal/value"
)

// instance: what a compile knows of its instance, built once by the first
// stages (newInstance) and read by the others: the typed facts they need,
// and what the mapping sees (scope).
type instance struct {
	release, namespace string       // Inputs.Release, Inputs.Namespace
	name               string       // Inputs.Name, the release when it is empty
	svc                *Service     // the package: Chart.yaml's name and version
	values             M            // the merged parameters, as the user wrote them: global, connections and upstream values included (chart.FinalValues)
	okdp               M            // the platform's settings: the context's global.okdp (contextLayers), checked against platform.schema.json, its defaults applied
	oidc               M            // platform.oidc: global.okdp.oidc, its defaults applied, its mode derived
	dcr                bool         // platform.oidc.dcr.enabled: OIDC enabled, the OAuth clients registered by DCR
	deps               []Dependency // the instances the connection references point to, of the namespace or, for a claim, of its provider's, by parameter path
	scope              M            // what the mapping sees
	transfers          []transfer   // the credentials of its claims of another namespace, and of its connections in the platform's secret store, read into Secrets of its own (pullSecret)
}

// newInstance: stages 1 to 3, the instance of a compile of the package
// files.
//
// 1. Parameters: the package's parameter schema, read once for the stages
// that read it (chart.ReadParameterSchema): the keys of global an instance
// sets, the parameters, the connection references, their requirements
// compiled (requirements). Then the values layers, as Helm: the context's
// then the instance's (global and connections are the context's:
// valuesLayers), merged in order as -f files, coalesced over the schema's
// defaults, checked against it, connections apart (chart.Parameters).
//
// 2. Platform context: global.okdp of the context's layers alone
// (contextLayers: level 0's, the provisioning classes of the levels
// above), never of the parameters, which a package schema's defaults
// reach; checked against platformSchema, its defaults applied
// (platformSettings): a value of another type is refused rather than read
// as not set, oidc: false would leave OIDC enabled. The
// OIDC settings are derived from them: the client provisioning mode in
// effect (existing or dcr), and the CA bundle of the oidc-dcr Jobs,
// tls.caBundleSecret unless oidc.dcr.caSecret is set.
//
// 3. Connections: the connection references of the parameters
// (x-okdp-connection-ref) resolved in a copy against the contracts
// (Inputs.Contracts), the context's connections and what the other
// instances of the namespace publish (Inputs.Published), then checked
// against their requirements (require); the references
// that do not resolve are reported together, one error each, one per line,
// in the order of the walk (resolver.walk), as the violations of the
// parameter schema are; when some name an output no instance publishes,
// the error says which (missingOutputs). The references to an instance,
// an output or a claim, are recorded (deps), by parameter path, in string
// order.
//
// The merged values stay as the user wrote them. The mapping sees release
// (name, namespace), values (the resolved parameters), global (as written:
// global.okdp, the platform's settings, and the other keys of global, a
// project's own settings among them), platform (what the compiler derives
// from global.okdp: oidc, the Secret of the main OAuth client, the ingress
// annotations, the proxy environment), and the package: service (name,
// version), labels and selectorLabels (without component).
func newInstance(files fs.FS, in Inputs, svc *Service) (*instance, error) {
	schema, values, settings, err := parameters(files, in, svc)
	if err != nil {
		return nil, err
	}
	// The platform's settings come from the context's layers alone
	// (contextLayers), never from the parameters: a package schema's
	// defaults do not reach them.
	inst := &instance{release: in.Release, namespace: in.Namespace, name: cmp.Or(in.Name, in.Release), svc: svc, values: values, okdp: settings}
	oidc := value.AsMap(settings["oidc"])
	mode := value.Str(oidc["clientProvisioning"])
	inst.dcr = value.Truthy(oidc["enabled"]) && mode == "dcr"
	dcr := value.AsMap(oidc["dcr"])
	dcr["enabled"] = inst.dcr
	// The CA bundle the oidc-dcr Jobs trust: the platform's, tls.caBundleSecret,
	// unless dcr sets its own; "" for none.
	if _, set := dcr["caSecret"]; !set {
		dcr["caSecret"] = value.Str(value.Dig(settings, "tls.caBundleSecret"))
	}
	oidc["existing"] = value.Truthy(oidc["enabled"]) && mode == "existing"
	inst.oidc = oidc
	named, err := namedClaims(values)
	if err != nil {
		return nil, err
	}
	params := value.AsMap(value.Copy(values))
	r := resolver{contracts: in.Contracts, conns: value.AsMap(values["connections"]), levels: connectionLevels(in.Context), claims: named, classes: value.AsMap(inst.okdp["provisioning"]), published: in.Published, bindings: in.Bindings, store: storeName(inst.okdp), release: in.Release, namespace: in.Namespace, draft: schema.Doc["$schema"]}
	if r.walk(schema.Doc, params); r.errs != nil {
		err := errors.Join(r.errs...)
		if r.missing != nil {
			err = &missingOutputs{outputs: r.missing, err: err}
		}
		return nil, err
	}
	// Not parameters: global is exposed apart, connections and claims are
	// resolved.
	delete(params, "global")
	delete(params, "connections")
	delete(params, "claims")
	sort.Slice(r.deps, func(i, j int) bool { return r.deps[i].Field < r.deps[j].Field })
	inst.deps, inst.transfers = r.deps, r.transfers
	// Proxy settings, as environment variables.
	proxy := value.AsMap(inst.okdp["proxy"])
	proxyEnv := M{}
	for k, env := range map[string]string{"httpProxy": "HTTP_PROXY", "httpsProxy": "HTTPS_PROXY", "noProxy": "NO_PROXY"} {
		if value.Truthy(proxy[k]) {
			proxyEnv[env] = proxy[k]
		}
	}
	inst.scope = M{
		"service":        M{"name": svc.Name, "version": svc.Version},
		"labels":         labels(svc.Name, in.Release, "", false),
		"selectorLabels": labels(svc.Name, in.Release, "", true),
		"release":        M{"name": in.Release, "namespace": in.Namespace},
		"values":         params,
		"claims":         claimsScope(in.Claims),
		"global":         value.AsMap(value.Copy(values["global"])), // the platform's and the project's settings, as written
		"platform": M{
			"oidc": oidc,
			// The Secret that holds the service's main OAuth client:
			// provisioned, or registered by DCR (oidc.go; oidcClientSecret(name)
			// for the others).
			"oidcClientSecret":   clientSecret(in.Release, in.Namespace, inst.dcr, ""),
			"ingressAnnotations": ingressAnnotations(inst.okdp),
			"proxyEnv":           proxyEnv,
		},
	}
	return inst, nil
}

// ingressAnnotations: platform.ingressAnnotations, the annotations of
// every service's ingress: cert-manager.io/cluster-issuer, the platform's
// tls.clusterIssuer, then ingress.annotations of okdp (global.okdp, checked
// against platformSchema: strings), the ingress controller's, over it.
func ingressAnnotations(okdp M) M {
	out := M{"cert-manager.io/cluster-issuer": value.Str(value.Dig(okdp, "tls.clusterIssuer"))}
	maps.Copy(out, value.AsMap(value.Dig(okdp, "ingress.annotations")))
	return out
}

// parameters: stage 1, the parameter schema of the package's files, the
// requirements of its connection references compiled (requirements), and
// the parameters: the values layers merged over its defaults, checked;
// and the platform's settings, the context's global.okdp
// (contextLayers), {global: {okdp: ...}}, checked against platformSchema,
// its defaults applied (platformSettings). global.okdp of the parameters
// is the context's, as written, whatever the package's schema gives: the
// mapping and the charts read the platform's settings, never a package's.
func parameters(files fs.FS, in Inputs, svc *Service) (*chart.Schema, M, M, error) {
	schema, err := chart.ReadParameterSchema(files)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := requirements(schema.Doc); err != nil {
		return nil, nil, nil, err
	}
	layers, platform, err := valuesLayers(schema.Doc, in)
	if err != nil {
		return nil, nil, nil, err
	}
	values, err := chart.Parameters(schema, svc.Name, layers)
	if err != nil {
		return nil, nil, nil, err
	}
	g, _ := values["global"].(M)
	switch okdp, set := value.AsMap(platform["global"])["okdp"]; {
	case set && g == nil:
		values["global"] = M{"okdp": value.Copy(okdp)}
	case set:
		g["okdp"] = value.Copy(okdp)
	case g != nil:
		delete(g, "okdp")
	}
	settings, err := platformSettings(platform)
	if err != nil {
		return nil, nil, nil, err
	}
	return schema, values, settings, nil
}

// platformSettings: the platform's settings, global.okdp of platform, the
// context's (contextLayers), checked against platformSchema, its defaults
// applied: a value of another type is refused rather than read as not set
// (oidc: false would leave OIDC enabled).
func platformSettings(platform M) (M, error) {
	settings, err := schemaValues(platformSchema, "context values (Inputs.Context) don't meet the specifications of the schema of global.okdp", value.AsMap(value.Copy(platform)))
	if err != nil {
		return nil, err
	}
	return value.AsMap(value.Dig(settings, "global.okdp")), nil
}

// platformSchemaFile: the schema of the platform values the compiler reads
// (global.okdp): their types and defaults; the reference of platform
// authors, its descriptions their meaning.
//
//go:embed platform.schema.json
var platformSchemaFile []byte

// platformSchema: platformSchemaFile parsed once, for every compile
// (chart.ParseSchema).
var platformSchema = sync.OnceValues(func() (*chart.Schema, error) {
	return chart.ParseSchema("platform.schema.json", platformSchemaFile)
})

// schemaValues: user, values the schema of an embedded file describes
// (platformSchema, mappingSchema), coalesced over the schema's defaults and
// checked against it (chart.Schema.Values), as Helm reads a chart's: the
// values, or every violation at once, by path, under heading, as the
// parameters' are.
func schemaValues(schema func() (*chart.Schema, error), heading string, user M) (M, error) {
	s, err := schema()
	if err != nil {
		return nil, err
	}
	values, v, err := s.Values(user)
	if err != nil {
		return nil, err
	}
	if v != "" {
		return nil, fmt.Errorf("%s:\n%s", heading, v)
	}
	return values, nil
}
