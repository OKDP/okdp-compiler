package okdp

import (
	"maps"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/okdp/okdp-compiler/internal/chart"
	"github.com/okdp/okdp-compiler/internal/value"
)

// platformCtx: the platform context of the in-memory chart, with these
// global.okdp values.
func platformCtx(t *testing.T, okdp M) (M, error) {
	t.Helper()
	inst, err := newInstance(pkg, inputs(M{"global": M{"okdp": okdp}}), defService)
	if err != nil {
		return nil, err
	}
	return value.AsMap(inst.scope["platform"]), nil
}

// TestPlatformDefaults: OIDC defaults to an existing client, no proxy.
func TestPlatformDefaults(t *testing.T) {
	p, err := platformCtx(t, M{"certificateIssuers": M{"selfSigned": M{"name": "ca"}}})
	if err != nil {
		t.Fatal(err)
	}
	want := M{
		"oidc": M{
			"enabled":            true,
			"scope":              "openid profile email groups",
			"clientProvisioning": "existing",
			"existing":           true,
			"dcr":                M{"enabled": false, "caSecret": "certs-bundle", "ttlSeconds": float64(30), "backoffLimit": float64(6)},
		},
		"oidcClientSecret":   "creds-demo-app-oauth2",
		"ingressAnnotations": M{"cert-manager.io/cluster-issuer": "ca"},
		"proxyEnv":           M{},
	}
	if !reflect.DeepEqual(p, want) {
		t.Errorf("got  %#v\nwant %#v", p, want)
	}
}

// TestPlatformDcr: a DCR client has its own Secret; proxy settings become env.
func TestPlatformDcr(t *testing.T) {
	p, err := platformCtx(t, M{
		"oidc":  M{"clientProvisioning": "dcr", "scope": "openid"},
		"proxy": M{"httpProxy": "http://p:3128", "noProxy": ".svc"},
	})
	if err != nil {
		t.Fatal(err)
	}
	oidc := value.AsMap(p["oidc"])
	if value.AsMap(oidc["dcr"])["enabled"] != true || oidc["existing"] != false || oidc["scope"] != "openid" {
		t.Errorf("oidc: %#v", oidc)
	}
	if p["oidcClientSecret"] != "demo-app-demo-dcr" {
		t.Errorf("client secret: %#v", p["oidcClientSecret"])
	}
	if want := (M{"HTTP_PROXY": "http://p:3128", "NO_PROXY": ".svc"}); !reflect.DeepEqual(p["proxyEnv"], want) {
		t.Errorf("proxy env: %#v", p["proxyEnv"])
	}
}

// TestPlatformDisabled: OIDC off, neither client mode is active.
func TestPlatformDisabled(t *testing.T) {
	p, err := platformCtx(t, M{"oidc": M{"enabled": false, "clientProvisioning": "dcr"}})
	if err != nil {
		t.Fatal(err)
	}
	oidc := value.AsMap(p["oidc"])
	if value.AsMap(oidc["dcr"])["enabled"] != false || oidc["existing"] != false {
		t.Errorf("oidc: %#v", oidc)
	}
}

// TestPlatformDcrSettings: the settings of the oidc-dcr Jobs
// (global.okdp.oidc.dcr), defaults applied, the image passed as written.
func TestPlatformDcrSettings(t *testing.T) {
	defaults := M{"caSecret": "certs-bundle", "ttlSeconds": float64(30), "backoffLimit": float64(6)}
	for name, tc := range map[string]struct{ dcr, want M }{
		"defaults":  {M{"registrationUrl": "u"}, M{"registrationUrl": "u"}},
		"empty":     {M{"caSecret": ""}, M{"caSecret": ""}},
		"set":       {M{"caSecret": "ca", "ttlSeconds": float64(0), "backoffLimit": float64(1), "waitTtlSeconds": float64(60)}, M{"caSecret": "ca", "ttlSeconds": float64(0), "backoffLimit": float64(1), "waitTtlSeconds": float64(60)}},
		"camelCase": {M{"image": M{"registry": "r", "pullPolicy": "Always", "pullSecrets": []any{"s"}}}, M{"image": M{"registry": "r", "pullPolicy": "Always", "pullSecrets": []any{"s"}}}},
	} {
		t.Run(name, func(t *testing.T) {
			p, err := platformCtx(t, M{"oidc": M{"clientProvisioning": "dcr", "dcr": tc.dcr}})
			if err != nil {
				t.Fatal(err)
			}
			want := value.Merge(M{"enabled": true}, defaults)
			want = value.Merge(want, tc.want)
			if got := value.AsMap(value.Dig(p, "oidc.dcr")); !reflect.DeepEqual(got, want) {
				t.Errorf("got  %#v\nwant %#v", got, want)
			}
		})
	}
}

