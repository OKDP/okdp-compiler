package compiler

import (
	"maps"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"gopkg.in/yaml.v3"
)

// A minimal wrapper chart and service, in memory: what a caller without a
// checkout (the control plane server) passes.
var (
	chart = fstest.MapFS{
		"Chart.yaml":             {Data: []byte("name: demo\nversion: 1.2.3\nappVersion: \"4.5\"\n")},
		"values.yaml":            {Data: []byte("replicas: 1\nglobal:\n  okdp:\n    ingress:\n      suffix: example.org\n")},
		"values.schema.json":     {Data: []byte(`{"properties": {"replicas": {"type": "integer"}}}`)},
		"vendor/app/values.yaml": {Data: []byte("replicaCount: 1\nimage: {tag: latest}\nlabels: {}\n")},
		"vendor-values/app.yaml": {Data: []byte("image: {tag: \"4.5\"}\n")},
	}
	contracts = fstest.MapFS{}
	def       = &Definition{
		Service: Service{
			Charts:  map[string]ChartSpec{"app": {Base: "vendor-values/app.yaml"}},
			Require: []string{"ingress.suffix"},
		},
		Mapping: []byte(`
let:
  host: ${{ release.name + "." + global.okdp.ingress.suffix }}
errors:
  - '${{ values.replicas > 10 ? ["at most 10 replicas"] : [] }}'
output:
  app:
    replicaCount: ${{ values.replicas }}
    labels:
      tier: {$if: "${{ values.replicas > 1 }}", $value: ha}
instance:
  url: https://${{ host }}
  usage: demo
`),
	}
)

// inputs: the in-memory chart and contracts, with these values layers.
func inputs(values ...map[string]any) Inputs {
	return Inputs{Chart: chart, Contracts: contracts, Values: values, Release: "demo-app", Namespace: "demo"}
}

// TestCompile: final values and descriptor of a compile from memory.
func TestCompile(t *testing.T) {
	v, err := ParseValues([]byte("replicas: 3\n"))
	if err != nil {
		t.Fatal(err)
	}
	res, err := Compile(def, inputs(v))
	if err != nil {
		t.Fatal(err)
	}
	app := res.Values["app"]
	want := map[string]any{
		"replicaCount": float64(3),
		"image":        map[string]any{"tag": "4.5"},
		"labels":       map[string]any{"tier": "ha"},
		"global":       map[string]any{"okdp": map[string]any{"ingress": map[string]any{"suffix": "example.org"}}},
	}
	if !reflect.DeepEqual(app, want) {
		t.Errorf("values:\n got %#v\nwant %#v", app, want)
	}
	if len(res.Objects) != 1 {
		t.Fatalf("objects: got %d, want the descriptor only", len(res.Objects))
	}
	data := asMap(res.Objects[0]["data"])
	if data["url"] != "https://demo-app.example.org" || data["service"] != "demo" || data["version"] != "1.2.3" {
		t.Errorf("descriptor data: %#v", data)
	}
}

