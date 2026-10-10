// Context: the instance of a compile, built by the first stages, in order:
// its parameters (the package's parameter schema read once, the values
// layers loaded: parameters), its platform context (global.okdp checked
// against the embedded platform.schema.json, the OIDC settings derived
// from it: platformContext) and its connections (the references resolved,
// connections.go), which give what the mapping sees (mappingScope). The
// platform requirements are checked on it (checkRequirements).

package compiler

import (
	_ "embed"
	"fmt"
	"io/fs"
	"maps"
	"sort"
	"strings"
	"sync"

	"github.com/okdp/okdp-compiler/internal/chart"
	"github.com/okdp/okdp-compiler/internal/compile"
	"github.com/okdp/okdp-compiler/internal/value"
)

// instance: what a compile knows of its instance, built once by the first
// stages (parameters, platformContext, connections) and read by the
// others: the typed facts they need, and what the mapping sees (scope).
type instance struct {
	release, namespace string       // Inputs.Release, Inputs.Namespace
	svc                serviceMeta  // service.yaml's name and version
	values             M            // the merged parameters, as the user wrote them: global, connections and upstream values included (chart.FinalValues)
	schema             M            // the parameter schema's map view (chart.Schema.Doc): the connection references
	okdp               M            // global.okdp as written, as the mapping sees it, checked against platform.schema.json (platformSettings)
	suffix             string       // global.okdp.ingress.suffix: the hosts of internal references
	oidc               M            // platform.oidc: global.okdp.oidc, its defaults applied, its mode derived
	dcr                bool         // platform.oidc.dcr.enabled: OIDC enabled, the OAuth clients registered by DCR
	deps               []Dependency // the instances the connection references point to, by parameter path
	scope              M            // what the mapping sees (mappingScope)
}

// parameters: stage 1, the instance of a compile and its parameters. The
// package's parameter schema is read first, once for the stages that read
// it (chart.ReadParameterSchema): the keys of global an instance sets, the
// parameters, the connection references. Then the values layers, as Helm:
// the platform's, the project's then the instance's (global is the
// platform's: valuesLayers), merged in order as -f files, coalesced over
// the schema's defaults, checked against it (chart.Parameters). files: the
// package.
func parameters(files fs.FS, in Inputs, svc *Service) (*instance, error) {
	schema, err := chart.ReadParameterSchema(files)
	if err != nil {
		return nil, err
	}
	layers, err := valuesLayers(schema.Doc, in)
	if err != nil {
		return nil, err
	}
	values, err := chart.Parameters(schema, svc.Name, layers)
	if err != nil {
		return nil, err
	}
	return &instance{release: in.Release, namespace: in.Namespace, svc: svc.meta(), values: values, schema: schema.Doc}, nil
}

// platformContext: stage 2, the platform settings of inst (global.okdp),
// checked against platform.schema.json, defaults applied
// (platformSettings), and the OIDC settings derived from them: the client
// provisioning mode in effect (existing or dcr).
func platformContext(inst *instance) error {
	settings, err := platformSettings(inst.values["global"])
	if err != nil {
		return err
	}
	inst.okdp = value.AsMap(value.AsMap(inst.values["global"])["okdp"])
	inst.suffix = value.Str(value.Dig(inst.okdp, "ingress.suffix"))
	oidc := value.AsMap(settings["oidc"])
	// A null enabled, unlike another setting's null, is kept: it reads as
	// false.
	if e, set := value.AsMap(inst.okdp["oidc"])["enabled"]; set && e == nil {
		oidc["enabled"] = nil
	}
	mode := value.Str(oidc["clientProvisioning"])
	dcr := value.AsMap(oidc["dcr"])
	inst.dcr = value.Truthy(oidc["enabled"]) && mode == "dcr"
	dcr["enabled"] = inst.dcr
	oidc["existing"] = value.Truthy(oidc["enabled"]) && mode == "existing"
	inst.oidc = oidc
	return nil
}

// connections: stage 3, the connection references of the parameters of
// inst (x-okdp-connection-ref) resolved in place against contracts, the
// compile's, shared with the descriptor; every reference that does not
// resolve is reported (resolver.resolveAll). The mapping gets a copy, the
// merged values stay as the user wrote them: the resolved parameters are
// its values (mappingScope), the internal references inst.deps, by
// parameter path, in string order, as the errors of the references.
func connections(inst *instance, contracts *contractCache) error {
	params := value.AsMap(value.Copy(inst.values))
	r := resolver{contracts: contracts, conns: value.AsMap(inst.values["connections"]), namespace: inst.namespace, suffix: inst.suffix}
	if err := r.resolveAll(inst.schema, params); err != nil {
		return err
	}
	// Not parameters: global is exposed apart, connections are resolved.
	delete(params, "global")
	delete(params, "connections")
	sort.Slice(r.deps, func(i, j int) bool { return r.deps[i].Field < r.deps[j].Field })
	inst.deps = r.deps
	inst.scope = mappingScope(inst, params)
	return nil
}