// TestPlatformSchema: global.okdp checked against platform.schema.json,
// every violation at once, by path, as Helm reads a chart's values: a null
// deletes a setting's default, and is refused where there is none; the
// other keys are not checked.
func TestPlatformSchema(t *testing.T) {
	for name, tc := range map[string]struct {
		global any
		want   string // the violations, "" for none
	}{
		"global":      {"x", "- at '/global': got string, want object"},
		"global.okdp": {M{"okdp": []any{}}, "- at '/global/okdp': got array, want object"},
		"each map and setting": {M{"okdp": M{"proxy": "p", "oidc": M{"enabled": "false", "clientProvisioning": "manual"}}},
			"- at '/global/okdp/oidc': validation failed\n  - at '/global/okdp/oidc/clientProvisioning': value must be one of 'existing', 'dcr'\n  - at '/global/okdp/oidc/enabled': got string, want boolean\n- at '/global/okdp/proxy': got string, want object"},
		"dcr": {M{"okdp": M{"oidc": M{"dcr": M{"image": "quay.io/x:1", "caSecret": true, "ttlSeconds": "30s", "backoffLimit": float64(-1), "waitTtlSeconds": 1.5}}}},
			"- at '/global/okdp/oidc/dcr/backoffLimit': minimum: got -1, want 0\n- at '/global/okdp/oidc/dcr/caSecret': got boolean, want string\n- at '/global/okdp/oidc/dcr/image': got string, want object\n- at '/global/okdp/oidc/dcr/ttlSeconds': got string, want integer\n- at '/global/okdp/oidc/dcr/waitTtlSeconds': got number, want integer"},
		"image": {M{"okdp": M{"oidc": M{"dcr": M{"image": M{"tag": "1", "pull_policy": "Never"}}}}},
			"- at '/global/okdp/oidc/dcr/image/pull_policy': false schema"},
		"charts": {M{"okdp": M{"charts": M{"objects": "okdp-objects", "oidcDcr": M{"version": float64(1)}, "oidcDCR": M{}, "dcr": nil}}},
			"- at '/global/okdp/charts': additional properties 'dcr', 'objects', 'oidcDCR' not allowed\n- at '/global/okdp/charts/oidcDcr/version': got number, want string"},
		"classes": {M{"okdp": M{"provisioning": M{
			"server":   M{"default": "standard", "classes": M{"standard": M{"provider": "data/pg", "request": M{"size": "1Gi"}}}},
			"database": M{"default": float64(1), "other": true, "classes": M{"Fast": M{"provider": "data/pg"}, "slow": M{"request": "1Gi", "tier": "b"}}},
		}}},
			"- at '/global/okdp/provisioning/database': additional properties 'other' not allowed\n- at '/global/okdp/provisioning/database/classes': validation failed\n  - at '/global/okdp/provisioning/database/classes': additional properties 'Fast' not allowed\n  - at '/global/okdp/provisioning/database/classes/slow': validation failed\n    - at '/global/okdp/provisioning/database/classes/slow': additional properties 'tier' not allowed\n    - at '/global/okdp/provisioning/database/classes/slow': missing property 'provider'\n    - at '/global/okdp/provisioning/database/classes/slow/request': got string, want object\n- at '/global/okdp/provisioning/database/default': got number, want string"},
		"null, a default":  {M{"okdp": M{"oidc": M{"scope": "", "clientProvisioning": nil, "dcr": M{"caSecret": "", "ttlSeconds": nil}}}}, ""},
		"null, no default": {M{"okdp": M{"proxy": M{"noProxy": nil}, "ingress": nil, "charts": M{"oidcDcr": nil}}}, "- at '/global/okdp/charts/oidcDcr': got null, want object\n- at '/global/okdp/ingress': got null, want object\n- at '/global/okdp/proxy/noProxy': got null, want string"},
		"no global":        {nil, ""},
		"unread is unread": {M{"okdp": M{"ingress": M{"className": 1}, "oidc": M{"dcr": M{"registrationUrl": 1}}}, "imageRegistry": 1}, ""},
	} {
		t.Run(name, func(t *testing.T) {
			heading := "context values (Inputs.Context) don't meet the specifications of the schema of global.okdp"
			_, err := schemaValues(platformSchema, heading, M{"global": value.Copy(tc.global)})
			want := heading + ":\n" + tc.want
			if (err == nil) != (tc.want == "") || (err != nil && err.Error() != want) {
				t.Errorf("got %v, want %q", err, tc.want)
			}
		})
	}
	// Checked before the platform context is derived: false no longer leaves
	// OIDC enabled; a null enabled deletes its default, and OIDC is off; an
	// empty scope is a value.
	if _, err := platformCtx(t, M{"oidc": false}); err == nil || !strings.HasSuffix(err.Error(), "- at '/global/okdp/oidc': got boolean, want object") {
		t.Errorf("compile: got %v", err)
	}
	p, err := platformCtx(t, M{"oidc": M{"enabled": nil, "clientProvisioning": "dcr", "scope": ""}})
	oidc := value.AsMap(p["oidc"])
	if _, set := oidc["enabled"]; err != nil || set || oidc["existing"] != false || value.Dig(oidc, "dcr.enabled") != false || oidc["scope"] != "" {
		t.Errorf("enabled: null: %v %#v", err, oidc)
	}
}

