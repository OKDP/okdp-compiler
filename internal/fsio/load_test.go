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
// as one document of known fields, name the service and its version, and
// pass its checks; the package's other files are read by each compile.
func TestLoadDefinition(t *testing.T) {
	ok := fstest.MapFS{
		"service.yaml": {Data: []byte(`name: demo
version: 1.0.0
roles: [web]
dependsOn: [ingress]
require: [a, {key: b, $if: "${{ values.on }}"}]
charts: {app: {source: {repository: https://x, chart: app, version: 1.0.0}, defaults: f.yaml, protect: [p]}}
`)},
		"values.cel.yaml": {Data: []byte("output: {}\n")},
	}
	d, err := LoadDefinition(ok)
	if err != nil {
		t.Fatal(err)
	}
	want := okdp.Service{
		Name: "demo", Version: "1.0.0", Roles: []string{"web"}, DependsOn: []string{"ingress"},
		Require: okdp.Requirements{{Key: "a"}, {Key: "b", If: "${{ values.on }}"}}, Charts: chart.Specs{"app": {Source: chart.Ref{Repository: "https://x", Chart: "app", Version: "1.0.0"}, Defaults: "f.yaml", Protect: []string{"p"}}},
	}
	if !reflect.DeepEqual(d.Service, want) || string(d.Mapping) != "output: {}\n" || d.Files == nil {
		t.Errorf("got %#v", d)
	}
	for name, tc := range map[string]struct {
		fsys fstest.MapFS
		want string
	}{
		"no service.yaml":   {fstest.MapFS{"values.cel.yaml": ok["values.cel.yaml"]}, "service.yaml"},
		"no mapping":        {fstest.MapFS{"service.yaml": ok["service.yaml"]}, "values.cel.yaml"},
		"bad service.yaml":  {fstest.MapFS{"service.yaml": {Data: []byte("charts: [")}, "values.cel.yaml": ok["values.cel.yaml"]}, "service.yaml: "},
		"wrong field types": {fstest.MapFS{"service.yaml": {Data: []byte("charts: [a]\n")}, "values.cel.yaml": ok["values.cel.yaml"]}, "service.yaml: "},
		"unknown fields":    {fstest.MapFS{"service.yaml": {Data: []byte("name: demo\nchrats: {}\ncharts: {app: {protcet: [p]}}\n")}, "values.cel.yaml": ok["values.cel.yaml"]}, "service.yaml: line 2: unknown field chrats (allowed: name, version, roles, dependsOn, charts, require)\nservice.yaml: line 3: unknown field protcet (allowed: source, defaults, protect, append, upstream)"},
		"two documents":     {fstest.MapFS{"service.yaml": {Data: []byte("name: demo\nversion: 1.0.0\n---\nname: other\n")}, "values.cel.yaml": ok["values.cel.yaml"]}, "service.yaml: line 3: a second YAML document: service.yaml is one document"},
		"an empty file":     {fstest.MapFS{"service.yaml": {Data: []byte("# nothing\n")}, "values.cel.yaml": ok["values.cel.yaml"]}, "service.yaml: name is required"},
		"no name":           {fstest.MapFS{"service.yaml": {Data: []byte("version: 1.0.0\n")}, "values.cel.yaml": ok["values.cel.yaml"]}, "service.yaml: name is required"},
		"no version":        {fstest.MapFS{"service.yaml": {Data: []byte("name: demo\n")}, "values.cel.yaml": ok["values.cel.yaml"]}, "service.yaml: version is required"},
		"a chart check":     {fstest.MapFS{"service.yaml": {Data: []byte("name: demo\nversion: 1.0.0\ncharts: {app: {}}\n")}, "values.cel.yaml": ok["values.cel.yaml"]}, "service.yaml: charts.app.source is required"},
		"a require entry":   {fstest.MapFS{"service.yaml": {Data: []byte("require: [a, {key: b, if: x}]\n")}, "values.cel.yaml": ok["values.cel.yaml"]}, "service.yaml: require[1]: unknown field if"},
		"a require check":   {fstest.MapFS{"service.yaml": {Data: []byte("name: demo\nversion: 1.0.0\nrequire:\n  - a\n  -\n")}, "values.cel.yaml": ok["values.cel.yaml"]}, "service.yaml: require[1]: an empty key"},
	} {
		if _, err := LoadDefinition(tc.fsys); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want an error containing %q", name, err, tc.want)
		}
	}
}
