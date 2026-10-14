package okdp

import (
	"io/fs"
	"maps"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/okdp/okdp-compiler/internal/chart"
)

// TestLoadDefinition: Chart.yaml is required, okdp.yaml and the values
// file are not; Chart.yaml and okdp.yaml's package keys are read strictly
// and checked, the mapping kept as written; the package's other files are
// read by each compile.
func TestLoadDefinition(t *testing.T) {
	const chartYAML = "apiVersion: v2\nname: demo\nversion: 1.0.0\ndependencies: [{name: app, repository: https://x, version: 1.0.0}]\n"
	okdpYAML := `roles: [web]
dependsOn: [ingress]
require: [a, b]
defaults: {app: app.defaults.yaml}
upstream: {app: {protect: [p]}}
vars: {n: 1}
instance: {usage: '${{ n }}'}
`
	ok := fstest.MapFS{
		"Chart.yaml":      {Data: []byte(chartYAML)},
		"okdp.yaml":       {Data: []byte(okdpYAML)},
		"values.cel.yaml": {Data: []byte("app: {}\n")},
	}
	d, err := LoadDefinition(ok)
	if err != nil {
		t.Fatal(err)
	}
	app := chart.Dependency{Name: "app", Repository: "https://x", Version: "1.0.0"}
	if want := (chart.Metadata{APIVersion: "v2", Name: "demo", Version: "1.0.0", Dependencies: []chart.Dependency{app}}); !reflect.DeepEqual(d.Chart, want) {
		t.Errorf("Chart: got %#v", d.Chart)
	}
	p := d.Package
	if !reflect.DeepEqual([][]string{p.Roles, p.DependsOn, p.Require}, [][]string{{"web"}, {"ingress"}, {"a", "b"}}) || !reflect.DeepEqual(p.Defaults, map[string]string{"app": "app.defaults.yaml"}) ||
		!reflect.DeepEqual(p.Upstream, chart.Upstream{Keys: map[string]chart.UpstreamKey{"app": {Accept: true, Protect: []string{"p"}}}}) {
		t.Errorf("Package: got %#v", p)
	}
	if string(d.Okdp) != okdpYAML || string(d.Values) != "app: {}\n" || d.ValuesFile != "values.cel.yaml" || d.Warnings != nil || d.Files == nil {
		t.Errorf("got %#v", d)
	}
	svc, err := d.service()
	want := &Service{Name: "demo", Version: "1.0.0", Roles: []string{"web"}, DependsOn: []string{"ingress"}, Require: []string{"a", "b"}, Charts: chart.Specs{
		"app": {Source: app.Ref(), Defaults: "app.defaults.yaml", Upstream: true, Protect: []string{"p"}},
	}}
	if err != nil || !reflect.DeepEqual(svc, want) {
		t.Errorf("Service: got %#v (%v)", svc, err)
	}
	// Chart.yaml alone: no package key, no mapping.
	if d, err := LoadDefinition(fstest.MapFS{"Chart.yaml": ok["Chart.yaml"]}); err != nil || d.Okdp != nil || d.Values != nil || d.ValuesFile != "" || !reflect.DeepEqual(d.Package, Package{}) {
		t.Errorf("Chart.yaml alone: got %#v (%v)", d, err)
	}
	if d, err := LoadDefinition(with(ok, "okdp.yaml", "require: [a]\n---\nrequire: [b]\n")); err != nil || !reflect.DeepEqual(d.Package.Require, []string{"a"}) {
		t.Errorf("okdp.yaml: a second document is ignored: got %v", err)
	}
	if _, err := LoadDefinition(with(ok, "okdp.yaml", "# nothing\n")); err != nil {
		t.Errorf("an empty okdp.yaml: %v", err)
	}
	dir := maps.Clone(ok)
	dir["values.cel.yaml"] = &fstest.MapFile{Mode: fs.ModeDir}
	for name, tc := range map[string]struct {
		fsys fstest.MapFS
		want string
	}{
		"no Chart.yaml":       {fstest.MapFS{"service.yaml": {Data: []byte("name: demo\n")}, "values.cel.yaml": ok["values.cel.yaml"]}, "open Chart.yaml: file does not exist"},
		"Chart.yaml fields":   {with(ok, "Chart.yaml", chartYAML+"charts: {}\n"), "Chart.yaml: yaml: unmarshal errors:\n  line 5: field charts not found in type chart.Metadata"},
		"Chart.yaml checked":  {with(ok, "Chart.yaml", strings.Replace(chartYAML, "1.0.0\n", "\"1.0\"\n", 1)), `Chart.yaml: version "1.0": SemVer (1.2.3), the version of the umbrella chart`},
		"okdp listed":         {with(ok, "Chart.yaml", strings.Replace(chartYAML, "]\n", ", {name: okdp, repository: oci://x, version: 1.0.0}]\n", 1)), "Chart.yaml: dependencies[1] (okdp): the chart of OKDP's own objects is added by the compiler: remove it"},
		"output":              {with(ok, "okdp.yaml", "vars: {}\noutput: {app: {}}\n"), "okdp.yaml: output: the charts' values are in values.cel.yaml (or values.yaml)"},
		"name":                {with(ok, "okdp.yaml", "name: demo\n"), "okdp.yaml: name: the name, version and charts are Chart.yaml's"},
		"charts":              {with(ok, "okdp.yaml", "require: []\ncharts: {}\n"), "okdp.yaml: charts: the name, version and charts are Chart.yaml's"},
		"unknown fields":      {with(ok, "okdp.yaml", "require: []\nrequires: [a]\nvar: {}\n"), "okdp.yaml: yaml: unmarshal errors:\n  line 2: field requires not found in type okdp.Package\n  line 3: field var not found in type okdp.Package"},
		"not YAML":            {with(ok, "okdp.yaml", "require: ["), "okdp.yaml: yaml: line 1: did not find expected node content"},
		"not a map":           {with(ok, "okdp.yaml", "[require]\n"), "okdp.yaml: yaml: unmarshal errors:\n  line 1: cannot unmarshal !!seq into okdp.Package"},
		"a require entry":     {with(ok, "okdp.yaml", "require: [a, {key: b}]\n"), "okdp.yaml: yaml: unmarshal errors:\n  line 1: cannot unmarshal !!map into string"},
		"defaults":            {with(ok, "okdp.yaml", "defaults: {web: web.defaults.yaml}\n"), "okdp.yaml: defaults.web: not a dependency of Chart.yaml"},
		"upstream key":        {with(ok, "okdp.yaml", "upstream: {web: true}\n"), "okdp.yaml: upstream.web: not a dependency of Chart.yaml"},
		"upstream form":       {with(ok, "okdp.yaml", "upstream: {app: {protcet: [p]}}\n"), "okdp.yaml: upstream.app: true, false or {protect, append}"},
		"upstream not a map":  {with(ok, "okdp.yaml", "upstream: [app]\n"), "okdp.yaml: upstream: true, false or a map by dependency"},
		"upstream path":       {with(ok, "okdp.yaml", "upstream: {app: {protect: [a, 'b[']}}\n"), `okdp.yaml: upstream.app.protect[1] "b[": not a path`},
		"values.cel.yaml dir": {dir, "values.cel.yaml"},
	} {
		if _, err := LoadDefinition(tc.fsys); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want an error containing %q", name, err, tc.want)
		}
	}
}

