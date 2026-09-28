// Context: what a mapping sees. It loads the values layers, checks the
// platform requirements, derives the platform context from global.okdp, and
// resolves the connection references (connections.go).
package compiler

import (
	"fmt"
)

// buildContext: what a mapping sees, and the merged values.
func buildContext(in Inputs, svc *Service) (M, M, error) {
	// 1. values layers, as Helm: chart defaults, then each -f file.
	defaults, err := readYAML(in.Chart, "values.yaml")
	if err != nil {
		return nil, nil, err
	}
	values := asMap(deepCopy(defaults))
	for _, v := range in.Values {
		values = coalesce(values, asMap(normalize(v)))
	}
	global := asMap(values["global"])
	okdpG := asMap(global["okdp"])
	// 2. platform requirements.
	for _, k := range svc.Require {
		if !truthy(dig(okdpG, k)) {
			return nil, nil, fmt.Errorf("global.okdp.%s is required. It is a platform value: set it in platform/platform-values.yaml", k)
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
		return nil, nil, fmt.Errorf("global.okdp.oidc.clientProvisioning %q is not one of existing, dcr", mode)
	}
	dcr := asMap(deepCopy(oidc["dcr"]))
	dcr["enabled"] = truthy(oidc["enabled"]) && mode == "dcr"
	oidc["dcr"] = dcr
	oidc["existing"] = truthy(oidc["enabled"]) && mode == "existing"
	// The Secret that holds the OAuth client: provisioned, or registered by DCR.
	clientSecret := fmt.Sprintf("creds-%s-oauth2", in.Release)
	if dcr["enabled"] == true {
		clientSecret = fmt.Sprintf("%s-%s-dcr", in.Release, in.Namespace)
	}
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
		"oidcClientSecret":   clientSecret,
		"ingressAnnotations": M{"cert-manager.io/cluster-issuer": str(dig(okdpG, "certificateIssuers.selfSigned.name"))},
		"proxyEnv":           proxyEnv,
	}
	// 4. connection references, resolved in place (x-okdp-connection-ref).
	schema, err := readJSON(in.Chart, "values.schema.json")
	if err != nil {
		return nil, nil, err
	}
	// The mapping gets a copy: the merged values stay as the user wrote them.
	params := asMap(deepCopy(values))
	r := resolver{in: in, conns: asMap(values["connections"]), suffix: str(dig(okdpG, "ingress.suffix"))}
	if err := r.walk(schema, params, ""); err != nil {
		return nil, nil, err
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
	return ctx, values, nil
}
