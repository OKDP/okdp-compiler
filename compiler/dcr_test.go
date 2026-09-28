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

// TestDcrDefaults: a confidential client, named after the release.
func TestDcrDefaults(t *testing.T) {
	out, err := okdpDcr(dcrCtx(), M{"grantTypes": []any{"authorization_code"}, "redirectUris": []any{"https://x/cb"}})
	if err != nil {
		t.Fatal(err)
	}
	want := M{
		"ttl_seconds":      float64(30),
		"registration_url": "https://idp.example.org/register",
		"request": M{
			"application_type": "web",
			"client_name":      "demo-x-demo",
			"grant_types":      []any{"authorization_code"},
			"scope":            "profile email",
			"redirect_uris":    []any{"https://x/cb"},
		},
		"tls":      M{"insecure": false, "certificate": "certs-bundle"},
		"secret":   "demo-x-demo-dcr",
		"mapping":  M{"use_default": false, "key_mapping": M{"client_id": ".client_id", "client_secret": ".client_secret"}},
		"security": M{"service_account": "demo-x-oidc-dcr", "role": "demo-x-oidc-dcr"},
		"image":    M{"repository": "example/dcr"},
	}
	if !reflect.DeepEqual(out, want) {
		t.Errorf("got  %#v\nwant %#v", out, want)
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
	if keys := asMap(asMap(out["mapping"])["key_mapping"]); !reflect.DeepEqual(keys, M{"client_id": ".client_id"}) {
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
}
