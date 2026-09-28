package compiler

import (
	"fmt"
	"strings"
)

// okdpDcr: the values of the vendored oidc-dcr chart for one OAuth client,
// as okdp.oidc.dcrValues + okdp.vendor.oidcDcr compute them. A host function:
// plain Go, unit-testable, shared by every service mapping (it replaces a
// library define).
//
// args: grantTypes (required), redirectUris, scope, clientName, secret,
// public, request, caSecret, name.
func okdpDcr(ctx M, args M) (M, error) {
	rel := asMap(ctx["release"])
	release, ns := str(rel["name"]), str(rel["namespace"])
	oidc := asMap(asMap(ctx["platform"])["oidc"])
	dcr := asMap(oidc["dcr"])
	// OKDP only registers anonymously.
	if str(dcr["authMethod"]) != "anonymous" {
		return nil, fmt.Errorf("global.okdp.oidc.dcr.authMethod %q is not supported: OKDP registers anonymously", str(dcr["authMethod"]))
	}
	if !truthy(dcr["registrationUrl"]) {
		return nil, fmt.Errorf("global.okdp.oidc.dcr.registrationUrl is required")
	}
	grants, ok := args["grantTypes"].([]any)
	if !ok || len(grants) == 0 {
		return nil, fmt.Errorf("okdpDcr: grantTypes is required")
	}
	scope := str(args["scope"])
	if scope == "" {
		scope = str(oidc["scope"])
	}
	// openid is left out of the requested scope.
	var scopes []string
	for s := range strings.SplitSeq(scope, " ") {
		if s != "openid" {
			scopes = append(scopes, s)
		}
	}
	clientName := str(args["clientName"])
	if clientName == "" {
		clientName = release + "-" + ns
	}
	request := M{
		"application_type": "web",
		"client_name":      clientName,
		"grant_types":      grants,
		"scope":            strings.Join(scopes, " "),
	}
	if ru, ok := args["redirectUris"].([]any); ok && len(ru) > 0 {
		request["redirect_uris"] = ru
	}
	// The Secret keys the chart writes; a public client has no secret.
	keys := M{"client_id": ".client_id", "client_secret": ".client_secret"}
	if truthy(args["public"]) {
		request["token_endpoint_auth_method"] = "none"
		keys = M{"client_id": ".client_id"}
	}
	// The mapping may override any registration field.
	request = mergeOverwrite(request, asMap(deepCopy(args["request"])))
	ca := str(args["caSecret"])
	if ca == "" {
		ca = "certs-bundle"
	}
	secret := str(args["secret"])
	if secret == "" {
		secret = release + "-" + ns + "-dcr"
	}
	name := str(args["name"])
	if name == "" {
		name = release + "-oidc-dcr"
	}
	out := M{
		"ttl_seconds":      float64(30),
		"registration_url": dcr["registrationUrl"],
		"request":          request,
		"tls":              M{"insecure": false, "certificate": ca},
		"secret":           secret,
		"mapping":          M{"use_default": false, "key_mapping": keys},
		"security":         M{"service_account": name, "role": name},
	}
	if img, ok := dcr["image"].(M); ok {
		out["image"] = deepCopy(img)
	}
	return out, nil
}
