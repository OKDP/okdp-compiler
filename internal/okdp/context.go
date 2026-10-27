// Context: the instance of a compile, built by the first stages
// (newInstance): its parameters, its platform context (global.okdp checked
// against the embedded platform.schema.json, the OIDC settings derived from
// it) and its connections (the references resolved, connections.go), which
// give what the mapping sees.

package okdp

import (
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
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
	svc                *Service     // the package: Chart.yaml's name and version
	values             M            // the merged parameters, as the user wrote them: global, connections and upstream values included (chart.FinalValues)
	okdp               M            // global.okdp as written, as the mapping sees it, checked against platform.schema.json
	oidc               M            // platform.oidc: global.okdp.oidc, its defaults applied, its mode derived
	dcr                bool         // platform.oidc.dcr.enabled: OIDC enabled, the OAuth clients registered by DCR
	deps               []Dependency // the instances of the namespace the connection references point to, by parameter path
	scope              M            // what the mapping sees
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
// 2. Platform context: global.okdp checked against platformSchema, its
// defaults applied (schemaValues): a value of another type is refused
// rather than read as not set, oidc: false would leave OIDC enabled. The
// OIDC settings are derived from them: the client provisioning mode in
// effect (existing or dcr).
//
// 3. Connections: the connection references of the parameters
// (x-okdp-connection-ref) resolved in a copy against the contracts
// (Inputs.Contracts), the context's connections and what the other
// instances of the namespace publish (Inputs.Published), then checked
// against their requirements (require); the references
// that do not resolve are reported together, one error each, one per line,
// in the order of the walk (resolver.walk), as the violations of the
// parameter schema are; when some name an output no instance publishes,
// the error says which (missingOutputs). The references to an output are
// recorded (deps), by parameter path, in string order.
//
// The merged values stay as the user wrote them. The mapping sees release
// (name, namespace), values (the resolved parameters), global (global.okdp,
// as written), platform (what the compiler derives from it: oidc, the
// Secret of the main OAuth client, the ingress annotations, the proxy
// environment), and the package: service (name, version), labels and
// selectorLabels (without component).
func newInstance(files fs.FS, in Inputs, svc *Service) (*instance, error) {
	schema, values, err := parameters(files, in, svc)
	if err != nil {
		return nil, err
	}
	settings, err := schemaValues(platformSchema, "context values (Inputs.Context) don't meet the specifications of the schema of global.okdp", M{"global": value.Copy(values["global"])})
	if err != nil {
		return nil, err
	}
	inst := &instance{release: in.Release, namespace: in.Namespace, svc: svc, values: values}
	inst.okdp = value.AsMap(value.AsMap(values["global"])["okdp"])
	oidc := value.AsMap(value.Dig(settings, "global.okdp.oidc"))
	mode := value.Str(oidc["clientProvisioning"])
	inst.dcr = value.Truthy(oidc["enabled"]) && mode == "dcr"
	value.AsMap(oidc["dcr"])["enabled"] = inst.dcr
	oidc["existing"] = value.Truthy(oidc["enabled"]) && mode == "existing"
	inst.oidc = oidc
	params := value.AsMap(value.Copy(values))
	r := resolver{contracts: in.Contracts, conns: value.AsMap(values["connections"]), published: in.Published, release: in.Release, namespace: in.Namespace, draft: schema.Doc["$schema"]}
	if r.walk(schema.Doc, params, ""); r.errs != nil {
		err := errors.Join(r.errs...)
		if r.missing != nil {
			err = &missingOutputs{outputs: r.missing, err: err}
		}
		return nil, err
	}
	// Not parameters: global is exposed apart, connections are resolved.
	delete(params, "global")
	delete(params, "connections")
	sort.Slice(r.deps, func(i, j int) bool { return r.deps[i].Field < r.deps[j].Field })
	inst.deps = r.deps
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
		"global":         M{"okdp": value.Copy(inst.okdp)}, // the platform values, as written
		"platform": M{
			"oidc": oidc,
			// The Secret that holds the service's main OAuth client:
			// provisioned, or registered by DCR (oidc.go; oidcClientSecret(name)
			// for the others).
			"oidcClientSecret":   clientSecret(in.Release, in.Namespace, inst.dcr, ""),
			"ingressAnnotations": M{"cert-manager.io/cluster-issuer": value.Str(value.Dig(inst.okdp, "certificateIssuers.selfSigned.name"))},
			"proxyEnv":           proxyEnv,
		},
	}
	return inst, nil
}

// parameters: stage 1, the parameter schema of the package's files, the
// requirements of its connection references compiled (requirements), and
// the parameters: the values layers merged over its defaults, checked.
func parameters(files fs.FS, in Inputs, svc *Service) (*chart.Schema, M, error) {
	schema, err := chart.ReadParameterSchema(files)
	if err != nil {
		return nil, nil, err
	}
	if err := requirements(schema.Doc); err != nil {
		return nil, nil, err
	}
	layers, err := valuesLayers(schema.Doc, in)
	if err != nil {
		return nil, nil, err
	}
	values, err := chart.Parameters(schema, svc.Name, layers)
	if err != nil {
		return nil, nil, err
	}
	return schema, values, nil
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
