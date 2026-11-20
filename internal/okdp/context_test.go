package okdp

import (
	"context"
	"encoding/json"
	"maps"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/okdp/okdp-compiler/chart"
	ruleset "github.com/okdp/okdp-compiler/internal/rules"
	"github.com/okdp/okdp-compiler/value"
)

// platformCtx: the platform context of the in-memory chart, with these
// global.okdp values.
func platformCtx(t *testing.T, okdp M) (M, error) {
	t.Helper()
	inst, err := newInstance(context.Background(), pkg, inputs(M{"global": M{"okdp": okdp}}), defService)
	if err != nil {
		return nil, err
	}
	return value.AsMap(inst.scope["platform"]), nil
}

// TestPlatformDefaults: what the mandatory set's scope gives a mapping
// as platform by default: no proxy, the project's group in every role.
func TestPlatformDefaults(t *testing.T) {
	p, err := platformCtx(t, M{"tls": M{"clusterIssuer": "ca"}})
	if err != nil {
		t.Fatal(err)
	}
	want := M{
		"proxyEnv": M{},
		"access":   M{"viewers": []any{"okdp-project-demo"}, "editors": []any{"okdp-project-demo"}, "admins": []any{}, "platformAdmins": []any{}},
	}
	if !reflect.DeepEqual(p, want) {
		t.Errorf("got  %#v\nwant %#v", p, want)
	}
}

