package compiler

import (
	"reflect"
	"strings"
	"testing"
)

// dcrCtx: a context whose platform registers OAuth clients anonymously.
func dcrCtx() M {
	return M{
		"release": M{"name": "demo-x", "namespace": "demo"},
		"platform": M{"oidc": M{
			"scope": "openid profile email",
			"dcr": M{
				"authMethod":      "anonymous",
				"registrationUrl": "https://idp.example.org/register",
				"image":           M{"repository": "example/dcr"},
			},
		}},
	}
}

// TestDcrDefaults: a confidential client, named after the release, with
// the instance label and 6 retries; no issuerUri, no wait for the provider.
func TestDcrDefaults(t *testing.T) {
	out, err := okdpDcr(dcrCtx(), M{"grantTypes": []any{"authorization_code"}, "redirectUris": []any{"https://x/cb"}})
	if err != nil {
		t.Fatal(err)
	}
	want := M{
		"ttlSeconds":      float64(30),
		"registrationUrl": "https://idp.example.org/register",
		"request": M{
			"application_type": "web",
			"client_name":      "demo-x-demo",
			"grant_types":      []any{"authorization_code"},
			"scope":            "profile email",
			"redirect_uris":    []any{"https://x/cb"},
		},
		"tls":              M{"insecure": false, "certificate": "certs-bundle"},
		"secret":           "demo-x-demo-dcr",
		"mapping":          M{"useDefault": false, "keyMapping": M{"client_id": ".client_id", "client_secret": ".client_secret"}},
		"security":         M{"serviceAccount": "demo-x-oidc-dcr", "role": "demo-x-oidc-dcr"},
		"image":            M{"repository": "example/dcr"},
		"fullnameOverride": "demo-x-oidc-dcr",
		"commonLabels":     M{"app.kubernetes.io/instance": "demo-x"},
		"backoffLimit":     float64(6),
	}
	if !reflect.DeepEqual(out, want) {
		t.Errorf("got  %#v\nwant %#v", out, want)
	}
}

// TestDcrNames: the objects are named after name, or after the release cut
// as okdp.fullname; the wait for the provider uses its discovery URL (one
// trailing slash of issuerUri dropped, as okdp.vendor.oidcDcr did).
func TestDcrNames(t *testing.T) {
	ctx := dcrCtx()
	asMap(asMap(ctx["platform"])["oidc"])["issuerUri"] = "https://idp.example.org/realms/data/"
	grants := []any{"client_credentials"}
	for name, tc := range map[string]struct {
		release, arg, want string
	}{
		"default":        {"demo-x", "", "demo-x-oidc-dcr"},
		"given":          {"demo-x", "demo-x-console-oidc-dcr", "demo-x-console-oidc-dcr"},
		"a long release": {"demo-" + strings.Repeat("x", 50), "", "demo-" + strings.Repeat("x", 50) + "-oidc-dc"},
	} {
		t.Run(name, func(t *testing.T) {
			asMap(ctx["release"])["name"] = tc.release
			out, err := okdpDcr(ctx, M{"grantTypes": grants, "name": tc.arg})
			if err != nil {
				t.Fatal(err)
			}
			if out["fullnameOverride"] != tc.want || asMap(out["security"])["role"] != tc.want || asMap(out["security"])["serviceAccount"] != tc.want {
				t.Errorf("names: %#v %#v", out["fullnameOverride"], out["security"])
			}
			if got := asMap(out["commonLabels"])["app.kubernetes.io/instance"]; got != tc.release {
				t.Errorf("commonLabels: %#v", out["commonLabels"])
			}
			if want := (M{"discoveryUrl": "https://idp.example.org/realms/data/.well-known/openid-configuration"}); !reflect.DeepEqual(out["waitForProvider"], want) {
				t.Errorf("waitForProvider: %#v", out["waitForProvider"])
			}
		})
	}
}

