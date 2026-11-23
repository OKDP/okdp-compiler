package okdp

import (
	"context"
	"maps"
	"math"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/okdp/okdp-compiler/value"
)

// TestValuesLayers: any layer sets any key, global and global.okdp, a
// connection and a claim included, and changes what an earlier layer
// sets; the settings are global.okdp of the layers, merged in order.
func TestValuesLayers(t *testing.T) {
	files := maps.Clone(pkg)
	files["config.schema.json"] = &fstest.MapFile{Data: []byte(`{"properties": {"replicas": {"type": "integer", "default": 1},
		"db": {"type": "string", "x-okdp-connection-ref": {"contract": "database"}}}}`)}
	in := inputs(
		map[string]any{"global": M{"imageRegistry": "platform.example.org", "okdp": M{"proxy": M{"noProxy": ".svc"}}}, "replicas": 3},
		map[string]any{"global": M{"imageRegistry": "instance.example.org", "okdp": M{"proxy": M{"noProxy": ".local"}}}, "connections": M{"ext": M{"contract": "database", "host": "db.example.org"}}, "db": "ext", "replicas": 2},
	)
	in.Contracts = dbContract
	inst, err := newInstance(context.Background(), files, in, defService)
	if err != nil {
		t.Fatal(err)
	}
	if g := value.AsMap(inst.values["global"]); g["imageRegistry"] != "instance.example.org" || value.Dig(g, "okdp.cluster.domain") != "example.org" || value.Dig(g, "okdp.proxy.noProxy") != ".local" {
		t.Errorf("global: %#v", g)
	}
	if inst.okdp["proxy"] == nil || value.Dig(inst.okdp, "proxy.noProxy") != ".local" {
		t.Errorf("settings: %#v", inst.okdp)
	}
	if v := value.AsMap(inst.scope["values"]); v["replicas"] != float64(2) || value.Dig(v, "db.host") != "db.example.org" {
		t.Errorf("values: %#v", v)
	}
}

// TestValuesLayersGoTypes: layers a Go caller builds with other types than
// M are the same values read from a file: a map[string]string global merges
// with the global of an earlier layer rather than replacing it, an int32 is
// a number. What values cannot hold fails, its layer and path named.
func TestValuesLayersGoTypes(t *testing.T) {
	in := inputs(map[string]any{"global": map[string]string{"imageRegistry": "platform.example.org"}}, map[string]any{"replicas": int32(2)})
	res, err := Compile(context.Background(), def, in)
	if err != nil {
		t.Fatal(err)
	}
	app := res.Values["app"]
	if g := value.AsMap(app["global"]); g["imageRegistry"] != "platform.example.org" || value.Dig(g, "okdp.cluster.domain") != "example.org" {
		t.Errorf("global: %#v", g)
	}
	if app["replicaCount"] != float64(2) {
		t.Errorf("replicaCount: %#v", app["replicaCount"])
	}
	for name, tc := range map[string]struct {
		layers []map[string]any
		want   string
	}{
		"a NaN":  {[]map[string]any{{"global": M{"proxy": M{"no.proxy": math.NaN()}}}}, `values layer 2: json: cannot marshal from Go float64 within "/global/proxy/no.proxy": unsupported value: NaN`},
		"a func": {[]map[string]any{{}, {"hooks": []any{func() {}}}}, `values layer 3: json: cannot marshal from Go func() within "/hooks/0"`},
	} {
		if _, err := Compile(context.Background(), def, inputs(tc.layers...)); err == nil || err.Error() != tc.want {
			t.Errorf("%s: got %v, want %s", name, err, tc.want)
		}
	}
}