// TestPlatformProxy: the proxy settings become environment variables,
// those that are set only.
func TestPlatformProxy(t *testing.T) {
	p, err := platformCtx(t, M{"proxy": M{"httpProxy": "http://p:3128", "noProxy": ".svc"}})
	if err != nil {
		t.Fatal(err)
	}
	if want := (M{"HTTP_PROXY": "http://p:3128", "NO_PROXY": ".svc"}); !reflect.DeepEqual(p["proxyEnv"], want) {
		t.Errorf("proxy env: %#v", p["proxyEnv"])
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
		"each map and setting": {M{"okdp": M{"proxy": "p", "network": M{"policy": "true", "ingressNamespaces": []any{"Nginx"}}}},
			"- at '/global/okdp/network': validation failed\n  - at '/global/okdp/network/ingressNamespaces/0': 'Nginx' does not match pattern '^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$'\n  - at '/global/okdp/network/policy': got string, want boolean\n- at '/global/okdp/proxy': got string, want object"},
		"secrets": {M{"okdp": M{"secrets": M{"store": true, "provider": "vault", "refreshInterval": float64(60)}}},
			"- at '/global/okdp/secrets/provider': got string, want object\n- at '/global/okdp/secrets/refreshInterval': got number, want string\n- at '/global/okdp/secrets/store': got boolean, want string"},
		"tls": {M{"okdp": M{"tls": M{"clusterIssuer": true, "caBundleSecret": nil}, "certificateIssuers": M{"selfSigned": M{"name": "ca"}}}},
			"- at '/global/okdp/certificateIssuers': false schema\n- at '/global/okdp/tls': validation failed\n  - at '/global/okdp/tls/caBundleSecret': got null, want string\n  - at '/global/okdp/tls/clusterIssuer': got boolean, want string"},
		"serviceCatalog": {M{"okdp": M{"serviceCatalog": M{"categories": []any{}}}}, "- at '/global/okdp/serviceCatalog': false schema"},
		"charts": {M{"okdp": M{"charts": M{"objects": "okdp-objects", "other": M{"version": float64(1)}, "extra": nil}}},
			"- at '/global/okdp/charts': additional properties 'extra', 'other' not allowed\n- at '/global/okdp/charts/objects': got string, want object"},
		"classes": {M{"okdp": M{"provisioning": M{
			"server":   M{"default": "standard", "classes": M{"standard": M{"provider": "data/pg", "request": M{"size": "1Gi"}}}},
			"database": M{"default": float64(1), "other": true, "classes": M{"Fast": M{"provider": "data/pg"}, "slow": M{"request": "1Gi", "tier": "b"}}},
		}}},
			"- at '/global/okdp/provisioning/database': additional properties 'other' not allowed\n- at '/global/okdp/provisioning/database/classes': validation failed\n  - at '/global/okdp/provisioning/database/classes': additional properties 'Fast' not allowed\n  - at '/global/okdp/provisioning/database/classes/slow': validation failed\n    - at '/global/okdp/provisioning/database/classes/slow': additional properties 'tier' not allowed\n    - at '/global/okdp/provisioning/database/classes/slow': missing property 'provider'\n    - at '/global/okdp/provisioning/database/classes/slow/request': got string, want object\n- at '/global/okdp/provisioning/database/default': got number, want string"},
		"null, a default":  {M{"okdp": M{"secrets": M{"serviceAccount": nil, "refreshInterval": ""}}}, ""},
		"null, no default": {M{"okdp": M{"proxy": M{"noProxy": nil}, "access": nil, "charts": M{"objects": nil}}}, "- at '/global/okdp/access': got null, want object\n- at '/global/okdp/charts/objects': got null, want object\n- at '/global/okdp/proxy/noProxy': got null, want string"},
		"no global":        {nil, ""},
		"unread is unread": {M{"okdp": M{"cluster": M{"domain": 1}, "monitoring": M{"enabled": "yes"}}, "imageRegistry": 1}, ""},
	} {
		t.Run(name, func(t *testing.T) {
			heading := "context values (Inputs.Context) don't meet the specifications of the schema of global.okdp"
			_, err := ruleset.SchemaValues(Mandatory().PlatformSchema(), heading, M{"global": value.Copy(tc.global)})
			want := heading + ":\n" + tc.want
			if (err == nil) != (tc.want == "") || (err != nil && err.Error() != want) {
				t.Errorf("got %v, want %q", err, tc.want)
			}
		})
	}
	// Checked before the platform context is derived: false is not read as
	// no proxy; a null deletes a default, and the setting is absent; an
	// empty string is a value.
	if _, err := platformCtx(t, M{"proxy": false}); err == nil || !strings.HasSuffix(err.Error(), "- at '/global/okdp/proxy': got boolean, want object") {
		t.Errorf("compile: got %v", err)
	}
	settings, err := platformSettings(Mandatory(), M{"global": M{"okdp": M{"secrets": M{"serviceAccount": nil, "refreshInterval": ""}}}})
	secrets := value.AsMap(settings["secrets"])
	if _, set := secrets["serviceAccount"]; err != nil || set || secrets["refreshInterval"] != "" {
		t.Errorf("serviceAccount: null: %v %#v", err, secrets)
	}
}

// TestContextErrors: unreadable package files.
func TestContextErrors(t *testing.T) {
	ch := maps.Clone(pkg)
	delete(ch, "config.schema.json")
	ch["config.schema.json/x"] = &fstest.MapFile{}
	if _, err := newInstance(context.Background(), ch, inputs(), defService); err == nil || !strings.Contains(err.Error(), "read config.schema.json") {
		t.Errorf("config.schema.json a directory: got %v", err)
	}
}

