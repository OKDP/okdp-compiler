package fsio

import (
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/okdp/okdp-compiler/internal/chart"
	"github.com/okdp/okdp-compiler/internal/okdp"
)

// TestLoadDefinition: both files are required, service.yaml must parse,
// name the service and its version, and pass its checks; the package's
// other files are read by each compile.
func TestLoadDefinition(t *testing.T) {
	ok := fstest.MapFS{
		"service.yaml": {Data: []byte(`name: demo
version: 1.0.0
appVersion: 1.10
roles: [web]
dependsOn: [ingress]
require: [a, {key: b, $if: "${{ values.on }}"}]
charts: {app: {source: {repository: https://x, chart: app, version: 1.0.0}, base: f.yaml, protect: [p]}}
`)},
		"mapping.cel.yaml": {Data: []byte("output: {}\n")},
	}
	d, err := LoadDefinition(ok)
	if err != nil {
		t.Fatal(err)
	}
	want := okdp.Service{
		Name: "demo", Version: "1.0.0", AppVersion: "1.10", Roles: []string{"web"}, DependsOn: []string{"ingress"},
		Require: okdp.Requirements{{Key: "a"}, {Key: "b", If: "${{ values.on }}"}}, Charts: chart.Specs{"app": {Source: chart.Ref{Repository: "https://x", Chart: "app", Version: "1.0.0"}, Base: "f.yaml", Protect: []string{"p"}}},
	}
	if !reflect.DeepEqual(d.Service, want) || string(d.Mapping) != "output: {}\n" || d.Files == nil {
		t.Errorf("got %#v", d)
	}
	for name, tc := range map[string]struct {
		fsys fstest.MapFS
		want string
	}{
		"no service.yaml":   {fstest.MapFS{"mapping.cel.yaml": ok["mapping.cel.yaml"]}, "service.yaml"},
		"no mapping":        {fstest.MapFS{"service.yaml": ok["service.yaml"]}, "mapping.cel.yaml"},
		"bad service.yaml":  {fstest.MapFS{"service.yaml": {Data: []byte("charts: [")}, "mapping.cel.yaml": ok["mapping.cel.yaml"]}, "service.yaml: "},
		"wrong field types": {fstest.MapFS{"service.yaml": {Data: []byte("charts: [a]\n")}, "mapping.cel.yaml": ok["mapping.cel.yaml"]}, "service.yaml: "},
		"no name":           {fstest.MapFS{"service.yaml": {Data: []byte("version: 1.0.0\n")}, "mapping.cel.yaml": ok["mapping.cel.yaml"]}, "service.yaml: name is required"},
		"no version":        {fstest.MapFS{"service.yaml": {Data: []byte("name: demo\n")}, "mapping.cel.yaml": ok["mapping.cel.yaml"]}, "service.yaml: version is required"},
		"a chart check":     {fstest.MapFS{"service.yaml": {Data: []byte("name: demo\nversion: 1.0.0\ncharts: {app: {}}\n")}, "mapping.cel.yaml": ok["mapping.cel.yaml"]}, "service.yaml: charts.app.source is required"},
		"a require entry":   {fstest.MapFS{"service.yaml": {Data: []byte("require: [a, {key: b, if: x}]\n")}, "mapping.cel.yaml": ok["mapping.cel.yaml"]}, "service.yaml: require[1]: unknown field if"},
		"a require check":   {fstest.MapFS{"service.yaml": {Data: []byte("name: demo\nversion: 1.0.0\nrequire:\n  - a\n  -\n")}, "mapping.cel.yaml": ok["mapping.cel.yaml"]}, "service.yaml: require[1]: an empty key"},
	} {
		if _, err := LoadDefinition(tc.fsys); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want an error containing %q", name, err, tc.want)
		}
	}
}
