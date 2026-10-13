package okdp

import (
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/okdp/okdp-compiler/internal/chart"
)

// TestLoadDefinition: both files are required, service.yaml must parse,
// its first document of known fields, name the service and its version,
// and pass its checks; the package's other files are read by each compile.
func TestLoadDefinition(t *testing.T) {
	ok := fstest.MapFS{
		"service.yaml": {Data: []byte(`name: demo
version: 1.0.0
roles: [web]
dependsOn: [ingress]
require: [a, b]
charts: {app: {source: {repository: https://x, chart: app, version: 1.0.0}, defaults: f.yaml, protect: [p]}}
`)},
		"values.cel.yaml": {Data: []byte("output: {}\n")},
	}
	d, err := LoadDefinition(ok)
	if err != nil {
		t.Fatal(err)
	}
	want := Service{
		Name: "demo", Version: "1.0.0", Roles: []string{"web"}, DependsOn: []string{"ingress"},
		Require: []string{"a", "b"}, Charts: chart.Specs{"app": {Source: chart.Ref{Repository: "https://x", Chart: "app", Version: "1.0.0"}, Defaults: "f.yaml", Protect: []string{"p"}}},
	}
	if !reflect.DeepEqual(d.Service, want) || string(d.Mapping) != "output: {}\n" || d.Files == nil {
		t.Errorf("got %#v", d)
	}
	if d, err := LoadDefinition(fstest.MapFS{"service.yaml": {Data: []byte("name: demo\nversion: 1.0.0\n---\nname: other\n")}, "values.cel.yaml": ok["values.cel.yaml"]}); err != nil || d.Service.Name != "demo" {
		t.Errorf("a second document is ignored: got %v", err)
	}
	for name, tc := range map[string]struct {
		fsys fstest.MapFS
		want string
	}{
		"no service.yaml":   {fstest.MapFS{"values.cel.yaml": ok["values.cel.yaml"]}, "service.yaml"},
		"no mapping":        {fstest.MapFS{"service.yaml": ok["service.yaml"]}, "values.cel.yaml"},
		"bad service.yaml":  {fstest.MapFS{"service.yaml": {Data: []byte("charts: [")}, "values.cel.yaml": ok["values.cel.yaml"]}, "service.yaml: "},
		"wrong field types": {fstest.MapFS{"service.yaml": {Data: []byte("charts: [a]\n")}, "values.cel.yaml": ok["values.cel.yaml"]}, "service.yaml: "},
		"unknown fields":    {fstest.MapFS{"service.yaml": {Data: []byte("name: demo\nchrats: {}\ncharts: {app: {protcet: [p]}}\n")}, "values.cel.yaml": ok["values.cel.yaml"]}, "service.yaml: yaml: unmarshal errors:\n  line 2: field chrats not found in type okdp.Service\n  line 3: field protcet not found in type chart.Spec"},
		"an empty file":     {fstest.MapFS{"service.yaml": {Data: []byte("# nothing\n")}, "values.cel.yaml": ok["values.cel.yaml"]}, "service.yaml: name is required"},
		"no name":           {fstest.MapFS{"service.yaml": {Data: []byte("version: 1.0.0\n")}, "values.cel.yaml": ok["values.cel.yaml"]}, "service.yaml: name is required"},
		"no version":        {fstest.MapFS{"service.yaml": {Data: []byte("name: demo\n")}, "values.cel.yaml": ok["values.cel.yaml"]}, "service.yaml: version is required"},
		"a chart check":     {fstest.MapFS{"service.yaml": {Data: []byte("name: demo\nversion: 1.0.0\ncharts: {app: {}}\n")}, "values.cel.yaml": ok["values.cel.yaml"]}, "service.yaml: charts.app.source is required"},
		"a require entry":   {fstest.MapFS{"service.yaml": {Data: []byte("require: [a, {key: b}]\n")}, "values.cel.yaml": ok["values.cel.yaml"]}, "line 1: cannot unmarshal !!map into string"},
	} {
		if _, err := LoadDefinition(tc.fsys); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want an error containing %q", name, err, tc.want)
		}
	}
}
