package compiler

import (
	"maps"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

// platformCtx: the platform context of the in-memory chart, with these
// global.okdp values.
func platformCtx(t *testing.T, okdp M) (M, error) {
	t.Helper()
	ctx, _, _, _, err := buildContext(inputs(M{"global": M{"okdp": okdp}}), &def.Service)
	if err != nil {
		return nil, err
	}
	return asMap(ctx["platform"]), nil
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
	oidc := asMap(p["oidc"])
	if asMap(oidc["dcr"])["enabled"] != true || oidc["existing"] != false || oidc["scope"] != "openid" {
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
	oidc := asMap(p["oidc"])
	if asMap(oidc["dcr"])["enabled"] != false || oidc["existing"] != false {
		t.Errorf("oidc: %#v", oidc)
	}
}

// TestPlatformDcrSettings: the settings of the oidc-dcr Jobs
// (global.okdp.oidc.dcr), defaults applied, the image's snake_case keys of
// oidc-dcr < 0.5.0 renamed (a camelCase key wins over its former spelling).
func TestPlatformDcrSettings(t *testing.T) {
	defaults := M{"caSecret": "certs-bundle", "ttlSeconds": float64(30), "backoffLimit": float64(6)}
	for name, tc := range map[string]struct{ dcr, want M }{
		"defaults":       {M{"registrationUrl": "u"}, M{"registrationUrl": "u"}},
		"empty":          {M{"caSecret": "", "ttlSeconds": nil, "image": nil}, M{}},
		"set":            {M{"caSecret": "ca", "ttlSeconds": float64(0), "backoffLimit": float64(1), "waitTtlSeconds": float64(60)}, M{"caSecret": "ca", "ttlSeconds": float64(0), "backoffLimit": float64(1), "waitTtlSeconds": float64(60)}},
		"camelCase":      {M{"image": M{"registry": "r", "pullPolicy": "Always", "pullSecrets": []any{"s"}}}, M{"image": M{"registry": "r", "pullPolicy": "Always", "pullSecrets": []any{"s"}}}},
		"former keys":    {M{"image": M{"tag": "1", "pull_policy": "Never", "pull_secrets": []any{"s"}}}, M{"image": M{"tag": "1", "pullPolicy": "Never", "pullSecrets": []any{"s"}}}},
		"camelCase wins": {M{"image": M{"pull_policy": "Never", "pullPolicy": "Always"}}, M{"image": M{"pullPolicy": "Always"}}},
	} {
		t.Run(name, func(t *testing.T) {
			p, err := platformCtx(t, M{"oidc": M{"clientProvisioning": "dcr", "dcr": tc.dcr}})
			if err != nil {
				t.Fatal(err)
			}
			want := mergeMaps(M{"enabled": true}, defaults)
			want = mergeMaps(want, tc.want)
			if got := asMap(dig(p, "oidc.dcr")); !reflect.DeepEqual(got, want) {
				t.Errorf("got  %#v\nwant %#v", got, want)
			}
		})
	}
	for name, tc := range map[string]struct {
		dcr  M
		want string
	}{
		"image":          {M{"image": "quay.io/x:1"}, `global.okdp.oidc.dcr.image must be a map (registry, repository, tag, pullPolicy, pullSecrets), got "quay.io/x:1"`},
		"caSecret":       {M{"caSecret": true}, "global.okdp.oidc.dcr.caSecret must be the name of a Secret, got true"},
		"ttlSeconds":     {M{"ttlSeconds": "30s"}, `global.okdp.oidc.dcr.ttlSeconds must be a whole number >= 0, got "30s"`},
		"backoffLimit":   {M{"backoffLimit": float64(-1)}, "global.okdp.oidc.dcr.backoffLimit must be a whole number >= 0, got -1"},
		"waitTtlSeconds": {M{"waitTtlSeconds": 1.5}, "global.okdp.oidc.dcr.waitTtlSeconds must be a whole number >= 0, got 1.5"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := platformCtx(t, M{"oidc": M{"dcr": tc.dcr}}); err == nil || err.Error() != tc.want {
				t.Errorf("got %v, want %q", err, tc.want)
			}
		})
	}
}

// TestContextErrors: an unknown provisioning mode, unreadable chart files.
func TestContextErrors(t *testing.T) {
	if _, err := platformCtx(t, M{"oidc": M{"clientProvisioning": "manual"}}); err == nil ||
		!strings.Contains(err.Error(), `clientProvisioning "manual" is not one of existing, dcr`) {
		t.Errorf("got %v", err)
	}
	for _, missing := range []string{"values.yaml", "values.schema.json"} {
		ch := maps.Clone(chart)
		delete(ch, missing)
		in := inputs()
		in.Chart = ch
		if _, _, _, _, err := buildContext(in, &def.Service); err == nil {
			t.Errorf("without %s: must fail", missing)
		}
	}
}

// TestContextValues: the parameters the mapping sees, without global and
// connections, and the merged values left as written.
func TestContextValues(t *testing.T) {
	ctx, values, _, _, err := buildContext(inputs(M{"replicas": float64(2), "connections": M{}}), &def.Service)
	if err != nil {
		t.Fatal(err)
	}
	if want := (M{"replicas": float64(2)}); !reflect.DeepEqual(ctx["values"], want) {
		t.Errorf("values in the context: %#v", ctx["values"])
	}
	if _, ok := values["global"]; !ok {
		t.Error("the merged values keep global")
	}
	if want := (M{"name": "demo-app", "namespace": "demo"}); !reflect.DeepEqual(ctx["release"], want) {
		t.Errorf("release: %#v", ctx["release"])
	}
}

// TestContextUpstream: values.upstream.<chart> of a chart that accepts
// upstream values is its files then its inline values, merged as they will
// be applied (append paths appended); the other keys stay as written, and
// the merged values keep the inline values only.
func TestContextUpstream(t *testing.T) {
	svc := &Service{Charts: map[string]ChartSpec{
		"app":   {Upstream: Upstream{Enabled: true}, Append: []string{"env"}},
		"files": {Upstream: Upstream{Enabled: true, Files: []string{"a.yaml", "b.yaml"}}},
		"other": {},
	}}
	in := inputs(M{"upstream": M{
		"app":   M{"env": []any{"C"}, "image": M{"pullPolicy": "Always"}},
		"other": M{"y": 2},
	}})
	in.Instance = fstest.MapFS{
		"upstream.app.yaml":   {Data: []byte("env: [A]\nimage: {tag: f}\n")},
		"upstream.other.yaml": {Data: []byte("x: 1\n")},
		"a.yaml":              {Data: []byte("a: 1\nb: 1\n")},
		"b.yaml":              {Data: []byte("b: 2\n")},
	}
	ctx, values, ups, _, err := buildContext(in, svc)
	if err != nil {
		t.Fatal(err)
	}
	want := M{
		"app":   M{"env": []any{"A", "C"}, "image": M{"tag": "f", "pullPolicy": "Always"}},
		"files": M{"a": float64(1), "b": float64(2)},
		"other": M{"y": float64(2)},
	}
	if got := asMap(ctx["values"])["upstream"]; !reflect.DeepEqual(got, want) {
		t.Errorf("values.upstream:\n got %#v\nwant %#v", got, want)
	}
	if got := dig(values, "upstream.app"); !reflect.DeepEqual(got, M{"env": []any{"C"}, "image": M{"pullPolicy": "Always"}}) {
		t.Errorf("merged values: the inline upstream values as written, got %#v", got)
	}
	if len(ups["app"].layers) != 2 || ups["app"].layers[0].src.file != "upstream.app.yaml" || ups["app"].layers[1].src.chart != "app" {
		t.Errorf("upstream sources of app: %#v", ups["app"])
	}
	if _, ok := ups["other"]; ok {
		t.Error("a chart without upstream values has no upstream sources")
	}
}
