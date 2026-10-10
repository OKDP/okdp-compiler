package compiler

import (
	"context"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/okdp/okdp-compiler/internal/chart"
	"github.com/okdp/okdp-compiler/internal/value"
)

// dcrCtx: a context whose platform registers OAuth clients anonymously (dcr
// mode), these DCR settings over the defaults (platformContext).
func dcrCtx(settings ...M) M {
	dcr := M{
		"authMethod":      "anonymous",
		"registrationUrl": "https://idp.example.org/register",
		"image":           M{"repository": "example/dcr"},
	}
	for _, s := range settings {
		dcr = value.Merge(dcr, s)
	}
	inst := &instance{values: M{"global": M{"okdp": M{"oidc": M{"clientProvisioning": "dcr", "scope": "openid profile email", "dcr": dcr}}}}}
	if err := platformContext(inst); err != nil {
		panic(err)
	}
	return M{
		"release":  M{"name": "demo-x", "namespace": "demo"},
		"platform": M{"oidc": inst.oidc},
	}
}

// dcrValues: the oidc-dcr values of these clients in ctx, by chart.
func dcrValues(ctx M, clients ...M) (map[string]M, error) {
	section := []any{}
	for _, c := range clients {
		section = append(section, value.Copy(c))
	}
	_, values, err := oidcClients(newSession(ctx), testInstance(ctx), section)
	return values, err
}

