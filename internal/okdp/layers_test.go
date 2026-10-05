package okdp

import (
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
	files["values.schema.json"] = &fstest.MapFile{Data: []byte(`{"properties": {"replicas": {"type": "integer"},
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
			in.Platform, in.Values = platform, tc.instance
			res, err := Compile(withFiles(files), in)
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
	in.Platform = []map[string]any{{"global": M{"imageRegistry": "{{ .Values.registry }}"}}}
	if _, err := Compile(withFiles(files), in); err != nil {
		t.Errorf("a template in a platform layer: %v", err)
	}
}

// TestValuesLayersGoTypes: layers a Go caller builds with other types than
// M are the same values read from a file: a map[string]string global merges
// with the defaults of values.yaml rather than replacing them, an int32 is
// a number. What values cannot hold fails, its layer and path named.
func TestValuesLayersGoTypes(t *testing.T) {
	in := inputs(map[string]any{"replicas": int32(2)})
	in.Platform = []map[string]any{{"global": map[string]string{"imageRegistry": "platform.example.org"}}}
	res, err := Compile(def, in)
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
		"platform": {[]map[string]any{{"global": M{"proxy": M{"no.proxy": math.NaN()}}}}, nil, `platform layer 1: global.proxy["no.proxy"]: not a finite number: NaN`},
		"instance": {nil, []map[string]any{{}, {"hooks": []any{func() {}}}}, "values layer 2: hooks[0]: a func() cannot be a value"},
	} {
		in := inputs()
		in.Platform, in.Values = tc.platform, tc.instance
		if _, err := Compile(def, in); err == nil || err.Error() != tc.want {
			t.Errorf("%s: got %v, want %s", name, err, tc.want)
		}
	}
}
