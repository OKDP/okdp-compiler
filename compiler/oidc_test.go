package compiler

import (
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

// dcrCtx: a context whose platform registers OAuth clients anonymously (dcr
// mode), these DCR settings over the defaults (dcrSettings, as
// buildContext).
func dcrCtx(settings ...M) M {
	dcr := M{
		"authMethod":      "anonymous",
		"registrationUrl": "https://idp.example.org/register",
		"image":           M{"repository": "example/dcr"},
	}
	for _, s := range settings {
		dcr = mergeMaps(dcr, s)
	}
	if err := dcrSettings(dcr); err != nil {
		panic(err)
	}
	dcr["enabled"] = true
	return M{
		"release": M{"name": "demo-x", "namespace": "demo"},
		"platform": M{"oidc": M{
			"scope": "openid profile email",
			"dcr":   dcr,
		}},
	}
}

// dcrValues: the oidc-dcr values of these clients in ctx, by chart.
func dcrValues(ctx M, clients ...M) (map[string]M, error) {
	section := []any{}
	for _, c := range clients {
		section = append(section, deepCopy(c))
	}
	_, values, err := oidcClients(ctx, section)
	return values, err
}

// setIssuer: the platform's issuerUri.
func setIssuer(ctx M, issuer string) {
	asMap(asMap(ctx["platform"])["oidc"])["issuerUri"] = issuer
}

// TestDcrDefaults: the main client, confidential, named after the release,
// with the instance label and 6 retries; no issuerUri, no wait for the
// provider.
func TestDcrDefaults(t *testing.T) {
	values, err := dcrValues(dcrCtx(), M{"grantTypes": []any{"authorization_code"}, "redirectUris": []any{"https://x/cb"}})
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
	if len(values) != 1 || !reflect.DeepEqual(values[DcrChart], want) {
		t.Errorf("got  %#v\nwant %#v", values, want)
	}
}

// TestDcrNames: the main client's chart and objects are named after the
// release (cut as okdp.fullname), a named client's after the release and
// its name, and so are its Secret and client name; the wait for the
// provider uses its discovery URL (one trailing slash of issuerUri dropped,
// as okdp.vendor.oidcDcr did).
func TestDcrNames(t *testing.T) {
	ctx := dcrCtx()
	setIssuer(ctx, "https://idp.example.org/realms/data/")
	long := "demo-" + strings.Repeat("x", 50)
	for name, tc := range map[string]struct {
		release, client                    string
		chart, fullname, secret, clientNam string
	}{
		"main":           {"demo-x", "", "oidc-dcr", "demo-x-oidc-dcr", "demo-x-demo-dcr", "demo-x-demo"},
		"named":          {"demo-x", "console", "console-oidc-dcr", "demo-x-console-oidc-dcr", "demo-x-demo-console-dcr", "demo-x-demo-console"},
		"a long release": {long, "", "oidc-dcr", long + "-oidc-dc", long + "-demo-dcr", long + "-demo"},
	} {
		t.Run(name, func(t *testing.T) {
			asMap(ctx["release"])["name"] = tc.release
			values, err := dcrValues(ctx, M{"grantTypes": []any{"client_credentials"}, "name": tc.client})
			if err != nil {
				t.Fatal(err)
			}
			out, ok := values[tc.chart]
			if !ok || len(values) != 1 {
				t.Fatalf("charts: %v", slices.Collect(maps.Keys(values)))
			}
			if out["fullnameOverride"] != tc.fullname || dig(out, "security.role") != tc.fullname || dig(out, "security.serviceAccount") != tc.fullname {
				t.Errorf("names: %#v %#v", out["fullnameOverride"], out["security"])
			}
			if out["secret"] != tc.secret || dig(out, "request.client_name") != tc.clientNam {
				t.Errorf("secret %#v, client name %#v", out["secret"], dig(out, "request.client_name"))
			}
			if got := asMap(out["commonLabels"])["app.kubernetes.io/instance"]; got != tc.release {
				t.Errorf("commonLabels: %#v", out["commonLabels"])
			}
			if want := (M{"discoveryUrl": "https://idp.example.org/realms/data/.well-known/openid-configuration"}); !reflect.DeepEqual(out["waitForProvider"], want) {
				t.Errorf("waitForProvider: %#v", out["waitForProvider"])
			}
		})
	}
	// Two clients, one chart each.
	asMap(ctx["release"])["name"] = "demo-x"
	values, err := dcrValues(ctx, M{"grantTypes": []any{"client_credentials"}}, M{"name": "console", "grantTypes": []any{"authorization_code"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 2 || values["oidc-dcr"]["secret"] != "demo-x-demo-dcr" || values["console-oidc-dcr"]["secret"] != "demo-x-demo-console-dcr" {
		t.Errorf("two clients: %#v", values)
	}
}

// TestDcrArgs: a public client with every name given, a scope without
// openid, the CA Secret given and a request override.
func TestDcrArgs(t *testing.T) {
	values, err := dcrValues(dcrCtx(), M{
		"grantTypes": []any{"authorization_code"},
		"public":     true,
		"scope":      "openid groups",
		"clientName": "console",
		"secret":     "s",
		"caSecret":   "ca",
		"request":    M{"application_type": "native"},
	})
	if err != nil {
		t.Fatal(err)
	}
	out := values[DcrChart]
	req := asMap(out["request"])
	if req["token_endpoint_auth_method"] != "none" || req["scope"] != "groups" || req["client_name"] != "console" || req["application_type"] != "native" {
		t.Errorf("request: %#v", req)
	}
	if _, ok := req["redirect_uris"]; ok {
		t.Errorf("no redirect URI: %#v", req)
	}
	if keys := dig(out, "mapping.keyMapping"); !reflect.DeepEqual(keys, M{"client_id": ".client_id"}) {
		t.Errorf("a public client has no secret: %#v", keys)
	}
	if out["secret"] != "s" || dig(out, "security.role") != "demo-x-oidc-dcr" || dig(out, "tls.certificate") != "ca" {
		t.Errorf("names: %#v", out)
	}
	// The scope is the platform's when the client has none; public: false
	// is a confidential client.
	values, err = dcrValues(dcrCtx(), M{"grantTypes": []any{"client_credentials"}, "public": false, "redirectUris": []any{}})
	if err != nil {
		t.Fatal(err)
	}
	if out := values[DcrChart]; dig(out, "request.scope") != "profile email" || dig(out, "mapping.keyMapping.client_secret") != ".client_secret" {
		t.Errorf("defaults: %#v", out)
	}
	if _, ok := asMap(values[DcrChart]["request"])["redirect_uris"]; ok {
		t.Errorf("an empty redirectUris is left out: %#v", values[DcrChart]["request"])
	}
}

// TestDcrPlatform: the platform's DCR settings (platform.oidc.dcr): the Job
// TTL and retries, the CA Secret (the client's caSecret wins), the wait for
// the provider and the image, its snake_case keys renamed.
func TestDcrPlatform(t *testing.T) {
	ctx := dcrCtx(M{"ttlSeconds": float64(300), "backoffLimit": float64(2), "caSecret": "corp-ca", "waitTtlSeconds": float64(120),
		"image": M{"registry": "mirror.example.org", "pull_policy": "Always"}})
	setIssuer(ctx, "https://idp.example.org/realms/data")
	grants := []any{"client_credentials"}
	values, err := dcrValues(ctx, M{"grantTypes": grants})
	if err != nil {
		t.Fatal(err)
	}
	out := values[DcrChart]
	for k, v := range map[string]any{
		"ttlSeconds":      float64(300),
		"backoffLimit":    float64(2),
		"tls":             M{"insecure": false, "certificate": "corp-ca"},
		"waitForProvider": M{"discoveryUrl": "https://idp.example.org/realms/data/.well-known/openid-configuration", "ttlSeconds": float64(120)},
		"image":           M{"registry": "mirror.example.org", "repository": "example/dcr", "pullPolicy": "Always"},
	} {
		if !reflect.DeepEqual(out[k], v) {
			t.Errorf("%s: got %#v, want %#v", k, out[k], v)
		}
	}
	if values, err = dcrValues(ctx, M{"grantTypes": grants, "caSecret": "ca"}); err != nil || dig(values[DcrChart], "tls.certificate") != "ca" {
		t.Errorf("caSecret of the client: %v %#v", err, values)
	}
	// No image in the platform values: the chart's.
	ctx = dcrCtx()
	delete(asMap(dig(ctx, "platform.oidc.dcr")), "image")
	if values, err = dcrValues(ctx, M{"grantTypes": grants}); err != nil {
		t.Fatal(err)
	} else if _, ok := values[DcrChart]["image"]; ok {
		t.Errorf("image: %#v", values[DcrChart]["image"])
	}
}

// TestDcrModes: outside dcr mode no chart is compiled, but the clients are
// still checked; the Secret of each client in both modes.
func TestDcrModes(t *testing.T) {
	ctx := dcrCtx()
	asMap(dig(ctx, "platform.oidc.dcr"))["enabled"] = false
	clients, values, err := oidcClients(ctx, []any{M{"grantTypes": []any{"client_credentials"}}, M{"name": "console", "grantTypes": []any{"authorization_code"}}})
	if err != nil || values != nil || len(clients) != 2 || clients[1].chart != "console-oidc-dcr" {
		t.Errorf("existing mode: %v %#v %#v", err, values, clients)
	}
	if _, _, err := oidcClients(ctx, []any{M{"name": "console"}}); err == nil || !strings.Contains(err.Error(), "grantTypes is required") {
		t.Errorf("existing mode, checked: %v", err)
	}
	// No client: nothing to check on the platform.
	asMap(dig(ctx, "platform.oidc.dcr"))["enabled"] = true
	asMap(dig(ctx, "platform.oidc.dcr"))["authMethod"] = "bearer"
	if _, values, err := oidcClients(ctx, nil); err != nil || values != nil {
		t.Errorf("no client: %v %#v", err, values)
	}
	for _, tc := range []struct {
		dcr        bool
		name, want string
	}{
		{true, "", "demo-x-demo-dcr"},
		{true, "console", "demo-x-demo-console-dcr"},
		{false, "", "creds-demo-x-oauth2"},
		{false, "console", "creds-demo-x-oauth2-console"},
	} {
		if got := clientSecret("demo-x", "demo", tc.dcr, tc.name); got != tc.want {
			t.Errorf("clientSecret(dcr %v, %q): got %q, want %q", tc.dcr, tc.name, got, tc.want)
		}
		ctx := dcrCtx()
		asMap(dig(ctx, "platform.oidc.dcr"))["enabled"] = tc.dcr
		res, err := runCEL([]byte("output:\n  x: {s: '${{ oidcClientSecret(\""+tc.name+"\") }}'}\n"), ctx)
		if err != nil {
			t.Fatal(err)
		}
		if got := dig(res.output, "x.s"); got != tc.want {
			t.Errorf("oidcClientSecret(%q), dcr %v: got %#v, want %q", tc.name, tc.dcr, got, tc.want)
		}
	}
}

// TestDcrErrors: the platform must register anonymously at a registration
// URL (dcr mode), and the clients must be well formed (every mode).
func TestDcrErrors(t *testing.T) {
	grants := []any{"client_credentials"}
	for name, tc := range map[string]struct {
		dcr     M // the platform's settings, over dcrCtx's
		clients []any
		want    string
	}{
		"auth method":       {M{"authMethod": "bearer"}, []any{M{"grantTypes": grants}}, `global.okdp.oidc.dcr.authMethod "bearer" is not supported`},
		"registration url":  {M{"registrationUrl": ""}, []any{M{"grantTypes": grants}}, "global.okdp.oidc.dcr.registrationUrl is required"},
		"grant types":       {nil, []any{M{}}, "oidcClients[0]: grantTypes is required"},
		"empty grant types": {nil, []any{M{"grantTypes": []any{}}}, "oidcClients[0]: grantTypes is required"},
		"grant type":        {nil, []any{M{"grantTypes": []any{float64(1)}}}, "oidcClients[0]: grantTypes is required"},
		"not a map":         {nil, []any{"client_credentials"}, `oidcClients[0]: must be a map (name, grantTypes, redirectUris, scope, clientName, public, request, secret, caSecret), got "client_credentials"`},
		"unknown key":       {nil, []any{M{"grantTypes": grants, "redirectUri": "x", "ttl": 1}}, "oidcClients[0]: unknown key(s) redirectUri, ttl (allowed: name"},
		"name":              {nil, []any{M{"grantTypes": grants, "name": "Console"}}, `oidcClients[0]: name "Console" must be lowercase letters, digits and "-"`},
		"name type":         {nil, []any{M{"grantTypes": grants, "name": true}}, "oidcClients[0]: name true must be"},
		"same name":         {nil, []any{M{"grantTypes": grants, "name": "a"}, M{"grantTypes": grants, "name": "a"}}, "oidcClients[1] (a): oidcClients[0] (a) has the same name: client names must be unique"},
		"two main clients":  {nil, []any{M{"grantTypes": grants}, M{"grantTypes": grants, "name": ""}}, "oidcClients[1]: oidcClients[0] has no name either: only the service's main client (chart oidc-dcr) has none"},
		"redirect uris":     {nil, []any{M{"grantTypes": grants, "redirectUris": "https://x/cb"}}, `oidcClients[0]: redirectUris must be a list of URLs, got "https://x/cb"`},
		"scope":             {nil, []any{M{"grantTypes": grants, "scope": []any{"a"}}}, "oidcClients[0]: scope must be a string"},
		"public":            {nil, []any{M{"grantTypes": grants, "public": "yes", "name": "c"}}, `oidcClients[0] (c): public must be true or false, got "yes"`},
		"request":           {nil, []any{M{"grantTypes": grants, "request": "x"}}, "oidcClients[0]: request must be a map of registration fields"},
		"secret":            {nil, []any{M{"grantTypes": grants, "secret": float64(1)}}, "oidcClients[0]: secret must be a string"},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := dcrCtx()
			maps.Copy(asMap(dig(ctx, "platform.oidc.dcr")), tc.dcr)
			_, _, err := oidcClients(ctx, tc.clients)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
	// A null key is not set.
	if values, err := dcrValues(dcrCtx(), M{"grantTypes": grants, "scope": nil, "name": nil}); err != nil || dig(values[DcrChart], "request.scope") != "profile email" {
		t.Errorf("null keys: %v %#v", err, values)
	}
}

// TestDcrCompile: a mapping's oidcClients compiled with the service: in dcr
// mode, one oidc-dcr chart per client, merged over the vendored defaults;
// the mapping may not output these charts itself.
func TestDcrCompile(t *testing.T) {
	ch := maps.Clone(chart)
	ch["vendor/oidc-dcr/values.yaml"] = &fstest.MapFile{Data: []byte("ttlSeconds: 0\nmapping: {useDefault: true, keyMapping: {registration_client_uri: .registration_client_uri}}\n")}
	ch["vendor/console-oidc-dcr/values.yaml"] = ch["vendor/oidc-dcr/values.yaml"]
	platform := M{"global": M{"okdp": M{"oidc": M{"clientProvisioning": "dcr", "dcr": M{"authMethod": "anonymous", "registrationUrl": "https://idp/register"}}}}}
	mapping := `
let:
  host: app.${{ global.okdp.ingress.suffix }}
output:
  app:
    labels:
      main: ${{ oidcClientSecret('') }}
      console: ${{ oidcClientSecret('console') }}
  %s
oidcClients:
  - redirectUris: ['https://${{ host }}/callback']
    grantTypes: [authorization_code]
  - $if: ${{ values.replicas > 1 }}
    name: console
    public: true
    grantTypes: [authorization_code]
`
	for name, tc := range map[string]struct {
		output   string
		values   M
		existing bool
		charts   []string
		want     string
	}{
		"dcr mode":          {"", M{"replicas": 2}, false, []string{"app", "oidc-dcr", "console-oidc-dcr"}, ""},
		"a client left out": {"", M{"replicas": 1}, false, []string{"app", "oidc-dcr"}, ""},
		"existing mode":     {"", M{"replicas": 2}, true, []string{"app"}, ""},
		"in output":         {"oidc-dcr: {}", M{"replicas": 2}, true, nil, `mapping output "oidc-dcr": the chart of oidcClients[0], compiled from oidcClients: remove it from output`},
	} {
		t.Run(name, func(t *testing.T) {
			d := *def
			d.Mapping = []byte(strings.Replace(mapping, "%s", tc.output, 1))
			d.Service.Charts = map[string]ChartSpec{"app": {}, "oidc-dcr": {}, "console-oidc-dcr": {}}
			p := asMap(deepCopy(platform))
			if tc.existing {
				dig(p, "global.okdp.oidc").(M)["clientProvisioning"] = "existing"
			}
			in := inputs(p, tc.values)
			in.Chart = ch
			res, err := Compile(&d, in)
			if tc.want != "" {
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Errorf("got %v, want an error containing %q", err, tc.want)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := slices.Sorted(maps.Keys(res.Values)); !slices.Equal(got, slices.Sorted(slices.Values(tc.charts))) {
				t.Errorf("charts: got %v, want %v", got, tc.charts)
			}
			wantLabels := M{"main": "demo-app-demo-dcr", "console": "demo-app-demo-console-dcr"}
			if tc.existing {
				wantLabels = M{"main": "creds-demo-app-oauth2", "console": "creds-demo-app-oauth2-console"}
			}
			if got := res.Values["app"]["labels"]; !reflect.DeepEqual(got, wantLabels) {
				t.Errorf("Secrets: got %#v, want %#v", got, wantLabels)
			}
			if tc.existing {
				return
			}
			dcr := res.Values["oidc-dcr"]
			if dcr["ttlSeconds"] != float64(30) || dig(dcr, "request.redirect_uris") == nil || dig(dcr, "mapping.keyMapping.registration_client_uri") != ".registration_client_uri" {
				t.Errorf("oidc-dcr values: %#v", dcr)
			}
			// What the template computed, before the vendored defaults.
			if c := asMap(res.Computed["oidc-dcr"]); c["ttlSeconds"] != float64(30) || dig(c, "mapping.keyMapping.registration_client_uri") != nil {
				t.Errorf("computed: %#v", c)
			}
		})
	}
	// A client whose chart service.yaml does not declare.
	d := *def
	d.Mapping = []byte(strings.Replace(mapping, "%s", "", 1))
	d.Service.Charts = map[string]ChartSpec{"app": {}, "oidc-dcr": {}}
	in := inputs(platform, M{"replicas": 2})
	in.Chart = ch
	if _, err := Compile(&d, in); err == nil || !strings.Contains(err.Error(), `oidcClients[1] (console): chart "console-oidc-dcr" is not a chart of service.yaml`) {
		t.Errorf("got %v", err)
	}
}
