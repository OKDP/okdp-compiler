// OIDC: the OAuth clients of a service, its mapping's oidcClients section.
// The platform provisions them (clientProvisioning: existing, the default:
// the Secrets creds-<release>-oauth2[-<name>]) or lets each service register
// its own by dynamic client registration (clientProvisioning: dcr): one
// oidc-dcr chart per client, whose Job registers it and writes its Secret.
//
// What is generic is Go: reading and checking the section, the modes, the
// names (charts, objects, Secrets), the chart entries and the Secret names
// the mapping sees (oidcClientSecret). The values of the oidc-dcr chart are
// a template in the mapping language, oidc-dcr.cel.yaml, evaluated per
// client.
package compiler

import (
	_ "embed"
	"fmt"
	"maps"
	"math"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
)

// DcrChart: the chart key of a service's main OAuth client (no name), and
// the suffix of the others: a client named n is compiled as the chart
// n-oidc-dcr. Both are rendered by the oidc-dcr chart (DcrSource).
const DcrChart = "oidc-dcr"

// DcrSource: the chart that registers an OAuth client by dynamic client
// registration, the version whose values the embedded template writes: the
// default of global.okdp.charts.oidcDcr.
var DcrSource = ChartRef{Repository: "oci://quay.io/adaltas", Chart: "oidc-dcr", Version: "0.5.0"}

// dcrTemplate: the oidc-dcr values of one client (the mapping language).
//
//go:embed oidc-dcr.cel.yaml
var dcrTemplate []byte

// clientKeys: the keys of a client of oidcClients.
var clientKeys = []string{"name", "grantTypes", "redirectUris", "scope", "clientName", "public", "request", "secret", "caSecret"}