// setIssuer: the platform's issuerUri.
func setIssuer(ctx M, issuer string) {
	value.AsMap(value.AsMap(ctx["platform"])["oidc"])["issuerUri"] = issuer
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
// release, a named client's after the release and its name (cut as
// okdpFullname), and so are its Secret and client name; the wait for the
// provider uses its discovery URL (one trailing slash of issuerUri dropped).
func TestDcrNames(t *testing.T) {
	ctx := dcrCtx()
	setIssuer(ctx, "https://idp.example.org/realms/data/")
	long := "demo-" + strings.Repeat("x", 48) // the longest release, 53 characters
	for name, tc := range map[string]struct {
		release, client                    string
		chart, fullname, secret, clientNam string
	}{
		"main":           {"demo-x", "", "oidc-dcr", "demo-x-oidc-dcr", "demo-x-demo-dcr", "demo-x-demo"},
		"named":          {"demo-x", "console", "console-oidc-dcr", "demo-x-console-oidc-dcr", "demo-x-demo-console-dcr", "demo-x-demo-console"},
		"a long release": {long, "consoles", "consoles-oidc-dcr", long + "-consoles", long + "-demo-consoles-dcr", long + "-demo-consoles"},
	} {
		t.Run(name, func(t *testing.T) {
			value.AsMap(ctx["release"])["name"] = tc.release
			values, err := dcrValues(ctx, M{"grantTypes": []any{"client_credentials"}, "name": tc.client})
			if err != nil {
				t.Fatal(err)
			}
			out, ok := values[tc.chart]
			if !ok || len(values) != 1 {
				t.Fatalf("charts: %v", slices.Collect(maps.Keys(values)))
			}
			if out["fullnameOverride"] != tc.fullname || value.Dig(out, "security.role") != tc.fullname || value.Dig(out, "security.serviceAccount") != tc.fullname {
				t.Errorf("names: %#v %#v", out["fullnameOverride"], out["security"])
			}
			if out["secret"] != tc.secret || value.Dig(out, "request.client_name") != tc.clientNam {
				t.Errorf("secret %#v, client name %#v", out["secret"], value.Dig(out, "request.client_name"))
			}
			if got := value.AsMap(out["commonLabels"])["app.kubernetes.io/instance"]; got != tc.release {
				t.Errorf("commonLabels: %#v", out["commonLabels"])
			}
			if want := (M{"discoveryUrl": "https://idp.example.org/realms/data/.well-known/openid-configuration"}); !reflect.DeepEqual(out["waitForProvider"], want) {
				t.Errorf("waitForProvider: %#v", out["waitForProvider"])
			}
		})
	}
	// Two clients, one chart each.
	value.AsMap(ctx["release"])["name"] = "demo-x"
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
	req := value.AsMap(out["request"])
	if req["token_endpoint_auth_method"] != "none" || req["scope"] != "groups" || req["client_name"] != "console" || req["application_type"] != "native" {
		t.Errorf("request: %#v", req)
	}
	if _, ok := req["redirect_uris"]; ok {
		t.Errorf("no redirect URI: %#v", req)
	}
	if keys := value.Dig(out, "mapping.keyMapping"); !reflect.DeepEqual(keys, M{"client_id": ".client_id"}) {
		t.Errorf("a public client has no secret: %#v", keys)
	}
	if out["secret"] != "s" || value.Dig(out, "security.role") != "demo-x-oidc-dcr" || value.Dig(out, "tls.certificate") != "ca" {
		t.Errorf("names: %#v", out)
	}
	// The scope is the platform's when the client has none; public: false
	// is a confidential client.
	values, err = dcrValues(dcrCtx(), M{"grantTypes": []any{"client_credentials"}, "public": false, "redirectUris": []any{}})
	if err != nil {
		t.Fatal(err)
	}
	if out := values[DcrChart]; value.Dig(out, "request.scope") != "profile email" || value.Dig(out, "mapping.keyMapping.client_secret") != ".client_secret" {
		t.Errorf("defaults: %#v", out)
	}
	if _, ok := value.AsMap(values[DcrChart]["request"])["redirect_uris"]; ok {
		t.Errorf("an empty redirectUris is left out: %#v", values[DcrChart]["request"])
	}
}

// TestDcrPlatform: the platform's DCR settings (platform.oidc.dcr): the Job
// TTL and retries, the CA Secret (the client's caSecret wins), the wait for
// the provider and the image.
func TestDcrPlatform(t *testing.T) {
	ctx := dcrCtx(M{"ttlSeconds": float64(300), "backoffLimit": float64(2), "caSecret": "corp-ca", "waitTtlSeconds": float64(120),
		"image": M{"registry": "mirror.example.org", "pullPolicy": "Always"}})
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
	if values, err = dcrValues(ctx, M{"grantTypes": grants, "caSecret": "ca"}); err != nil || value.Dig(values[DcrChart], "tls.certificate") != "ca" {
		t.Errorf("caSecret of the client: %v %#v", err, values)
	}
	// No image in the platform values: the chart's.
	ctx = dcrCtx()
	delete(value.AsMap(value.Dig(ctx, "platform.oidc.dcr")), "image")
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
	value.AsMap(value.Dig(ctx, "platform.oidc.dcr"))["enabled"] = false
	clients, values, err := oidcClients(newSession(ctx), testInstance(ctx), []any{M{"grantTypes": []any{"client_credentials"}}, M{"name": "console", "grantTypes": []any{"authorization_code"}}})
	if err != nil || values != nil || len(clients) != 2 || clients[1].chart != "console-oidc-dcr" {
		t.Errorf("existing mode: %v %#v %#v", err, values, clients)
	}
	if _, _, err := oidcClients(newSession(ctx), testInstance(ctx), []any{M{}, M{}}); err == nil || !strings.Contains(err.Error(), "has no name either") {
		t.Errorf("existing mode, checked: %v", err)
	}
	// No client: nothing to check on the platform.
	value.AsMap(value.Dig(ctx, "platform.oidc.dcr"))["enabled"] = true
	value.AsMap(value.Dig(ctx, "platform.oidc.dcr"))["authMethod"] = "bearer"
	if _, values, err := oidcClients(newSession(ctx), testInstance(ctx), nil); err != nil || values != nil {
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
		value.AsMap(value.Dig(ctx, "platform.oidc.dcr"))["enabled"] = tc.dcr
		res, err := runMapping(newSession(ctx), []byte("output:\n  x: {s: '${{ oidcClientSecret(\""+tc.name+"\") }}'}\n"), ctx)
		if err != nil {
			t.Fatal(err)
		}
		if got := value.Dig(res.output, "x.s"); got != tc.want {
			t.Errorf("oidcClientSecret(%q), dcr %v: got %#v, want %q", tc.name, tc.dcr, got, tc.want)
		}
	}
}

// TestDcrErrors: the platform must register anonymously at a registration
// URL (dcr mode), and the clients must be told apart (every mode); their
// form is mapping.schema.json's (TestMappingErrors).
func TestDcrErrors(t *testing.T) {
	grants := []any{"client_credentials"}
	for name, tc := range map[string]struct {
		dcr     M // the platform's settings, over dcrCtx's
		clients []any
		want    string
	}{
		"auth method":      {M{"authMethod": "bearer"}, []any{M{"grantTypes": grants}}, `global.okdp.oidc.dcr.authMethod "bearer" is not supported`},
		"registration url": {M{"registrationUrl": ""}, []any{M{"grantTypes": grants}}, "global.okdp.oidc.dcr.registrationUrl is required"},
		"same name":        {nil, []any{M{"grantTypes": grants, "name": "a"}, M{"grantTypes": grants, "name": "a"}}, "oidcClients[1] (a): oidcClients[0] (a) has the same name: client names must be unique"},
		"same secret":      {nil, []any{M{"grantTypes": grants}, M{"grantTypes": grants, "name": "b", "secret": "demo-x-demo-dcr"}}, `oidcClients[1] (b): oidcClients[0] writes the same Secret, "demo-x-demo-dcr": give each client its own secret`},
		"two main clients": {nil, []any{M{"grantTypes": grants}, M{"grantTypes": grants, "name": ""}}, "oidcClients[1]: oidcClients[0] has no name either: only the service's main client (chart oidc-dcr) has none"},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := dcrCtx()
			maps.Copy(value.AsMap(value.Dig(ctx, "platform.oidc.dcr")), tc.dcr)
			_, _, err := oidcClients(newSession(ctx), testInstance(ctx), tc.clients)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

// TestDcrCompile: a mapping's oidcClients compiled with the service: in dcr
// mode, one oidc-dcr chart per client, which service.yaml does not list,
// rendered by DcrSource (its defaults merged, from the chart cache); the
// compiler owns these charts: service.yaml and the mapping's output may not
// list them.
func TestDcrCompile(t *testing.T) {
	ch := maps.Clone(charts)
	ch["quay.io/adaltas/oidc-dcr-0.5.0/values.yaml"] = &fstest.MapFile{Data: []byte("ttlSeconds: 0\nmapping: {useDefault: true, keyMapping: {registration_client_uri: .registration_client_uri}}\n")}
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
	compile := func(output string, existing bool, specs chart.Specs, cache fstest.MapFS, values M) (*Result, error) {
		d := *def
		d.Mapping = []byte(strings.Replace(mapping, "%s", output, 1))
		d.Service.Charts = specs
		p := value.AsMap(value.Copy(platform))
		if existing {
			value.Dig(p, "global.okdp.oidc").(M)["clientProvisioning"] = "existing"
		}
		in := inputs(p, values)
		in.Charts = cache
		return Compile(context.Background(), &d, in)
	}
	app := chart.Specs{"app": {Source: appSource}}
	for name, tc := range map[string]struct {
		values   M
		existing bool
		charts   map[string]chart.Ref // Result.Charts
	}{
		"dcr mode":          {M{"replicas": 2}, false, map[string]chart.Ref{"app": appSource, "okdp": objectsSource, "oidc-dcr": DcrSource, "console-oidc-dcr": DcrSource}},
		"a client left out": {M{"replicas": 1}, false, map[string]chart.Ref{"app": appSource, "okdp": objectsSource, "oidc-dcr": DcrSource}},
		"existing mode":     {M{"replicas": 2}, true, map[string]chart.Ref{"app": appSource, "okdp": objectsSource}},
	} {
		t.Run(name, func(t *testing.T) {
			res, err := compile("", tc.existing, app, ch, tc.values)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(res.Charts, tc.charts) || len(res.Values) != len(tc.charts) {
				t.Errorf("charts: got %v (values of %v), want %v", res.Charts, slices.Sorted(maps.Keys(res.Values)), tc.charts)
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
			if dcr["ttlSeconds"] != float64(30) || value.Dig(dcr, "request.redirect_uris") == nil || value.Dig(dcr, "mapping.keyMapping.registration_client_uri") != ".registration_client_uri" {
				t.Errorf("oidc-dcr values: %#v", dcr)
			}
			if c := res.Values["console-oidc-dcr"]; len(tc.charts) == 4 && (c["fullnameOverride"] != "demo-app-console-oidc-dcr" || value.Dig(c, "mapping.keyMapping.registration_client_uri") != ".registration_client_uri") {
				t.Errorf("console-oidc-dcr values, over oidc-dcr's defaults: %#v", c)
			}
		})
	}
	for name, tc := range map[string]struct {
		output   string
		existing bool
		charts   chart.Specs
		files    fstest.MapFS
		want     string
	}{
		"in output":        {"oidc-dcr: {}", true, app, ch, `mapping output "oidc-dcr": the chart of oidcClients[0] is compiled from oidcClients: remove it from output`},
		"in service.yaml":  {"", false, chart.Specs{"app": {Source: appSource}, "console-oidc-dcr": {Source: DcrSource}}, ch, "service.yaml: charts.console-oidc-dcr: the chart of oidcClients[1] (console) is added by the compiler: remove it from service.yaml"},
		"not in the cache": {"", false, app, charts, "oidc-dcr: chart oidc-dcr-0.5.0 is not in the chart cache: mkdir -p <chart cache>/quay.io/adaltas && helm pull oci://quay.io/adaltas/oidc-dcr --version 0.5.0 -d <chart cache>/quay.io/adaltas"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := compile(tc.output, tc.existing, tc.charts, tc.files, M{"replicas": 2})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
	// The platform may name another oidc-dcr chart, a mirror.
	mirror := chart.Ref{Repository: "oci://mirror.example.org/adaltas", Chart: "oidc-dcr", Version: "0.5.1"}
	mirrored := maps.Clone(charts)
	mirrored["mirror.example.org/adaltas/oidc-dcr-0.5.1/values.yaml"] = ch["quay.io/adaltas/oidc-dcr-0.5.0/values.yaml"]
	res, err := compile("", false, app, mirrored, M{"replicas": 1, "global": M{"okdp": M{"charts": M{"oidcDcr": M{"repository": mirror.Repository, "chart": mirror.Chart, "version": mirror.Version}}}}})
	if err != nil || res.Charts["oidc-dcr"] != mirror {
		t.Errorf("global.okdp.charts.oidcDcr: %v %#v", err, res)
	}
	// In existing mode, the chart cache needs no oidc-dcr chart.
	if _, err := compile("", true, app, charts, M{"replicas": 2}); err != nil {
		t.Errorf("existing mode without oidc-dcr: %v", err)
	}
	// def.Service is left as it was.
	if _, err := compile("", false, app, ch, M{"replicas": 2}); err != nil || len(app) != 1 {
		t.Errorf("service.yaml changed: %v %v", err, app)
	}
}