// TestHostSettings: the host's settings (Inputs.Settings) join the
// settings of the layers: the platform schema checks them, the hooks,
// Result.Settings and SettingsOf read them, and a package's requirement
// holds with them; they reach no values: neither the mapping's global nor
// the final values of a chart. A value both give is refused, a key
// added to a map of the host's values among them, and so is a value that
// values cannot hold.
func TestHostSettings(t *testing.T) {
	ctx := context.Background()
	in := inputs(M{"global": M{"okdp": M{"team": M{"name": "data"}}}})
	in.Values = in.Values[1:] // cluster.domain, which def requires, is the host's
	in.Settings = map[string]any{"cluster": map[string]string{"domain": "example.org"}, "team": M{"members": M{"ann": "lead"}}}
	inst, err := newInstance(ctx, pkg, in, defService)
	if err != nil {
		t.Fatal(err)
	}
	if want := (M{"okdp": M{"team": M{"name": "data"}}}); !reflect.DeepEqual(inst.scope["global"], want) || !reflect.DeepEqual(inst.values["global"], want) {
		t.Errorf("global: %#v, %#v", inst.scope["global"], inst.values["global"])
	}
	d := *def
	d.Okdp, d.Values = nil, []byte("app:\n  team: ${{ global.okdp.team }}\n")
	res, err := Compile(ctx, &d, in)
	if err != nil {
		t.Fatal(err)
	}
	if value.Dig(res.Settings, "cluster.domain") != "example.org" || value.Dig(res.Settings, "team.name") != "data" || value.Dig(res.Settings, "team.members.ann") != "lead" {
		t.Errorf("Result.Settings: %#v", res.Settings)
	}
	if app := res.Values["app"]; !reflect.DeepEqual(app["team"], M{"name": "data"}) || !reflect.DeepEqual(app["global"], M{"okdp": M{"team": M{"name": "data"}}}) {
		t.Errorf("the final values: %#v", app)
	}
	if s, err := SettingsOf(ctx, in); err != nil || value.Dig(s, "cluster.domain") != "example.org" {
		t.Errorf("SettingsOf: %v %#v", err, s)
	}
	r := hookRules(t)
	hd, err := LoadDefinition(hookPackage(hookOkdp), r)
	if err != nil {
		t.Fatal(err)
	}
	hooked := inputs(M{"objects": []any{labelled("a", "team")}})
	hooked.Rules, hooked.Settings = r, map[string]any{"label": "owner"}
	if _, err := Compile(ctx, hd, hooked); err == nil || err.Error() != "demo: objects: ConfigMap/a: metadata.labels: no label owner in demo" {
		t.Errorf("the objects hook: %v", err)
	}
	for name, tc := range map[string]struct {
		layer    any
		settings M
		want     string
	}{
		"a value":        {M{"team": M{"name": "data"}}, M{"team": M{"name": "ops"}}, "global.okdp.team: set both by the values (Inputs.Values) and by the host's settings (Inputs.Settings)"},
		"a member":       {M{"team": M{"members": M{"bob": "dev"}}}, M{"team": M{"members": M{"ann": "lead"}}}, "global.okdp.team.members: set both"},
		"an empty map":   {M{"team": M{"members": M{"bob": "dev"}}}, M{"team": M{"members": M{}}}, "global.okdp.team.members: set both"},
		"a map, a value": {M{"team": "data"}, M{"team": M{"name": "ops"}}, "global.okdp.team: set both"},
		"a null":         {M{"team": nil}, M{"team": M{}}, "global.okdp.team: set both"},
		"not a map":      {"x", M{"team": "x"}, "global.okdp: set both"},
		"the schema":     {M{}, M{"proxy": M{"httpProxy": true}}, "at '/global/okdp/proxy/httpProxy': got boolean, want string"},
		"a func":         {M{}, M{"hooks": []any{func() {}}}, `Inputs.Settings: json: cannot marshal from Go func() within "/hooks/0"`},
	} {
		in := inputs(M{"global": M{"okdp": tc.layer}})
		in.Settings = tc.settings
		if _, err := Compile(ctx, def, in); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want %q", name, err, tc.want)
		}
	}
}
