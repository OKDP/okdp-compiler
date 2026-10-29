package okdp

import (
	"context"
	"maps"
	"math"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/okdp/okdp-compiler/internal/value"
)

// TestValuesLayers: global, connections and claims are the context's. An
// instance layer sets only the keys of global the parameter schema
// declares, never okdp, over the context's layers, no connections and no
// claims, to any value, and no "{{" under any key; the platform's level is
// not checked.
func TestValuesLayers(t *testing.T) {
	files := maps.Clone(pkg)
	files["config.schema.json"] = &fstest.MapFile{Data: []byte(`{"properties": {"replicas": {"type": "integer", "default": 1},
		"global": {"properties": {"imageRegistry": {"type": "string"}, "okdp": {"type": "object"}}}}}`)}
	platform := ContextLayer{Values: map[string]any{"global": M{"imageRegistry": "platform.example.org"}}}
	for name, tc := range map[string]struct {
		instance []map[string]any
		want     string // the error, none when empty
	}{
		"a declared key": {[]map[string]any{{"global": M{"imageRegistry": "instance.example.org"}}}, ""},
		"okdp":           {[]map[string]any{{"global": M{"okdp": M{"ingress": M{"suffix": "other.example.org"}}}}}, "values layer 1: global.okdp is set by the context (Inputs.Context)"},
		"another key":    {[]map[string]any{{"replicas": 2}, {"global": M{"imageRegistry": "r", "proxy": "p"}}}, "values layer 2: global.proxy is set by the context"},
		"not a map":      {[]map[string]any{{"global": nil}}, "values layer 1: global is set by the context"},
		"connections":    {[]map[string]any{{"replicas": 2}, {"connections": M{}}}, "values layer 2: connections are given by the context (Inputs.Context): an instance's own in a context layer of its level"},
		"a null":         {[]map[string]any{{"connections": nil}}, "values layer 1: connections are given by the context"},
		"claims":         {[]map[string]any{{"claims": M{"reports": M{"contract": "database"}}}}, "values layer 1: claims are declared by the context (Inputs.Context): a project's, of level 1 or above"},
		"a template":     {[]map[string]any{{"replicas": 2}, {"upstream": M{"app": M{"tags": []any{"{{ .Release.Name }}"}}}}}, `values layer 2: upstream.app.tags[0]: the value contains "{{"`},
		"a template key": {[]map[string]any{{"upstream": M{"app": M{"podAnnotations": M{"{{ x }}": "y"}}}}}, `values layer 1: upstream.app.podAnnotations.{{ x }}: the key contains "{{"`},
	} {
		t.Run(name, func(t *testing.T) {
			in := inputs()
			in.Context, in.Values = append(in.Context, platform), tc.instance
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
	in.Context = append(in.Context, ContextLayer{Values: map[string]any{"global": M{"imageRegistry": "{{ .Values.registry }}"}}})
	if _, err := Compile(context.Background(), withFiles(files), in); err != nil {
		t.Errorf("a template at level 0: %v", err)
	}
}

// TestContextLevels: a level adds to the lower levels' values, it changes
// none: not a value they set (the same one included, a null too, a parent
// of it), not a connection they give, even by a field they do not set, nor
// a claim they declare, nor a class they define or the default class they
// set: it adds classes beside theirs, and the classes of a contract they
// leave out. A
// layer changes an earlier one of its level. Levels are merged lowest
// first, whatever their order in Inputs.Context and their gaps; above level
// 0, no "{{". inputs gives layer 1, at level 0, the platform layer 2.
func TestContextLevels(t *testing.T) {
	platform := ContextLayer{Values: map[string]any{
		"global":      M{"labels": M{"okdp.io/tier": "gold"}, "okdp": M{"registry": nil, "provisioning": M{"server": M{"default": "standard", "classes": M{"standard": M{"provider": "data/pg"}}}}}},
		"connections": M{"shared-db": M{"contract": "database", "host": "db.example.org"}},
	}}
	at := func(level uint, v map[string]any) ContextLayer { return ContextLayer{Level: level, Values: v} }
	for name, tc := range map[string]struct {
		layers []ContextLayer
		want   string // the error, none when empty
	}{
		"added": {[]ContextLayer{
			at(1, map[string]any{"global": M{"storageClass": "fast", "okdp": M{"project": M{"name": "demo"}}}, "connections": M{"team-db": M{"contract": "database", "host": "a"}}}),
			at(1, map[string]any{"connections": M{"team-db": M{"host": "team-db.example.org"}}}),
		}, ""},
		"a platform value":       {[]ContextLayer{at(1, map[string]any{"global": M{"okdp": M{"ingress": M{"suffix": "other.example.org"}}}})}, "context layer 3 (level 1): global.okdp.ingress.suffix is set at level 0 (Inputs.Context): a level adds values, it does not change a lower level's"},
		"the same value":         {[]ContextLayer{at(1, map[string]any{"global": M{"okdp": M{"ingress": M{"suffix": "example.org"}}}})}, "context layer 3 (level 1): global.okdp.ingress.suffix is set at level 0"},
		"a platform null":        {[]ContextLayer{at(1, map[string]any{"global": M{"okdp": M{"registry": M{"host": "registry.example.org"}}}})}, "context layer 3 (level 1): global.okdp.registry is set at level 0"},
		"a parent":               {[]ContextLayer{at(1, map[string]any{}), at(1, map[string]any{"global": M{"okdp": nil}})}, "context layer 4 (level 1): global.okdp is set at level 0"},
		"a key with dots":        {[]ContextLayer{at(1, map[string]any{"global": M{"labels": M{"okdp.io/tier": "silver"}}})}, `context layer 3 (level 1): global.labels["okdp.io/tier"] is set at level 0`},
		"a platform connection":  {[]ContextLayer{at(1, map[string]any{"connections": M{"shared-db": M{"port": 5433}}})}, "context layer 3 (level 1): connections.shared-db is set at level 0"},
		"a template":             {[]ContextLayer{at(1, map[string]any{}), at(1, map[string]any{"connections": M{"team-db": M{"host": "{{ .Release.Name }}"}}})}, `context layer 4 (level 1): connections.team-db.host: the value contains "{{"`},
		"a platform class":       {[]ContextLayer{at(1, map[string]any{"global": M{"okdp": M{"provisioning": M{"server": M{"classes": M{"standard": M{"request": M{"size": "1Gi"}}}}}}}})}, "context layer 3 (level 1): global.okdp.provisioning.server.classes.standard is set at level 0"},
		"the default class":      {[]ContextLayer{at(1, map[string]any{"global": M{"okdp": M{"provisioning": M{"server": M{"default": "fast"}}}}})}, "context layer 3 (level 1): global.okdp.provisioning.server.default is set at level 0 (Inputs.Context): a level adds values, it does not change a lower level's"},
		"a class added":          {[]ContextLayer{at(1, map[string]any{"global": M{"okdp": M{"provisioning": M{"server": M{"classes": M{"fast": M{"provider": "data/pg2"}}}, "s3": M{"default": "standard", "classes": M{"standard": M{"provider": "data/minio"}}}}}}})}, ""},
		"a project's claim":      {[]ContextLayer{at(1, map[string]any{"claims": M{"reports": M{"contract": "database"}}}), at(2, map[string]any{"claims": M{"reports": M{"provider": "data/pg"}}})}, "context layer 4 (level 2): claims.reports is set at level 1"},
		"a value of no layer":    {[]ContextLayer{at(1, map[string]any{"replicas": 3})}, ""},
		"a value of no platform": {[]ContextLayer{at(1, map[string]any{"global": M{"okdp": M{"ingress": M{"className": "nginx"}}}})}, ""},
		"a third level":          {[]ContextLayer{at(1, map[string]any{"global": M{"storageClass": "fast"}}), at(2, map[string]any{"global": M{"storageClass": "slow"}})}, "context layer 4 (level 2): global.storageClass is set at level 1"},
		"a gap":                  {[]ContextLayer{at(5, map[string]any{"global": M{"storageClass": "fast"}}), at(10, map[string]any{"global": M{"storageClass": "slow"}})}, "context layer 4 (level 10): global.storageClass is set at level 5"},
		"in any order":           {[]ContextLayer{at(2, map[string]any{"global": M{"storageClass": "slow"}}), at(1, map[string]any{"global": M{"storageClass": "fast"}})}, "context layer 3 (level 2): global.storageClass is set at level 1"},
		"the lowest level first": {[]ContextLayer{at(2, map[string]any{"global": M{"okdp": M{"registry": "r"}}}), at(1, map[string]any{"global": M{"okdp": M{"registry": "r"}}})}, "context layer 4 (level 1): global.okdp.registry is set at level 0"},
	} {
		t.Run(name, func(t *testing.T) {
			in := inputs()
			in.Context = append(append(in.Context, platform), tc.layers...)
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
	// The context is merged by level, then the instance's values: a higher
	// level's global reaches every chart, the instance's values win.
	in := inputs(map[string]any{"replicas": 2})
	in.Context = append(in.Context, ContextLayer{Level: 1, Values: map[string]any{"global": M{"storageClass": "fast"}, "replicas": 3}})
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
	in.Context = append(in.Context, ContextLayer{Values: map[string]any{"global": map[string]string{"imageRegistry": "platform.example.org"}}})
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
		context  []ContextLayer
		instance []map[string]any
		want     string
	}{
		"context":  {[]ContextLayer{{Level: 1, Values: map[string]any{"global": M{"proxy": M{"no.proxy": math.NaN()}}}}}, nil, `context layer 1 (level 1): json: cannot marshal from Go float64 within "/global/proxy/no.proxy": unsupported value: NaN`},
		"instance": {nil, []map[string]any{{}, {"hooks": []any{func() {}}}}, `values layer 2: json: cannot marshal from Go func() within "/hooks/0"`},
	} {
		in := inputs()
		in.Context, in.Values = tc.context, tc.instance
		if _, err := Compile(context.Background(), def, in); err == nil || err.Error() != tc.want {
			t.Errorf("%s: got %v, want %s", name, err, tc.want)
		}
	}
}
