package compiler

import (
	"fmt"
	"strings"
)

// okdpDcr: the values of the vendored oidc-dcr chart (>= 0.5.0, camelCase
// values) for one OAuth client, as okdp.oidc.dcrValues + okdp.vendor.oidcDcr
// compute them. A host function: plain Go, unit-testable, shared by every
// service mapping (it replaces a library define). The registration request
// keeps the snake_case fields of the OIDC Dynamic Client Registration
// standard.
//
// What okdp.vendor.oidcDcr changed after rendering are values of the chart:
// the objects are named after `name` (fullnameOverride, default
// <release>-oidc-dcr cut as okdp.fullname), carry the instance label
// (commonLabels), the Job retries 6 times (backoffLimit), and, when the
// platform has an issuerUri, waits for the provider's discovery document
// (waitForProvider.discoveryUrl).
//
// The platform's global.okdp.oidc.dcr.image is passed as the chart's image;
// the snake_case keys of oidc-dcr < 0.5.0 (pull_policy, pull_secrets) are
// still accepted there and renamed.
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
	request = mergeMaps(request, asMap(deepCopy(args["request"])))
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
		name = fullname(release, "oidc-dcr")
	}
	out := M{
		"ttlSeconds":       float64(30),
		"registrationUrl":  dcr["registrationUrl"],
		"request":          request,
		"tls":              M{"insecure": false, "certificate": ca},
		"secret":           secret,
		"mapping":          M{"useDefault": false, "keyMapping": keys},
		"security":         M{"serviceAccount": name, "role": name},
		"fullnameOverride": name,
		"commonLabels":     M{"app.kubernetes.io/instance": release},
		"backoffLimit":     float64(6),
	}
	if issuer := str(oidc["issuerUri"]); issuer != "" {
		out["waitForProvider"] = M{"discoveryUrl": strings.TrimSuffix(issuer, "/") + "/.well-known/openid-configuration"}
	}
	if img, ok := dcr["image"].(M); ok {
		out["image"] = dcrImage(img)
	}
	return out, nil
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
