package okdp

import (
	"encoding/json"
	"maps"
	"math/rand/v2"
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

// TestPlatformDefaults: OIDC defaults to an existing client, no proxy, the
// oidc-dcr Jobs without a CA bundle; the ingress annotations name the
// platform's ClusterIssuer.
func TestPlatformDefaults(t *testing.T) {
	p, err := platformCtx(t, M{"tls": M{"clusterIssuer": "ca"}})
	if err != nil {
		t.Fatal(err)
	}
	want := M{
		"oidc": M{
			"enabled":            true,
			"scope":              "openid profile email groups",
			"clientProvisioning": "existing",
			"existing":           true,
			"dcr":                M{"enabled": false, "caSecret": "", "ttlSeconds": float64(30), "backoffLimit": float64(6)},
		},
		"oidcClientSecret":   "creds-demo-app-oauth2",
		"ingressAnnotations": M{"cert-manager.io/cluster-issuer": "ca"},
		"proxyEnv":           M{},
	}
	if !reflect.DeepEqual(p, want) {
		t.Errorf("got  %#v\nwant %#v", p, want)
	}
}

// TestPlatformIngressAnnotations: the platform's ingress annotations,
// those of its controller, merged over the ClusterIssuer's; a value that is
// not a string refused.
func TestPlatformIngressAnnotations(t *testing.T) {
	p, err := platformCtx(t, M{"tls": M{"clusterIssuer": "ca"}, "ingress": M{"annotations": M{
		"traefik.ingress.kubernetes.io/router.middlewares": "okdp-auth@kubernetescrd",
		"cert-manager.io/cluster-issuer":                   "acme",
	}}})
	if err != nil {
		t.Fatal(err)
	}
	want := M{"cert-manager.io/cluster-issuer": "acme", "traefik.ingress.kubernetes.io/router.middlewares": "okdp-auth@kubernetescrd"}
	if !reflect.DeepEqual(p["ingressAnnotations"], want) {
		t.Errorf("got  %#v\nwant %#v", p["ingressAnnotations"], want)
	}
	if _, err := platformCtx(t, M{"ingress": M{"annotations": M{"a": 1}}}); err == nil || !strings.Contains(err.Error(), "/global/okdp/ingress/annotations/a") {
		t.Errorf("a number: got %v", err)
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
// (global.okdp.oidc.dcr), defaults applied, the image passed as written;
// their CA bundle the platform's (tls.caBundleSecret) unless they set
// theirs, "" for none.
func TestPlatformDcrSettings(t *testing.T) {
	defaults := M{"caSecret": "", "ttlSeconds": float64(30), "backoffLimit": float64(6)}
	bundle := M{"caBundleSecret": "certs-bundle"}
	for name, tc := range map[string]struct{ tls, dcr, want M }{
		"defaults":          {nil, M{"registrationUrl": "u"}, M{"registrationUrl": "u"}},
		"the platform's CA": {bundle, M{}, M{"caSecret": "certs-bundle"}},
		"empty":             {bundle, M{"caSecret": ""}, M{"caSecret": ""}},
		"set":               {bundle, M{"caSecret": "ca", "ttlSeconds": float64(0), "backoffLimit": float64(1), "waitTtlSeconds": float64(60)}, M{"caSecret": "ca", "ttlSeconds": float64(0), "backoffLimit": float64(1), "waitTtlSeconds": float64(60)}},
		"camelCase":         {nil, M{"image": M{"registry": "r", "pullPolicy": "Always", "pullSecrets": []any{"s"}}}, M{"image": M{"registry": "r", "pullPolicy": "Always", "pullSecrets": []any{"s"}}}},
	} {
		t.Run(name, func(t *testing.T) {
			okdp := M{"oidc": M{"clientProvisioning": "dcr", "dcr": tc.dcr}}
			if tc.tls != nil {
				okdp["tls"] = tc.tls
			}
			p, err := platformCtx(t, okdp)
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
		"tls": {M{"okdp": M{"tls": M{"clusterIssuer": true, "caBundleSecret": nil}, "certificateIssuers": M{"selfSigned": M{"name": "ca"}}}},
			"- at '/global/okdp/certificateIssuers': false schema\n- at '/global/okdp/tls': validation failed\n  - at '/global/okdp/tls/caBundleSecret': got null, want string\n  - at '/global/okdp/tls/clusterIssuer': got boolean, want string"},
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
// connections; global as written, the platform's okdp and a project's own
// keys beside it; and the merged values left as written.
func TestContextValues(t *testing.T) {
	in := inputs(M{"replicas": float64(2), "connections": M{}})
	in.Context = append(in.Context, ContextLayer{Level: 1, Values: map[string]any{"global": M{"project": M{"name": "demo"}}}})
	inst, err := newInstance(pkg, in, defService)
	if err != nil {
		t.Fatal(err)
	}
	if want := (M{"replicas": float64(2)}); !reflect.DeepEqual(inst.scope["values"], want) {
		t.Errorf("values in the context: %#v", inst.scope["values"])
	}
	if want := (M{"okdp": M{"ingress": M{"suffix": "example.org"}}, "project": M{"name": "demo"}}); !reflect.DeepEqual(inst.scope["global"], want) {
		t.Errorf("global in the context: %#v", inst.scope["global"])
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

// TestPlatformFromContext: whatever defaults a package's schema gives
// global, in any shape, the platform's settings an instance reads are its
// context's, level 0's and the provisioning classes of the levels above:
// the instance's (inst.okdp) are those of that view, its defaults applied,
// and global.okdp of the mapping's global and of the parameters is that
// view as written; a schema whose defaults give global.okdp is refused, as
// one that declares it.
func TestPlatformFromContext(t *testing.T) {
	okdpDefault := M{"policy": M{"privileged": []any{"demo"}}, "secrets": M{"store": "evil"}, "charts": M{"oidcDcr": M{"repository": "oci://evil.example.org"}}}
	shapes := []any{
		M{"type": "object", "default": M{"okdp": okdpDefault}},
		M{"default": M{"okdp": nil}},
		M{"default": M{"okdp": M{"provisioning": M{"pg": M{"default": "evil"}}}}},
		M{"default": M{"imageRegistry": "r.example.org"}},
		M{"default": "x"},
		M{"default": nil},
		M{"default": []any{M{"okdp": okdpDefault}}},
		M{"properties": M{"okdp": M{"default": okdpDefault}}},
		M{"properties": M{"okdp": M{"properties": M{"policy": M{"default": M{"privileged": []any{"demo"}}}}}}},
		M{"properties": M{"imageRegistry": M{"default": "r.example.org"}}},
		M{"properties": M{"x": M{"default": M{"okdp": okdpDefault}}}},
		M{"allOf": []any{M{"properties": M{"okdp": M{"default": okdpDefault}}}}},
	}
	// And random shapes: defaults of global or of a key of it, okdp among
	// them, maps of these keys, okdp among them, to any value.
	rnd := rand.New(rand.NewPCG(1, 2))
	var gen func(depth int) any
	gen = func(depth int) any {
		switch n := rnd.IntN(6); {
		case n == 0 || depth > 3:
			return []any{"demo", float64(rnd.IntN(3)), nil, true}[rnd.IntN(4)]
		case n == 1:
			return []any{gen(depth + 1)}
		default:
			m := M{}
			for range rnd.IntN(4) {
				m[[]string{"okdp", "default", "properties", "policy", "privileged", "secrets", "provisioning", "global"}[rnd.IntN(8)]] = gen(depth + 1)
			}
			return m
		}
	}
	for range 300 {
		key := []string{"okdp", "x"}[rnd.IntN(2)]
		shapes = append(shapes, M{"default": gen(0)}, M{"properties": M{key: M{"default": gen(0)}}})
	}
	contexts := [][]ContextLayer{
		{{Values: platformValues}},
		{{Values: platformValues}, {Values: map[string]any{"global": M{"okdp": M{"policy": M{"privileged": []any{"infra"}}}}}}},
		{{Values: platformValues}, {Level: 1, Values: map[string]any{"global": M{"project": "demo", "okdp": M{"provisioning": M{"pg": M{"classes": M{"small": M{"provider": "demo/pg"}}}}}}}}},
		{{Level: 1, Values: map[string]any{"global": M{"project": "demo"}}}},
	}
	refusals := 0
	for i, shape := range shapes {
		doc, err := json.Marshal(M{"type": "object", "properties": M{"global": shape}})
		if err != nil {
			t.Fatal(err)
		}
		files := maps.Clone(pkg)
		files["config.schema.json"] = &fstest.MapFile{Data: doc}
		for j, ctx := range contexts {
			in := inputs()
			in.Context = ctx
			inst, err := newInstance(files, in, defService)
			if err != nil {
				if !strings.Contains(err.Error(), "config.schema.json: global.okdp is the platform's settings") {
					t.Errorf("shape %d %s, context %d: %v", i, doc, j, err)
				}
				refusals++
				continue
			}
			// The view, of the context's layers alone: level 0's
			// global.okdp, the provisioning of the levels above.
			view := M{}
			for _, l := range ctx {
				okdp, set := value.AsMap(l.Values["global"])["okdp"]
				switch {
				case set && l.Level == 0:
					view = value.Merge(view, M{"global": M{"okdp": okdp}})
				case set:
					view = value.Merge(view, M{"global": M{"okdp": M{"provisioning": value.AsMap(okdp)["provisioning"]}}})
				}
			}
			want, err := platformSettings(view)
			if err != nil {
				t.Fatal(err)
			}
			// oidc gets what newInstance derives of it (existing,
			// dcr.enabled, dcr.caSecret): compared without them.
			got := value.AsMap(value.Copy(inst.okdp))
			if oidc, ok := got["oidc"].(M); ok {
				delete(oidc, "existing")
				delete(value.AsMap(oidc["dcr"]), "enabled")
				delete(value.AsMap(oidc["dcr"]), "caSecret")
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("shape %d %s, context %d: the platform's settings\n got %#v\nwant %#v", i, doc, j, inst.okdp, want)
			}
			written, set := value.AsMap(view["global"])["okdp"]
			for name, g := range map[string]M{"the mapping's global": value.AsMap(inst.scope["global"]), "the parameters' global": value.AsMap(inst.values["global"])} {
				if got, has := g["okdp"]; has != set || !reflect.DeepEqual(got, written) {
					t.Errorf("shape %d %s, context %d: global.okdp of %s\n got %#v\nwant %#v", i, doc, j, name, got, written)
				}
			}
			if privileged(inst.okdp, "demo") || storeName(inst.okdp) == "evil" {
				t.Errorf("shape %d %s, context %d: a package schema's default reached the platform's policy", i, doc, j)
			}
		}
	}
	if refusals == 0 {
		t.Error("no shape refused")
	}
	for _, shape := range shapes[:3] {
		doc, _ := json.Marshal(M{"properties": M{"global": shape}})
		if _, err := chart.ParseSchema("x", doc); err != nil {
			continue
		}
		if _, err := chart.ReadParameterSchema(fstest.MapFS{"config.schema.json": {Data: doc}}); err == nil || !strings.Contains(err.Error(), "a package schema gives it no default (properties.global.default)") {
			t.Errorf("%s: got %v, want the default of global.okdp refused", doc, err)
		}
	}
}
