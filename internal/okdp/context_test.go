package okdp

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

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
// as platform: by default no proxy; the proxy settings as environment
// variables, those that are set only. platform.access is the security
// set's.
func TestPlatformDefaults(t *testing.T) {
	for _, tc := range []struct{ okdp, want M }{
		{M{"tls": M{"clusterIssuer": "ca"}}, M{"proxyEnv": M{}}},
		{M{"proxy": M{"httpProxy": "http://p:3128", "noProxy": ".svc"}}, M{"proxyEnv": M{"HTTP_PROXY": "http://p:3128", "NO_PROXY": ".svc"}}},
	} {
		if p, err := platformCtx(t, tc.okdp); err != nil || !reflect.DeepEqual(p, tc.want) {
			t.Errorf("%v: got  %#v (%v)\nwant %#v", tc.okdp, p, err, tc.want)
		}
	}
}

// TestPlatformSchema: global.okdp checked against platform.schema.json,
// every violation at once, by path, as Helm reads a chart's values: a null
// deletes a setting's default, and is refused where there is none; the
// other keys are not checked, those of the other sets among them (the
// security set's policy and access, a host's network): each set checks
// its own.
func TestPlatformSchema(t *testing.T) {
	for name, tc := range map[string]struct {
		global any
		want   string // the violations, "" for none
	}{
		"global":      {"x", "- at '/global': got string, want object"},
		"global.okdp": {M{"okdp": []any{}}, "- at '/global/okdp': got array, want object"},
		"each map and setting": {M{"okdp": M{"tls": "t", "proxy": M{"httpProxy": true, "noProxy": float64(1)}}},
			"- at '/global/okdp/proxy': validation failed\n  - at '/global/okdp/proxy/httpProxy': got boolean, want string\n  - at '/global/okdp/proxy/noProxy': got number, want string\n- at '/global/okdp/tls': got string, want object"},
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
		"null, a default":    {M{"okdp": M{"secrets": M{"serviceAccount": nil, "refreshInterval": ""}}}, ""},
		"null, no default":   {M{"okdp": M{"proxy": M{"noProxy": nil}, "tls": nil, "charts": M{"objects": nil}}}, "- at '/global/okdp/charts/objects': got null, want object\n- at '/global/okdp/proxy/noProxy': got null, want string\n- at '/global/okdp/tls': got null, want object"},
		"no global":          {nil, ""},
		"unread is unread":   {M{"okdp": M{"cluster": M{"domain": 1}, "monitoring": M{"enabled": "yes"}}, "imageRegistry": 1}, ""},
		"keys of other sets": {M{"okdp": M{"policy": M{"privileged": "x", "projectApiGroup": []any{"x"}}, "access": M{"groups": M{"devs": "editor"}}, "network": M{"policy": "yes"}}}, ""},
	} {
		t.Run(name, func(t *testing.T) {
			heading := "the values (Inputs.Values) don't meet the specifications of the schema of global.okdp"
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

// TestContextValues: the parameters the mapping sees, without global and
// connections; global as written, the platform's okdp and a project's own
// keys beside it; and the merged values left as written.
func TestContextValues(t *testing.T) {
	in := inputs(M{"replicas": float64(2), "connections": M{}})
	in.Values = append(in.Values, map[string]any{"global": M{"project": M{"name": "demo"}}})
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

// TestPlatformFromValues: whatever defaults a package's schema gives
// global, in any shape, the settings an instance reads are global.okdp of
// its values layers: the instance's (inst.okdp) are those of that view, its defaults applied,
// and global.okdp of the mapping's global and of the parameters is that
// view as written; a schema whose defaults give global.okdp is refused, as
// one that declares it.
func TestPlatformFromValues(t *testing.T) {
	okdpDefault := M{"policy": M{"projectApiGroups": []any{"evil.example.org"}}, "secrets": M{"store": "evil"}, "charts": M{"objects": M{"repository": "oci://evil.example.org"}}}
	shapes := []any{
		M{"type": "object", "default": M{"okdp": okdpDefault}},
		M{"default": M{"okdp": nil}},
		M{"default": M{"okdp": M{"provisioning": M{"pg": M{"default": "evil"}}}}},
		M{"default": M{"imageRegistry": "r.example.org"}},
		M{"default": "x"},
		M{"default": nil},
		M{"default": []any{M{"okdp": okdpDefault}}},
		M{"properties": M{"okdp": M{"default": okdpDefault}}},
		M{"properties": M{"okdp": M{"properties": M{"policy": M{"default": M{"projectApiGroups": []any{"evil.example.org"}}}}}}},
		M{"properties": M{"imageRegistry": M{"default": "r.example.org"}}},
		M{"properties": M{"x": M{"default": M{"okdp": okdpDefault}}}},
		M{"allOf": []any{M{"properties": M{"okdp": M{"default": okdpDefault}}}}},
	}
	contexts := [][]map[string]any{
		{platformValues},
		{platformValues, {"global": M{"okdp": M{"policy": M{"projectApiGroups": []any{"infra.example.org"}}}}}},
		{platformValues, {"global": M{"project": "demo", "okdp": M{"provisioning": M{"pg": M{"classes": M{"small": M{"provider": "demo/pg"}}}}}}}},
		{{"global": M{"project": "demo"}}},
	}
	refusals := 0
	for i, shape := range shapes {
		doc, err := json.Marshal(M{"type": "object", "properties": M{"global": shape}})
		if err != nil {
			t.Fatal(err)
		}
		files := withSchema(string(doc)).Files
		for j, ctx := range contexts {
			in := inputs()
			in.Values = ctx
			inst, err := newInstance(context.Background(), files, in, defService)
			if err != nil {
				if !strings.Contains(err.Error(), "config.schema.json: global.okdp is the platform's settings") {
					t.Errorf("shape %d %s, context %d: %v", i, doc, j, err)
				}
				refusals++
				continue
			}
			// The view, of the values layers alone: their global.okdp.
			view := M{}
			for _, l := range ctx {
				if okdp, set := value.AsMap(l["global"])["okdp"]; set {
					view = value.Merge(view, M{"global": M{"okdp": okdp}})
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
			if slices.Contains(value.AsList(value.Dig(inst.okdp, "policy.projectApiGroups")), any("evil.example.org")) || storeName(inst.okdp) == "evil" {
				t.Errorf("shape %d %s, context %d: a package schema's default reached the platform's policy", i, doc, j)
			}
		}
	}
	if refusals == 0 {
		t.Error("no shape refused")
	}
}
