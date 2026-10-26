package okdp

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/okdp/okdp-compiler/internal/chart"
)

// TestRequireYAML: require in okdp.yaml, a list of keys; an entry of
// another form fails with its line.
func TestRequireYAML(t *testing.T) {
	if p, err := decodePackage([]byte("require: [ingress.suffix, oidc.issuerUri]\n")); err != nil || !reflect.DeepEqual(p.Require, []string{"ingress.suffix", "oidc.issuerUri"}) {
		t.Errorf("got %#v (%v)", p.Require, err)
	}
	for src, want := range map[string]string{
		"require: a":          "line 1: cannot unmarshal !!str `a` into []string",
		"require: [a, [b]]":   "line 1: cannot unmarshal !!seq into string",
		"require: [{key: a}]": "line 1: cannot unmarshal !!map into string",
	} {
		if _, err := decodePackage([]byte(src)); err == nil || err.Error() != "okdp.yaml: yaml: unmarshal errors:\n  "+want {
			t.Errorf("%s: got %v, want %q", src, err, want)
		}
	}
}

// TestDefinitionService: the Service of a Definition built in memory, and
// what it refuses before any compile, its messages naming the file: the
// checks of Chart.yaml, okdp.yaml's defaults and upstream.
func TestDefinitionService(t *testing.T) {
	d := *def
	d.Package.Roles, d.Package.DependsOn = []string{"web"}, []string{"ingress"}
	d.Package.Upstream = chart.Upstream{All: true}
	svc, err := d.service()
	want := &Service{Name: "demo", Version: "1.2.3", Roles: []string{"web"}, DependsOn: []string{"ingress"}, Require: []string{"ingress.suffix"}, Charts: chart.Specs{
		"app": {Source: appSource, Defaults: "app.defaults.yaml", Upstream: true},
	}}
	if err != nil || !reflect.DeepEqual(svc, want) {
		t.Errorf("got %#v (%v)", svc, err)
	}
	for name, tc := range map[string]struct {
		set  func(d *Definition)
		want string
	}{
		"no name":    {func(d *Definition) { d.Chart.Name = "" }, "Chart.yaml: name is required"},
		"no version": {func(d *Definition) { d.Chart.Version = "" }, "Chart.yaml: version is required"},
		"a key": {func(d *Definition) {
			d.Chart.Dependencies = []chart.Dependency{{Name: "App", Repository: "https://x", Version: "1.0.0"}}
		}, `Chart.yaml: dependencies[0] (App): key "App": a DNS label`},
		"defaults": {func(d *Definition) { d.Package.Defaults = map[string]string{"web": "web.yaml"} }, "okdp.yaml: defaults.web: not a dependency of Chart.yaml"},
		"upstream": {func(d *Definition) { d.Package.Upstream = upstreamOf("okdp", chart.UpstreamKey{Accept: true}) }, "okdp.yaml: upstream.okdp: not a dependency of Chart.yaml"},
	} {
		d := *def
		tc.set(&d)
		if _, err := d.service(); err == nil || !strings.HasPrefix(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want %q", name, err, tc.want)
		}
		if _, err := Compile(context.Background(), &d, inputs()); err == nil || !strings.HasPrefix(err.Error(), tc.want) {
			t.Errorf("%s: Compile: got %v, want %q", name, err, tc.want)
		}
	}
}

// TestRequire: each key of require must be set in global.okdp and not
// empty, in order; every entry is checked once the context is built (its
// errors first), before the platform charts and the mapping.
func TestRequire(t *testing.T) {
	d := *def
	d.Package.Require = []string{"ingress.suffix", "oidc.issuerUri"}
	missing := "global.okdp.oidc.issuerUri is required. It is a context value: the platform's or a project's (Inputs.Context)"
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
		"before platform charts": {[]map[string]any{{"global": M{"okdp": M{"charts": M{"oidcDcr": M{"version": "1.x"}}}}}}, missing},
		"after the context":      {[]map[string]any{{"global": M{"okdp": M{"ingress": M{"suffix": ""}, "oidc": M{"enabled": "yes"}}}}}, "context values (Inputs.Context) don't meet the specifications of the schema of global.okdp:\n- at '/global/okdp/oidc/enabled': got string, want boolean"},
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