// TestContextValues: the parameters the mapping sees, without global and
// connections; global as written, the platform's okdp and a project's own
// keys beside it; and the merged values left as written.
func TestContextValues(t *testing.T) {
	in := inputs(M{"replicas": float64(2), "connections": M{}})
	in.Context = append(in.Context, ContextLayer{Level: 1, Values: map[string]any{"global": M{"project": M{"name": "demo"}}}})
	inst, err := newInstance(context.Background(), pkg, in, defService)
	if err != nil {
		t.Fatal(err)
	}
	if want := (M{"replicas": float64(2)}); !reflect.DeepEqual(inst.scope["values"], want) {
		t.Errorf("values in the context: %#v", inst.scope["values"])
	}
	if want := (M{"okdp": M{"cluster": M{"domain": "example.org"}}, "project": M{"name": "demo"}}); !reflect.DeepEqual(inst.scope["global"], want) {
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
	inst, err := newInstance(context.Background(), pkg, inputs(M{"upstream": up}), svc)
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
	okdpDefault := M{"policy": M{"privileged": []any{"demo"}}, "secrets": M{"store": "evil"}, "charts": M{"objects": M{"repository": "oci://evil.example.org"}}}
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
			inst, err := newInstance(context.Background(), files, in, defService)
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
			want, err := platformSettings(Mandatory(), view)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(inst.okdp, want) {
				t.Errorf("shape %d %s, context %d: the platform's settings\n got %#v\nwant %#v", i, doc, j, inst.okdp, want)
			}
			written, set := value.AsMap(view["global"])["okdp"]
			for name, g := range map[string]M{"the mapping's global": value.AsMap(inst.scope["global"]), "the parameters' global": value.AsMap(inst.values["global"])} {
				if got, has := g["okdp"]; has != set || !reflect.DeepEqual(got, written) {
					t.Errorf("shape %d %s, context %d: global.okdp of %s\n got %#v\nwant %#v", i, doc, j, name, got, written)
				}
			}
			if Privileged(inst.okdp, "demo") || storeName(inst.okdp) == "evil" {
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

// TestPlatformAccess: platform.access, the groups of the project (its
// file's access, the default group an editor without one) and the
// platform's administrators: each role's list holds the higher roles'
// groups and the administrators, sorted, once each. A refused access
// refuses the instance.
func TestPlatformAccess(t *testing.T) {
	list := func(g ...string) []any {
		out := []any{}
		for _, x := range g {
			out = append(out, x)
		}
		return out
	}
	project := func(access any) []ContextLayer {
		return []ContextLayer{{Level: 1, Values: map[string]any{"access": access}}}
	}
	for name, tc := range map[string]struct {
		okdp   M
		layers []ContextLayer
		want   M
	}{
		"the default group":             {M{}, nil, M{"viewers": list("okdp-project-demo"), "editors": list("okdp-project-demo"), "admins": list(), "platformAdmins": list()}},
		"roles":                         {M{}, project(M{"readers": "viewer", "devs": "editor", "leads": "admin"}), M{"viewers": list("devs", "leads", "readers"), "editors": list("devs", "leads"), "admins": list("leads"), "platformAdmins": list()}},
		"no group":                      {M{}, project(M{}), M{"viewers": list(), "editors": list(), "admins": list(), "platformAdmins": list()}},
		"the platform's administrators": {M{"access": M{"admins": []any{"ops", "okdp-admins", "ops"}}}, project(M{"devs": "editor", "ops": "viewer"}), M{"viewers": list("devs", "okdp-admins", "ops"), "editors": list("devs", "okdp-admins", "ops"), "admins": list("okdp-admins", "ops"), "platformAdmins": list("okdp-admins", "ops")}},
	} {
		t.Run(name, func(t *testing.T) {
			in := inputs(M{"global": M{"okdp": tc.okdp}})
			in.Context = append(in.Context, tc.layers...)
			inst, err := newInstance(context.Background(), pkg, in, defService)
			if err != nil {
				t.Fatal(err)
			}
			if got := value.AsMap(inst.scope["platform"])["access"]; !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got  %#v\nwant %#v", got, tc.want)
			}
		})
	}
	// A refused access refuses the instance.
	in := inputs()
	in.Context = append(in.Context, project(M{"a/b": "viewer"})...)
	if _, err := newInstance(context.Background(), pkg, in, defService); err == nil || !strings.HasPrefix(err.Error(), "the project's file (level 1): access.a/b: a group name") {
		t.Errorf("a refused access: got %v", err)
	}
}