// clientName: a client's name, part of its chart, object and Secret names.
var clientName = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// oidcClient: a client of the oidcClients section, checked.
type oidcClient struct {
	where string // oidcClients[<i>], and (<name>) when named, for the errors
	name  string // "" for the service's main client
	spec  M      // as written, null keys left out, name set
	chart string // oidc-dcr, or <name>-oidc-dcr
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
// platform's mode; a client's explicit secret is not known to it.
func oidcFunctions(ctx M) []cel.EnvOption {
	release, ns := str(dig(ctx, "release.name")), str(dig(ctx, "release.namespace"))
	dcr := dig(ctx, "platform.oidc.dcr.enabled") == true
	return []cel.EnvOption{
		cel.Function("oidcClientSecret", cel.Overload("oidcClientSecret_dyn", []*cel.Type{cel.DynType}, cel.StringType,
			cel.UnaryBinding(func(v ref.Val) ref.Val { return types.String(clientSecret(release, ns, dcr, text(v))) }))),
	}
}

// checkClient: the i-th item of oidcClients, checked: a map of the client
// keys, grantTypes required, each key of its type.
func checkClient(i int, v any) (oidcClient, error) {
	c := oidcClient{where: fmt.Sprintf("oidcClients[%d]", i)}
	m, ok := v.(M)
	if !ok {
		return c, fmt.Errorf("%s: must be a map (%s), got %s", c.where, strings.Join(clientKeys, ", "), short(v))
	}
	// A null key is not set ($if, or a null expression).
	c.spec = M{}
	var unknown []string
	for k, x := range m {
		if x == nil {
			continue
		}
		if !slices.Contains(clientKeys, k) {
			unknown = append(unknown, k)
		}
		c.spec[k] = x
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return c, fmt.Errorf("%s: unknown key(s) %s (allowed: %s)", c.where, strings.Join(unknown, ", "), strings.Join(clientKeys, ", "))
	}
	if n, set := c.spec["name"]; set {
		s, ok := n.(string)
		if !ok || (s != "" && !clientName.MatchString(s)) {
			return c, fmt.Errorf("%s: name %s must be lowercase letters, digits and \"-\" (it names the chart %s, its objects and its Secret)", c.where, short(n), clientChart("<name>"))
		}
		c.name = s
	}
	c.spec["name"] = c.name
	c.chart = clientChart(c.name)
	if c.name != "" {
		c.where += " (" + c.name + ")"
	}
	isStrings := func(x any) bool {
		l, ok := x.([]any)
		for _, s := range l {
			if t, ok := s.(string); !ok || t == "" {
				return false
			}
		}
		return ok
	}
	if g, ok := c.spec["grantTypes"].([]any); !ok || len(g) == 0 || !isStrings(g) {
		return c, fmt.Errorf("%s: grantTypes is required: a list of OAuth grant types (authorization_code, refresh_token, client_credentials...)", c.where)
	}
	if r, set := c.spec["redirectUris"]; set && !isStrings(r) {
		return c, fmt.Errorf("%s: redirectUris must be a list of URLs, got %s", c.where, short(r))
	}
	for _, k := range []string{"scope", "clientName", "secret", "caSecret"} {
		if x, set := c.spec[k]; set {
			if _, ok := x.(string); !ok {
				return c, fmt.Errorf("%s: %s must be a string, got %s", c.where, k, short(x))
			}
		}
	}
	if p, set := c.spec["public"]; set {
		if _, ok := p.(bool); !ok {
			return c, fmt.Errorf("%s: public must be true or false, got %s", c.where, short(p))
		}
	}
	if r, set := c.spec["request"]; set {
		if _, ok := r.(M); !ok {
			return c, fmt.Errorf("%s: request must be a map of registration fields, got %s", c.where, short(r))
		}
	}
	return c, nil
}

// oidcClients: the clients of the mapping's oidcClients section, checked in
// every mode (one main client at most, unique names), and, in dcr mode, the
// values of the oidc-dcr chart of each: chart -> values, from dcrTemplate.
// The platform must then register anonymously, at its registrationUrl.
func oidcClients(ctx M, section []any) ([]oidcClient, map[string]M, error) {
	var clients []oidcClient
	seen := map[string]string{}
	for i, v := range section {
		c, err := checkClient(i, v)
		if err != nil {
			return nil, nil, err
		}
		if other, dup := seen[c.name]; dup && c.name == "" {
			return nil, nil, fmt.Errorf("%s: %s has no name either: only the service's main client (chart %s) has none, name the others", c.where, other, DcrChart)
		} else if dup {
			return nil, nil, fmt.Errorf("%s: %s has the same name: client names must be unique", c.where, other)
		}
		seen[c.name] = c.where
		clients = append(clients, c)
	}
	oidc := asMap(dig(ctx, "platform.oidc"))
	dcr := asMap(oidc["dcr"])
	if len(clients) == 0 || dcr["enabled"] != true {
		return clients, nil, nil
	}
	// OKDP only registers anonymously.
	if str(dcr["authMethod"]) != "anonymous" {
		return nil, nil, fmt.Errorf("global.okdp.oidc.dcr.authMethod %q is not supported: OKDP registers its OAuth clients anonymously", str(dcr["authMethod"]))
	}
	if u, ok := dcr["registrationUrl"].(string); !ok || u == "" {
		return nil, nil, fmt.Errorf("global.okdp.oidc.dcr.registrationUrl is required (clientProvisioning: dcr): the endpoint the oidc-dcr Jobs register the OAuth clients at")
	}
	release, ns := str(dig(ctx, "release.name")), str(dig(ctx, "release.namespace"))
	values := map[string]M{}
	for _, c := range clients {
		names := M{"chart": c.chart, "fullname": fullname(release, c.chart), "secret": clientSecret(release, ns, true, c.name)}
		if s := str(c.spec["secret"]); s != "" {
			names["secret"] = s
		}
		in := maps.Clone(ctx)
		in["client"] = c.spec
		in["names"] = names
		v, err := evalTemplate(dcrTemplate, in)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %s values: %w", c.where, DcrChart, err)
		}
		values[c.chart] = v
	}
	return clients, values, nil
}