// TestContextErrors: unreadable package files.
func TestContextErrors(t *testing.T) {
	ch := maps.Clone(pkg)
	delete(ch, "config.schema.json")
	ch["config.schema.json/x"] = &fstest.MapFile{}
	if _, err := newInstance(ch, inputs(), defService); err == nil || !strings.Contains(err.Error(), "read config.schema.json") {
		t.Errorf("config.schema.json a directory: got %v", err)
	}
}

// TestContextValues: the parameters the mapping sees, without global and
// connections, and the merged values left as written.
func TestContextValues(t *testing.T) {
	inst, err := newInstance(pkg, inputs(M{"replicas": float64(2), "connections": M{}}), defService)
	if err != nil {
		t.Fatal(err)
	}
	if want := (M{"replicas": float64(2)}); !reflect.DeepEqual(inst.scope["values"], want) {
		t.Errorf("values in the context: %#v", inst.scope["values"])
	}
	for _, k := range []string{"global", "connections"} {
		if _, ok := inst.values[k]; !ok {
			t.Errorf("the merged values keep %s", k)
		}
	}
	if want := (M{"name": "demo-app", "namespace": "demo"}); !reflect.DeepEqual(inst.scope["release"], want) {
		t.Errorf("release: %#v", inst.scope["release"])
	}
}

// TestContextUpstream: values.upstream is the instance's upstream values
// as written, whether the chart accepts them or not.
func TestContextUpstream(t *testing.T) {
	svc := &Service{Charts: chart.Specs{
		"app":   {Upstream: true, Append: []string{"env"}},
		"other": {},
	}}
	up := M{"app": M{"env": []any{"C"}, "image": M{"pullPolicy": "Always"}}, "other": M{"y": float64(2)}}
	inst, err := newInstance(pkg, inputs(M{"upstream": up}), svc)
	if err != nil {
		t.Fatal(err)
	}
	if got := value.AsMap(inst.scope["values"])["upstream"]; !reflect.DeepEqual(got, up) {
		t.Errorf("values.upstream:\n got %#v\nwant %#v", got, up)
	}
}
