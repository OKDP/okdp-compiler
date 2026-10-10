package compiler

import (
	"context"
	"maps"
	"math"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/okdp/okdp-compiler/internal/value"
)

// TestValuesLayers: global is the platform's. An instance layer sets only
// the keys of global the parameter schema declares, never okdp, over the
// platform's layers, and no "{{" under any key; the platform's layers are
// not checked.
func TestValuesLayers(t *testing.T) {
	files := maps.Clone(pkg)
	files["config.schema.json"] = &fstest.MapFile{Data: []byte(`{"properties": {"replicas": {"type": "integer", "default": 1},
		"global": {"properties": {"imageRegistry": {"type": "string"}, "okdp": {"type": "object"}}}}}`)}
	platform := []map[string]any{{"global": M{"imageRegistry": "platform.example.org"}}}
	for name, tc := range map[string]struct {
		instance []map[string]any
		want     string // the error, none when empty
	}{
		"a declared key": {[]map[string]any{{"global": M{"imageRegistry": "instance.example.org"}}}, ""},
		"okdp":           {[]map[string]any{{"global": M{"okdp": M{"ingress": M{"suffix": "other.example.org"}}}}}, "values layer 1: global.okdp is set by the platform (--platform, Inputs.Platform)"},
		"another key":    {[]map[string]any{{"replicas": 2}, {"global": M{"imageRegistry": "r", "proxy": "p"}}}, "values layer 2: global.proxy is set by the platform"},
		"not a map":      {[]map[string]any{{"global": nil}}, "values layer 1: global is set by the platform"},
		"a template":     {[]map[string]any{{"replicas": 2}, {"connections": M{"ext": M{"tags": []any{"{{ .Release.Name }}"}}}}}, `values layer 2: connections.ext.tags[0]: the value contains "{{"`},
		"a template key": {[]map[string]any{{"upstream": M{"app": M{"podAnnotations": M{"{{ x }}": "y"}}}}}, `values layer 1: upstream.app.podAnnotations.{{ x }}: the key contains "{{"`},
	} {
		t.Run(name, func(t *testing.T) {
			in := inputs()
			in.Platform, in.Values = append(in.Platform, platform...), tc.instance
			res, err := Compile(context.Background(), withFiles(files), in)
			if tc.want != "" {
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Errorf("got %v, want an error containing %q", err, tc.want)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if g := value.AsMap(res.Values["app"]["global"]); g["imageRegistry"] != "instance.example.org" || value.Dig(g, "okdp.ingress.suffix") != "example.org" {
				t.Errorf("global: %#v", g)
			}
		})
	}
	in := inputs()
	in.Platform = append(in.Platform, map[string]any{"global": M{"imageRegistry": "{{ .Values.registry }}"}})
	if _, err := Compile(context.Background(), withFiles(files), in); err != nil {
		t.Errorf("a template in a platform layer: %v", err)
	}
}

// TestProjectLayers: a project layer adds to the platform's values, it
// changes none: not a value the platform sets (the same one included, a
// null too, a parent of it), not a connection it gives, even by a field it
// does not set. A later project layer changes an earlier one; no "{{".
func TestProjectLayers(t *testing.T) {
	platform := map[string]any{
		"global":      M{"labels": M{"okdp.io/tier": "gold"}, "okdp": M{"proxy": nil}},
		"connections": M{"shared-db": M{"contract": "database", "host": "db.example.org"}},
	}
	for name, tc := range map[string]struct {
		project []map[string]any
		want    string // the error, none when empty
	}{
		"added": {[]map[string]any{
			{"global": M{"storageClass": "fast", "okdp": M{"project": M{"name": "demo"}}}, "connections": M{"team-db": M{"contract": "database", "host": "a"}}},
			{"connections": M{"team-db": M{"host": "team-db.example.org"}}},
		}, ""},
		"a platform value":       {[]map[string]any{{"global": M{"okdp": M{"ingress": M{"suffix": "other.example.org"}}}}}, "project layer 1: global.okdp.ingress.suffix is set by the platform (--platform, Inputs.Platform): a project adds values, it does not change the platform's"},
		"the same value":         {[]map[string]any{{"global": M{"okdp": M{"ingress": M{"suffix": "example.org"}}}}}, "project layer 1: global.okdp.ingress.suffix is set by the platform"},
		"a platform null":        {[]map[string]any{{"global": M{"okdp": M{"proxy": M{"httpProxy": "http://proxy:3128"}}}}}, "project layer 1: global.okdp.proxy is set by the platform"},
		"a parent":               {[]map[string]any{{}, {"global": M{"okdp": nil}}}, "project layer 2: global.okdp is set by the platform"},
		"a key with dots":        {[]map[string]any{{"global": M{"labels": M{"okdp.io/tier": "silver"}}}}, `project layer 1: global.labels["okdp.io/tier"] is set by the platform`},
		"a platform connection":  {[]map[string]any{{"connections": M{"shared-db": M{"port": 5433}}}}, "project layer 1: connections.shared-db is set by the platform"},
		"a template":             {[]map[string]any{{}, {"connections": M{"team-db": M{"host": "{{ .Release.Name }}"}}}}, `project layer 2: connections.team-db.host: the value contains "{{"`},
		"a value of no layer":    {[]map[string]any{{"replicas": 3}}, ""},
		"a value of no platform": {[]map[string]any{{"global": M{"okdp": M{"ingress": M{"className": "nginx"}}}}}, ""},
	} {
		t.Run(name, func(t *testing.T) {
			in := inputs()
			in.Platform, in.Project = append(in.Platform, platform), tc.project
			res, err := Compile(context.Background(), def, in)
			if tc.want != "" {
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Errorf("got %v, want an error containing %q", err, tc.want)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			g := value.AsMap(res.Values["app"]["global"])
			if value.Dig(g, "okdp.ingress.suffix") != "example.org" || value.Dig(g, "labels") == nil {
				t.Errorf("global: %#v", g)
			}
		})
	}
	// The project's values are merged after the platform's, before the
	// instance's: its global reaches every chart.
	in := inputs(map[string]any{"replicas": 2})
	in.Project = []map[string]any{{"global": M{"storageClass": "fast"}, "replicas": 3}}
	res, err := Compile(context.Background(), def, in)
	if err != nil {
		t.Fatal(err)
	}
	if app := res.Values["app"]; value.Dig(app, "global.storageClass") != "fast" || app["replicaCount"] != float64(2) {
		t.Errorf("app: %#v", app)
	}
}

// TestValuesLayersGoTypes: layers a Go caller builds with other types than
// M are the same values read from a file: a map[string]string global merges
// with the global of an earlier layer rather than replacing it, an int32 is
// a number. What values cannot hold fails, its layer and path named.
func TestValuesLayersGoTypes(t *testing.T) {
	in := inputs(map[string]any{"replicas": int32(2)})
	in.Platform = append(in.Platform, map[string]any{"global": map[string]string{"imageRegistry": "platform.example.org"}})
	res, err := Compile(context.Background(), def, in)
	if err != nil {
		t.Fatal(err)
	}
	app := res.Values["app"]
	if g := value.AsMap(app["global"]); g["imageRegistry"] != "platform.example.org" || value.Dig(g, "okdp.ingress.suffix") != "example.org" {
		t.Errorf("global: %#v", g)
	}
	if app["replicaCount"] != float64(2) {
		t.Errorf("replicaCount: %#v", app["replicaCount"])
	}
	for name, tc := range map[string]struct {
		platform, instance []map[string]any
		want               string
	}{
		"platform": {[]map[string]any{{"global": M{"proxy": M{"no.proxy": math.NaN()}}}}, nil, `platform layer 1: json: cannot marshal from Go float64 within "/global/proxy/no.proxy": unsupported value: NaN`},
		"instance": {nil, []map[string]any{{}, {"hooks": []any{func() {}}}}, `values layer 2: json: cannot marshal from Go func() within "/hooks/0"`},
	} {
		in := inputs()
		in.Platform, in.Values = tc.platform, tc.instance
		if _, err := Compile(context.Background(), def, in); err == nil || err.Error() != tc.want {
			t.Errorf("%s: got %v, want %s", name, err, tc.want)
		}
	}
}
