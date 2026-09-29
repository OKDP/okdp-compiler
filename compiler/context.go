// Context: what a mapping sees. It loads the values layers, checks the
// platform requirements, derives the platform context from global.okdp, and
// resolves the connection references (connections.go).
package compiler

import (
	"fmt"
	"maps"
	"sort"
)

// Context: what a mapping sees, the merged values, and the instances the
// connection references point to.
func buildContext(in Inputs, svc *Service) (M, M, []Dependency, error) {
	// 1. values layers, as Helm: the -f files merged in order, coalesced
	// over the chart defaults.
	defaults, err := readValues(in.Chart, "values.yaml")
	if err != nil {
		return nil, nil, nil, err
	}
	user := M{}
	for _, v := range in.Values {
		user = mergeMaps(user, asMap(normalize(v)))
	}
	values := coalesceValues(defaults, user)
	// Checked against the parameter schema, as Helm checks values.schema.json.
	v, err := schemaViolations(in.Chart, "values.schema.json", values)
	if err != nil {
		return nil, nil, nil, err
	}
	chart, err := readChart(in.Chart)
	if err != nil {
		return nil, nil, nil, err
	}
	if v != "" {
		return nil, nil, nil, schemaError("values", map[string]string{chart.Name: v})
	}
	global := asMap(values["global"])
	okdpG := asMap(global["okdp"])
	// 2. platform requirements.
	for _, k := range svc.Require {
		if !truthy(dig(okdpG, k)) {
			return nil, nil, nil, fmt.Errorf("global.okdp.%s is required. It is a platform value: set it in platform/platform-values.yaml", k)
		}
	}
	// 3. platform context.
	// OIDC: defaults, then the client provisioning mode (existing or dcr).
	oidc := asMap(deepCopy(okdpG["oidc"]))
	if _, ok := oidc["enabled"]; !ok {
		oidc["enabled"] = true
	}
	if !truthy(oidc["scope"]) {
		oidc["scope"] = "openid profile email groups"
	}
	if !truthy(oidc["clientProvisioning"]) {
		oidc["clientProvisioning"] = "existing"
	}
	mode := str(oidc["clientProvisioning"])
	if mode != "existing" && mode != "dcr" {
		return nil, nil, nil, fmt.Errorf("global.okdp.oidc.clientProvisioning %q is not one of existing, dcr", mode)
	}
	// The settings of the oidc-dcr Jobs, defaults applied (oidc.go).
	dcr := asMap(deepCopy(oidc["dcr"]))
	if err := dcrSettings(dcr); err != nil {
		return nil, nil, nil, err
	}
	dcr["enabled"] = truthy(oidc["enabled"]) && mode == "dcr"
	oidc["dcr"] = dcr
	oidc["existing"] = truthy(oidc["enabled"]) && mode == "existing"
	// The Secret that holds the service's main OAuth client: provisioned, or
	// registered by DCR (oidc.go; oidcClientSecret(name) for the others).
	mainSecret := clientSecret(in.Release, in.Namespace, dcr["enabled"] == true, "")
	// Proxy settings, as environment variables.
	proxy := asMap(okdpG["proxy"])
	proxyEnv := M{}
	for k, env := range map[string]string{"httpProxy": "HTTP_PROXY", "httpsProxy": "HTTPS_PROXY", "noProxy": "NO_PROXY"} {
		if truthy(proxy[k]) {
			proxyEnv[env] = proxy[k]
		}
	}
	// What the compiler derives from global.okdp (the former okdp.oidc,
	// okdp.oidc.clientSecret, okdp.ingressAnnotations and okdp.proxy.env).
	platform := M{
		"oidc":               oidc,
		"oidcClientSecret":   mainSecret,
		"ingressAnnotations": M{"cert-manager.io/cluster-issuer": str(dig(okdpG, "certificateIssuers.selfSigned.name"))},
		"proxyEnv":           proxyEnv,
	}
	// 4. connection references, resolved in place (x-okdp-connection-ref).
	schema, err := readJSON(in.Chart, "values.schema.json")
	if err != nil {
		return nil, nil, nil, err
	}
	// The mapping gets a copy: the merged values stay as the user wrote them.
	params := asMap(deepCopy(values))
	var deps []Dependency
	r := resolver{in: in, conns: asMap(values["connections"]), suffix: str(dig(okdpG, "ingress.suffix")), deps: &deps}
	if err := r.walk(schema, params, ""); err != nil {
		return nil, nil, nil, err
	}
	// Not parameters: global is exposed apart, connections are resolved.
	delete(params, "global")
	delete(params, "connections")
	ctx := M{
		"release":  M{"name": in.Release, "namespace": in.Namespace},
		"values":   params,
		"global":   M{"okdp": deepCopy(okdpG)}, // platform-values.yaml, as written
		"platform": platform,                   // derived from it by the compiler
	}
	// The wrapper chart: chart (name, version, appVersion), labels and
	// selectorLabels (okdp.labels, okdp.selectorLabels).
	maps.Copy(ctx, chartContext(chart, in.Release))
	// The schema walk follows map order: sort for a stable result.
	sort.Slice(deps, func(i, j int) bool { return deps[i].Field < deps[j].Field })
	return ctx, values, deps, nil
}