// TestCompileErrors: mapping errors, platform values and missing inputs.
func TestCompileErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		in   Inputs
		want string
	}{
		"mapping error":   {inputs(map[string]any{"replicas": 11}), "at most 10 replicas"},
		"platform value":  {inputs(map[string]any{"global": map[string]any{"okdp": map[string]any{"ingress": nil}}}), "global.okdp.ingress.suffix is required"},
		"missing release": {Inputs{Chart: chart, Contracts: contracts, Namespace: "demo"}, "Release is required"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Compile(def, tc.in)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

// TestDependencies: internal references only, with their parameter path.
func TestDependencies(t *testing.T) {
	ch := fstest.MapFS{}
	maps.Copy(ch, chart)
	ch["values.schema.json"] = &fstest.MapFile{Data: []byte(`{"properties": {
		"replicas": {"type": "integer"},
		"backends": {"type": "array", "items": {"properties": {
			"database": {"type": "string", "x-okdp-connection-ref": {"contract": "database"}}}}}}}`)}
	in := inputs(map[string]any{
		"backends": []any{
			map[string]any{"database": "ext"},     // an external connection: not a dependency
			map[string]any{"database": "demo-db"}, // an instance
			map[string]any{"database": ""},        // not set
		},
		"connections": map[string]any{"ext": map[string]any{"contract": "database", "host": "db.example.org"}},
	})
	in.Chart = ch
	in.Contracts = fstest.MapFS{"database.schema.json": {Data: []byte(`{
		"x-okdp-contract": "database",
		"x-okdp-internal": {"host": "${ref}-db.${namespace}.svc"},
		"properties": {"host": {"type": "string"}},
		"required": ["host"]}`)}}
	res, err := Compile(def, in)
	if err != nil {
		t.Fatal(err)
	}
	want := []Dependency{{Instance: "demo-db", Contract: "database", Field: "backends[1].database"}}
	if !reflect.DeepEqual(res.Dependencies, want) {
		t.Errorf("got %#v, want %#v", res.Dependencies, want)
	}
}

// TestUpstreamYAML: upstream: in service.yaml, true or false, a path or a
// list of paths.
func TestUpstreamYAML(t *testing.T) {
	for in, want := range map[string]Upstream{
		"":                           {},
		"upstream: false":            {},
		"upstream: true":             {Enabled: true},
		"upstream: a.yaml":           {Enabled: true, Files: []string{"a.yaml"}},
		"upstream: [a.yaml, b.yaml]": {Enabled: true, Files: []string{"a.yaml", "b.yaml"}},
		"upstream: []":               {Enabled: true, Files: []string{}},
	} {
		var spec ChartSpec
		if err := yaml.Unmarshal([]byte(in), &spec); err != nil {
			t.Errorf("%q: %v", in, err)
		} else if !reflect.DeepEqual(spec.Upstream, want) {
			t.Errorf("%q: got %#v, want %#v", in, spec.Upstream, want)
		}
	}
	for _, in := range []string{`upstream: ""`, "upstream: 1", "upstream: {a: b}", `upstream: [a.yaml, ""]`, "upstream: [[a]]"} {
		var spec ChartSpec
		if err := yaml.Unmarshal([]byte(in), &spec); err == nil {
			t.Errorf("%q: no error", in)
		}
	}
}

// TestUpstreamFiles: the instance's upstream values files, in order, then
// its inline upstream values, each checked against protect and append.
func TestUpstreamFiles(t *testing.T) {
	instance := fstest.MapFS{
		"upstream.app.yaml": {Data: []byte("image: {tag: default-file}\nenv: [A]\n")},
		"a.yaml":            {Data: []byte("image: {tag: a}\nenv: [A]\nextra: a\n")},
		"b.yaml":            {Data: []byte("image: {tag: b}\nenv: [B]\n")},
		"protected.yaml":    {Data: []byte("labels: {x: y}\n")},
	}
	inline := map[string]any{"upstream": map[string]any{"app": map[string]any{"env": []any{"C"}}}}
	for name, tc := range map[string]struct {
		upstream Upstream
		instance fstest.MapFS
		want     map[string]any // the keys of the final values checked
	}{
		"disabled":          {Upstream{}, instance, map[string]any{"image": map[string]any{"tag": "4.5"}, "env": nil}},
		"default file":      {Upstream{Enabled: true}, instance, map[string]any{"image": map[string]any{"tag": "default-file"}, "env": []any{"A", "C"}}},
		"no default file":   {Upstream{Enabled: true}, fstest.MapFS{}, map[string]any{"image": map[string]any{"tag": "4.5"}, "env": []any{"C"}}},
		"no instance files": {Upstream{Enabled: true}, nil, map[string]any{"image": map[string]any{"tag": "4.5"}, "env": []any{"C"}}},
		"one path":          {Upstream{Enabled: true, Files: []string{"b.yaml"}}, instance, map[string]any{"image": map[string]any{"tag": "b"}, "env": []any{"B", "C"}}},
		"paths in order":    {Upstream{Enabled: true, Files: []string{"a.yaml", "b.yaml"}}, instance, map[string]any{"image": map[string]any{"tag": "b"}, "env": []any{"A", "B", "C"}, "extra": "a"}},
	} {
		t.Run(name, func(t *testing.T) {
			d := *def
			d.Service.Charts = map[string]ChartSpec{"app": {Base: "vendor-values/app.yaml", Upstream: tc.upstream, Append: []string{"env"}}}
			in := inputs(inline)
			if tc.instance != nil {
				in.Instance = tc.instance
			}
			res, err := Compile(&d, in)
			if err != nil {
				t.Fatal(err)
			}
			for k, v := range tc.want {
				if got := res.Values["app"][k]; !reflect.DeepEqual(got, v) {
					t.Errorf("%s: got %#v, want %#v", k, got, v)
				}
			}
		})
	}
	for name, tc := range map[string]struct {
		files    []string
		instance fstest.MapFS
		want     string
	}{
		"missing path":      {[]string{"missing.yaml"}, instance, "app: open missing.yaml"},
		"no instance files": {[]string{"a.yaml"}, nil, "upstream file a.yaml: no instance files"},
		"protected path":    {[]string{"protected.yaml"}, instance, "protected.yaml: labels: labels is set by the platform"},
	} {
		t.Run(name, func(t *testing.T) {
			d := *def
			d.Service.Charts = map[string]ChartSpec{"app": {Upstream: Upstream{Enabled: true, Files: tc.files}, Protect: []string{"labels"}}}
			in := inputs()
			if tc.instance != nil {
				in.Instance = tc.instance
			}
			if _, err := Compile(&d, in); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
}
