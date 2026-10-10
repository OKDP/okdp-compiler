// OIDC: the OAuth clients of a service, its mapping's oidcClients section.
// The platform provisions them (clientProvisioning: existing, the default:
// the Secrets creds-<release>-oauth2[-<name>]) or lets each service register
// its own by dynamic client registration (clientProvisioning: dcr): one
// oidc-dcr chart per client, whose Job registers it and writes its Secret.
//
// What is generic is Go: the checks the schema of the section
// (mapping.schema.json) cannot express, the modes, the names (charts,
// objects, Secrets), the chart entries and the Secret names the mapping
// sees (oidcClientSecret). The values of the oidc-dcr chart are a template
// in the mapping language, oidc-dcr.cel.yaml, evaluated per client.

package compiler

import (
	_ "embed"
	"fmt"
	"maps"

	"cel.dev/cel-go/cel"

	"github.com/okdp/okdp-compiler/internal/chart"
	"github.com/okdp/okdp-compiler/internal/compile"
	"github.com/okdp/okdp-compiler/internal/value"
)

// DcrChart: the chart key of a service's main OAuth client (no name), and
// the suffix of the others: a client named n is compiled as the chart
// n-oidc-dcr. Both are rendered by the oidc-dcr chart (DcrSource).
const DcrChart = "oidc-dcr"

// DcrSource: the chart that registers an OAuth client by dynamic client
// registration, the version whose values the embedded template writes: the
// default of global.okdp.charts.oidcDcr.
var DcrSource = chart.Ref{Repository: "oci://quay.io/adaltas", Chart: "oidc-dcr", Version: "0.5.0"}

// dcrTemplate: the oidc-dcr values of one client (the mapping language).
//
//go:embed oidc-dcr.cel.yaml
var dcrTemplate []byte

// oidcClient: a client of the oidcClients section, checked.
type oidcClient struct {
	where    string // oidcClients[<i>], and (<name>) when named, for the errors
	name     string // "" for the service's main client
	spec     M      // as written, null keys left out (runMapping), name set
	chart    string // oidc-dcr, or <name>-oidc-dcr
	fullname string // its objects' name, <release>-<chart> cut as okdpFullname
}

// clientChart: the chart of a client: oidc-dcr for the main client,
// <name>-oidc-dcr for a named one.
func clientChart(name string) string {
	if name == "" {
		return DcrChart
	}
	return name + "-" + DcrChart
}

// clientSecret: the Secret holding the OAuth client name ("" for the main
// client) of a release: written by its oidc-dcr Job in dcr mode,
// <release>-<namespace>[-<name>]-dcr; provisioned by the platform otherwise,
// creds-<release>-oauth2[-<name>].
func clientSecret(release, namespace string, dcr bool, name string) string {
	switch {
	case dcr && name == "":
		return release + "-" + namespace + "-dcr"
	case dcr:
		return release + "-" + namespace + "-" + name + "-dcr"
	case name == "":
		return "creds-" + release + "-oauth2"
	}
	return "creds-" + release + "-oauth2-" + name
}

// oidcFunctions: oidcClientSecret(name), the Secret of the OAuth client
// name ("" for the main client, as platform.oidcClientSecret) in the
// platform's mode, for the instance inst; a client's explicit secret is
// not known to it.
func oidcFunctions(inst *instance) []cel.EnvOption {
	release, ns, dcr := inst.release, inst.namespace, inst.dcr
	return []cel.EnvOption{
		cel.Function("oidcClientSecret", cel.Overload("oidcClientSecret_dyn", []*cel.Type{cel.DynType}, cel.StringType,
			compile.Unary(func(v any) (any, error) { return clientSecret(release, ns, dcr, value.Str(v)), nil }))),
	}
}

// oidcClients: the clients of the mapping's oidcClients section, checked
// against mappingSchema (runMapping), then in every mode (one main client
// at most, unique names), and, in dcr mode, the values of the oidc-dcr
// chart of each: chart -> values, from dcrTemplate. The platform must then
// register anonymously, at its registrationUrl, and no two clients write
// one Secret.
// The template is evaluated in the compile's session, on what the mapping
// of inst sees plus the client and its names.
func oidcClients(session *compile.Session, inst *instance, section []any) ([]oidcClient, map[string]M, error) {
	var clients []oidcClient
	seen := map[string]string{}
	for i, v := range section {
		spec := value.AsMap(v)
		c := oidcClient{where: fmt.Sprintf("oidcClients[%d]", i), name: value.Str(spec["name"]), spec: spec}
		c.chart = clientChart(c.name)
		if c.name != "" {
			c.where += " (" + c.name + ")"
		}
		spec["name"] = c.name
		if other, dup := seen[c.name]; dup && c.name == "" {
			return nil, nil, fmt.Errorf("%s: %s has no name either: only the service's main client (chart %s) has none, name the others", c.where, other, DcrChart)
		} else if dup {
			return nil, nil, fmt.Errorf("%s: %s has the same name: client names must be unique", c.where, other)
		}
		seen[c.name] = c.where
		c.fullname = fullname(inst.release, c.chart)
		clients = append(clients, c)
	}
	if len(clients) == 0 || !inst.dcr {
		return clients, nil, nil
	}
	dcr := value.AsMap(inst.oidc["dcr"])
	// OKDP only registers anonymously.
	if value.Str(dcr["authMethod"]) != "anonymous" {
		return nil, nil, fmt.Errorf("global.okdp.oidc.dcr.authMethod %q is not supported: OKDP registers its OAuth clients anonymously", value.Str(dcr["authMethod"]))
	}
	if u, ok := dcr["registrationUrl"].(string); !ok || u == "" {
		return nil, nil, fmt.Errorf("global.okdp.oidc.dcr.registrationUrl is required (clientProvisioning: dcr): the endpoint the oidc-dcr Jobs register the OAuth clients at")
	}
	values := map[string]M{}
	secrets := map[string]string{}
	for _, c := range clients {
		secret := clientSecret(inst.release, inst.namespace, true, c.name)
		if s := value.Str(c.spec["secret"]); s != "" {
			secret = s
		}
		if other, dup := secrets[secret]; dup {
			return nil, nil, fmt.Errorf("%s: %s writes the same Secret, %q: give each client its own secret", c.where, other, secret)
		}
		secrets[secret] = c.where
		names := M{"chart": c.chart, "fullname": c.fullname, "secret": secret}
		scope := maps.Clone(inst.scope)
		scope["client"] = c.spec
		scope["names"] = names
		v, err := session.EvalTemplate(dcrTemplate, scope)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %s values: %w", c.where, DcrChart, err)
		}
		values[c.chart] = v
	}
	return clients, values, nil
}

// clientCharts: the chart of each client, in order, the compiler's in any
// mode (ownChart): its key reserved, compiled from values, by chart (dcr
// mode: oidcClients), rendered by the platform's oidc-dcr chart src.
func clientCharts(clients []oidcClient, values map[string]M, src chart.Ref) []ownChart {
	own := make([]ownChart, 0, len(clients))
	for _, c := range clients {
		own = append(own, ownChart{key: c.chart, of: c.where, from: "oidcClients", source: src, values: values[c.chart]})
	}
	return own
}