// TestDcrArgs: a public client, every name given, and a request override.
func TestDcrArgs(t *testing.T) {
	out, err := okdpDcr(dcrCtx(), M{
		"grantTypes": []any{"authorization_code"},
		"public":     true,
		"scope":      "openid groups",
		"clientName": "console",
		"secret":     "s",
		"name":       "n",
		"caSecret":   "ca",
		"request":    M{"application_type": "native"},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := asMap(out["request"])
	if req["token_endpoint_auth_method"] != "none" || req["scope"] != "groups" || req["client_name"] != "console" || req["application_type"] != "native" {
		t.Errorf("request: %#v", req)
	}
	if keys := asMap(asMap(out["mapping"])["keyMapping"]); !reflect.DeepEqual(keys, M{"client_id": ".client_id"}) {
		t.Errorf("a public client has no secret: %#v", keys)
	}
	if out["secret"] != "s" || asMap(out["security"])["role"] != "n" || asMap(out["tls"])["certificate"] != "ca" {
		t.Errorf("names: %#v", out)
	}
}

// TestDcrErrors: the platform must allow anonymous registration, and
// grantTypes is required.
func TestDcrErrors(t *testing.T) {
	grants := M{"grantTypes": []any{"client_credentials"}}
	for name, tc := range map[string]struct {
		dcr  M
		args M
		want string
	}{
		"auth method":      {M{"authMethod": "bearer", "registrationUrl": "u"}, grants, `authMethod "bearer" is not supported`},
		"registration url": {M{"authMethod": "anonymous"}, grants, "registrationUrl is required"},
		"grant types":      {M{"authMethod": "anonymous", "registrationUrl": "u"}, M{}, "grantTypes is required"},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := dcrCtx()
			asMap(asMap(ctx["platform"])["oidc"])["dcr"] = tc.dcr
			_, err := okdpDcr(ctx, tc.args)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

// TestDcrFromMapping: okdpDcr called from a mapping.
func TestDcrFromMapping(t *testing.T) {
	res, err := runCEL([]byte("output:\n  oidc-dcr: '${{ okdpDcr({\"grantTypes\": [\"client_credentials\"]}) }}'\n"), dcrCtx())
	if err != nil {
		t.Fatal(err)
	}
	if got := asMap(res.output["oidc-dcr"])["secret"]; got != "demo-x-demo-dcr" {
		t.Errorf("got %#v", got)
	}
	// $merge puts okdpDcr's values over the keys of the mapping: a mapping
	// that still sets them gets the same values.
	res, err = runCEL([]byte(`
output:
  oidc-dcr:
    $merge: '${{ okdpDcr({"grantTypes": ["client_credentials"]}) }}'
    fullnameOverride: ${{ release.name }}-oidc-dcr
    commonLabels: {app.kubernetes.io/instance: "${{ release.name }}"}
    backoffLimit: 6
`), dcrCtx())
	if err != nil {
		t.Fatal(err)
	}
	want, _ := okdpDcr(dcrCtx(), M{"grantTypes": []any{"client_credentials"}})
	if got := asMap(res.output["oidc-dcr"]); !reflect.DeepEqual(got, want) {
		t.Errorf("got  %#v\nwant %#v", got, want)
	}
}

// TestDcrImage: the platform's image of the Jobs, its snake_case keys of
// oidc-dcr < 0.5.0 renamed; a camelCase key wins over its former spelling.
func TestDcrImage(t *testing.T) {
	for name, tc := range map[string]struct{ image, want M }{
		"camelCase":      {M{"registry": "r", "pullPolicy": "Always", "pullSecrets": []any{"s"}}, M{"registry": "r", "pullPolicy": "Always", "pullSecrets": []any{"s"}}},
		"former keys":    {M{"tag": "1", "pull_policy": "Never", "pull_secrets": []any{"s"}}, M{"tag": "1", "pullPolicy": "Never", "pullSecrets": []any{"s"}}},
		"camelCase wins": {M{"pull_policy": "Never", "pullPolicy": "Always"}, M{"pullPolicy": "Always"}},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := dcrCtx()
			asMap(asMap(asMap(ctx["platform"])["oidc"])["dcr"])["image"] = tc.image
			out, err := okdpDcr(ctx, M{"grantTypes": []any{"client_credentials"}})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(out["image"], tc.want) {
				t.Errorf("got %#v, want %#v", out["image"], tc.want)
			}
		})
	}
}