// dcrDefaults: the settings of global.okdp.oidc.dcr a platform may leave
// out, and their defaults. waitTtlSeconds has none: the chart's own
// waitForProvider.ttlSeconds (600 s) applies.
var dcrDefaults = []struct {
	key string
	def any
}{
	{"ttlSeconds", float64(30)},  // TTL of the finished Job (ttlSeconds), its logs kept that long
	{"backoffLimit", float64(6)}, // retries of the Job (backoffLimit)
	{"waitTtlSeconds", nil},      // longest wait for the provider (waitForProvider.ttlSeconds)
}

// dcrSettings: global.okdp.oidc.dcr, checked, with its defaults applied in
// place: caSecret (the Secret holding the CA bundle the Jobs trust,
// certs-bundle), ttlSeconds (30), backoffLimit (6), waitTtlSeconds (unset);
// image, the Jobs' image, a map whose snake_case keys of oidc-dcr < 0.5.0
// are renamed (dcrImage). registrationUrl and authMethod are left as
// written: they are checked when a client is registered.
func dcrSettings(dcr M) error {
	switch img := dcr["image"].(type) {
	case nil:
		delete(dcr, "image")
	case M:
		dcr["image"] = dcrImage(img)
	default:
		return fmt.Errorf("global.okdp.oidc.dcr.image must be a map (registry, repository, tag, pullPolicy, pullSecrets), got %s", short(img))
	}
	switch ca := dcr["caSecret"].(type) {
	case nil:
		dcr["caSecret"] = "certs-bundle"
	case string:
		if ca == "" {
			dcr["caSecret"] = "certs-bundle"
		}
	default:
		return fmt.Errorf("global.okdp.oidc.dcr.caSecret must be the name of a Secret, got %s", short(ca))
	}
	for _, d := range dcrDefaults {
		v := dcr[d.key]
		if v == nil {
			delete(dcr, d.key)
			if d.def != nil {
				dcr[d.key] = d.def
			}
			continue
		}
		if n, ok := v.(float64); !ok || n < 0 || n != math.Trunc(n) {
			return fmt.Errorf("global.okdp.oidc.dcr.%s must be a whole number >= 0, got %s", d.key, short(v))
		}
	}
	return nil
}

// dcrImage: the platform's image of the oidc-dcr Jobs, with the snake_case
// keys of oidc-dcr < 0.5.0 renamed; a camelCase key wins over its former
// spelling.
func dcrImage(img M) M {
	out := asMap(deepCopy(img))
	for old, key := range map[string]string{"pull_policy": "pullPolicy", "pull_secrets": "pullSecrets"} {
		if v, ok := out[old]; ok {
			if _, set := out[key]; !set {
				out[key] = v
			}
			delete(out, old)
		}
	}
	return out
}

// clientCharts: the charts of service.yaml, plus the chart of each client
// compiled in dcr mode (the chart key of the client, rendered by the
// platform's oidc-dcr chart src, no base values, no instance values), its
// values added to the mapping's output. The compiler owns these charts:
// neither service.yaml nor the mapping's output may list them, in any mode.
// svc is not changed.
func clientCharts(svc *Service, clients []oidcClient, values map[string]M, output M, src ChartRef) (*Service, error) {
	if len(clients) == 0 {
		return svc, nil
	}
	for _, c := range clients {
		if _, ok := svc.Charts[c.chart]; ok {
			return nil, fmt.Errorf("service.yaml: charts.%s: the chart of %s is added by the compiler: remove it from service.yaml", c.chart, c.where)
		}
		if _, ok := output[c.chart]; ok {
			return nil, fmt.Errorf("mapping output %q: the chart of %s is compiled from oidcClients: remove it from output", c.chart, c.where)
		}
	}
	out := *svc
	out.Charts = maps.Clone(svc.Charts)
	if out.Charts == nil {
		out.Charts = map[string]ChartSpec{}
	}
	for chart, v := range values {
		out.Charts[chart] = ChartSpec{Source: src}
		output[chart] = v
	}
	return &out, nil
}