// TestLoadValuesFile: the values file is values.cel.yaml or values.yaml,
// read as written, its name kept; a values.yaml that holds an expression or
// a $-key, at any depth, gives one warning; a package has one of them.
func TestLoadValuesFile(t *testing.T) {
	const warning = "values.yaml holds expressions or directives (${{ }}, $-keys): name it values.cel.yaml"
	chartOnly := fstest.MapFS{"Chart.yaml": {Data: []byte("apiVersion: v2\nname: demo\nversion: 1.0.0\ndependencies: [{name: app, repository: https://x, version: 1.0.0}]\n")}}
	for name, tc := range map[string]struct {
		file, data string
		warn       bool
	}{
		"plain":           {"values.yaml", "app:\n  image: {tag: \"1.0\"}\n  env: [{name: A, value: $HOME}, {name: B, value: a${b}}]\n  a$b: {}\n", false},
		"expression":      {"values.yaml", "app:\n  env: [{name: A, value: 'x-${{ release.name }}'}]\n", true},
		"$$ key":          {"values.yaml", "app: {config: {$$schema: x}}\n", true},
		"$${{":            {"values.yaml", "app: {text: '$${{ x }}'}\n", true},
		"directive":       {"values.yaml", "app: {$if: true, a: 1}\n", true},
		"several":         {"values.yaml", "$if: true\napp: {a: '${{ 1 }}', $merge: {}}\n", true},
		"not YAML":        {"values.yaml", "app: [\n", false},
		"values.cel.yaml": {"values.cel.yaml", "app: {a: '${{ 1 }}'}\n", false},
	} {
		d, err := LoadDefinition(with(chartOnly, tc.file, tc.data))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		var want []string
		if tc.warn {
			want = []string{warning}
		}
		if d.ValuesFile != tc.file || string(d.Values) != tc.data || !reflect.DeepEqual(d.Warnings, want) {
			t.Errorf("%s: got %q %q, warnings %q", name, d.ValuesFile, d.Values, d.Warnings)
		}
	}
	both := with(with(chartOnly, "values.yaml", "app: {}\n"), "values.cel.yaml", "app: {}\n")
	if _, err := LoadDefinition(both); err == nil || err.Error() != "values.yaml and values.cel.yaml: a package has one of them" {
		t.Errorf("both: got %v", err)
	}
}

// with: fsys with the file name holding data.
func with(fsys fstest.MapFS, name, data string) fstest.MapFS {
	f := maps.Clone(fsys)
	f[name] = &fstest.MapFile{Data: []byte(data)}
	return f
}