// mappingScope: what the mapping of inst sees, params its parameters:
// release (name, namespace), values, global (global.okdp, as written),
// platform (what the compiler derives from it: oidc, the Secret of the main
// OAuth client, the ingress annotations, the proxy environment), and the
// package: service (name, version), labels and selectorLabels
// (serviceContext).
func mappingScope(inst *instance, params M) M {
	// Proxy settings, as environment variables.
	proxy := value.AsMap(inst.okdp["proxy"])
	proxyEnv := M{}
	for k, env := range map[string]string{"httpProxy": "HTTP_PROXY", "httpsProxy": "HTTPS_PROXY", "noProxy": "NO_PROXY"} {
		if value.Truthy(proxy[k]) {
			proxyEnv[env] = proxy[k]
		}
	}
	scope := M{
		"release": M{"name": inst.release, "namespace": inst.namespace},
		"values":  params,
		"global":  M{"okdp": value.Copy(inst.okdp)}, // the platform values, as written
		"platform": M{
			"oidc": inst.oidc,
			// The Secret that holds the service's main OAuth client:
			// provisioned, or registered by DCR (oidc.go; oidcClientSecret(name)
			// for the others).
			"oidcClientSecret":   clientSecret(inst.release, inst.namespace, inst.dcr, ""),
			"ingressAnnotations": M{"cert-manager.io/cluster-issuer": value.Str(value.Dig(inst.okdp, "certificateIssuers.selfSigned.name"))},
			"proxyEnv":           proxyEnv,
		},
	}
	maps.Copy(scope, serviceContext(inst.svc, inst.release))
	return scope
}

// checkRequirements: stage 4, the requirements rs, in order, on the
// instance inst: an entry without a condition holds in every case, one with
// $if when its condition, evaluated on what the mapping sees (inst.scope)
// in the compile's session (the mapping's host functions, its budget), is
// true; the key of an entry that holds must be set in global.okdp
// (required). The first error is reported.
func checkRequirements(session *compile.Session, rs Requirements, inst *instance) error {
	for i, r := range rs {
		holds := true
		if r.If != "" {
			var err error
			if holds, err = session.Condition(r.If, inst.scope); err != nil {
				return fmt.Errorf("service.yaml: %s: $if: %w", entry(i, r.Key), err)
			}
		}
		if !holds {
			continue
		}
		if err := required(inst.okdp, r.Key); err != nil {
			return err
		}
	}
	return nil
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

// platformSettings: global.okdp of global, the platform values, checked
// against platformSchema, its defaults applied (schemaValues): a value of
// another type is refused rather than read as not set: oidc: false would
// leave OIDC enabled. global is not changed.
func platformSettings(global any) (M, error) {
	values, err := schemaValues(platformSchema, "platform values (--platform, Inputs.Platform) don't meet the specifications of the schema of global.okdp", M{"global": value.Copy(global)})
	if err != nil {
		return nil, err
	}
	return value.AsMap(value.Dig(values, "global.okdp")), nil
}

// schemaValues: user, values the schema of an embedded file describes
// (platformSchema, mappingSchema), what is not set dropped (unset),
// coalesced over the schema's defaults and checked against it
// (chart.Schema.Values): the values, or every violation at once, by path,
// under heading, as the parameters' are. user is changed.
func schemaValues(schema func() (*chart.Schema, error), heading string, user M) (M, error) {
	s, err := schema()
	if err != nil {
		return nil, err
	}
	unset(user, s.Doc, s.Doc)
	values, v, err := s.Values(user)
	if err != nil {
		return nil, err
	}
	if v != "" {
		return nil, fmt.Errorf("%s:\n%s", heading, v)
	}
	return values, nil
}

// unset: the values v without the settings the schema s describes that are
// not set, in place: a null, and an empty string where the default is a
// string (oidc.scope, oidc.clientProvisioning, oidc.dcr.caSecret). s
// describes a key by properties or additionalProperties, each item of a
// list by items, at any depth, through the $refs to its schema document
// root. Each then takes its default, where the coalesce of the parameters
// would delete it, or stays unset. A null under a key s does not describe
// is a value, kept as written.
func unset(v any, s, root M) {
	if ref, ok := s["$ref"].(string); ok {
		s = value.AsMap(value.Dig(root, strings.ReplaceAll(strings.TrimPrefix(ref, "#/"), "/", ".")))
	}
	switch t := v.(type) {
	case []any:
		for _, x := range t {
			unset(x, value.AsMap(s["items"]), root)
		}
	case M:
		props := value.AsMap(s["properties"])
		for k, x := range t {
			p, described := props[k].(M)
			if !described {
				p, described = s["additionalProperties"].(M)
			}
			if !described {
				continue
			}
			if _, str := p["default"].(string); x == nil || x == "" && str {
				delete(t, k)
				continue
			}
			unset(x, p, root)
		}
	}
}
