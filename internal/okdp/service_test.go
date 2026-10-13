package okdp

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// TestRequireYAML: require in service.yaml, a list of keys; an entry of
// another form fails with its line.
func TestRequireYAML(t *testing.T) {
	var svc Service
	if err := yaml.Unmarshal([]byte("require: [ingress.suffix, oidc.issuerUri]\n"), &svc); err != nil || !reflect.DeepEqual(svc.Require, []string{"ingress.suffix", "oidc.issuerUri"}) {
		t.Errorf("got %#v (%v)", svc.Require, err)
	}
	for src, want := range map[string]string{
		"require: a":          "line 1: cannot unmarshal !!str `a` into []string",
		"require: [a, [b]]":   "line 1: cannot unmarshal !!seq into string",
		"require: [{key: a}]": "line 1: cannot unmarshal !!map into string",
	} {
		var svc Service
		if err := yaml.Unmarshal([]byte(src), &svc); err == nil || err.Error() != "yaml: unmarshal errors:\n  "+want {
			t.Errorf("%s: got %v, want %q", src, err, want)
		}
	}
}

// TestRequire: each key of require must be set in global.okdp and not
// empty, in order; every entry is checked once the context is built (its
// errors first), before the platform charts and the mapping.
func TestRequire(t *testing.T) {
	d := *def
	d.Service.Require = []string{"ingress.suffix", "oidc.issuerUri"}
	missing := "global.okdp.oidc.issuerUri is required. It is a context value: set it with --context (Inputs.Context)"
	issuer := M{"global": M{"okdp": M{"oidc": M{"issuerUri": "https://sso.example.org"}}}}
	for name, tc := range map[string]struct {
		layers []map[string]any
		want   string // "": no error
	}{
		"missing":                {nil, missing},
		"set":                    {[]map[string]any{issuer}, ""},
		"empty":                  {[]map[string]any{{"global": M{"okdp": M{"oidc": M{"issuerUri": ""}}}}}, missing},
		"in order":               {[]map[string]any{{"global": M{"okdp": M{"ingress": M{"suffix": ""}}}}}, "global.okdp.ingress.suffix is required"},
		"before the mapping":     {[]map[string]any{{"replicas": 11}}, missing},
		"before platform charts": {[]map[string]any{{"global": M{"okdp": M{"charts": M{"objects": M{"version": "1.x"}}}}}}, missing},
		"after the context":      {[]map[string]any{{"global": M{"okdp": M{"ingress": M{"suffix": ""}, "oidc": M{"enabled": "yes"}}}}}, "context values (--context, Inputs.Context) don't meet the specifications of the schema of global.okdp:\n- at '/global/okdp/oidc/enabled': got string, want boolean"},
	} {
		_, err := Compile(context.Background(), &d, inputs(tc.layers...))
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: %v", name, err)
		case tc.want != "" && (err == nil || !strings.HasPrefix(err.Error(), tc.want)):
			t.Errorf("%s: got %v, want %q", name, err, tc.want)
		}
	}
}
