package okdp

import (
	"maps"
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
