package compiler

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
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
		"upstream: {from: a}":        {From: "a"},
	} {
		var spec ChartSpec
		if err := yaml.Unmarshal([]byte(in), &spec); err != nil {
			t.Errorf("%q: %v", in, err)
		} else if !reflect.DeepEqual(spec.Upstream, want) {
			t.Errorf("%q: got %#v, want %#v", in, spec.Upstream, want)
		}
	}
	for _, in := range []string{`upstream: ""`, "upstream: 1", "upstream: {a: b}", `upstream: [a.yaml, ""]`, "upstream: [[a]]",
		"upstream: {}", `upstream: {from: ""}`, "upstream: {from: a, files: [b]}", "upstream: {from: [a]}", "upstream: {from: true}"} {
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
	// A file that cannot be read fails the compile of its chart only: a
	// chart the mapping leaves out does not need it.
	d := *def
	d.Mapping = []byte("output:\n  app: {$if: '${{ values.replicas > 1 }}'}\n")
	d.Service.Charts = map[string]ChartSpec{"app": {Upstream: Upstream{Enabled: true, Files: []string{"missing.yaml"}}}}
	if _, err := Compile(&d, inputs()); err != nil {
		t.Errorf("chart left out: %v", err)
	}
	if _, err := Compile(&d, inputs(M{"replicas": 2})); err == nil || !strings.Contains(err.Error(), "app: upstream file missing.yaml: no instance files") {
		t.Errorf("chart compiled: got %v", err)
	}
}

// TestUpstreamSeenByMapping: the mapping sees the instance's upstream
// values, files and inline, as they will be applied, so that a rule of its
// errors section covers a value written in an upstream file too (the files
// used to be merged after the mapping, out of its sight).
func TestUpstreamSeenByMapping(t *testing.T) {
	d := *def
	d.Mapping = []byte(`
errors:
  - >-
    ${{ values.?upstream.?app.?image.?tag.orValue('') == 'latest'
        ? ['demo: upstream.app.image.tag: latest is refused'] : [] }}
output:
  app: {}
`)
	d.Service.Charts = map[string]ChartSpec{"app": {Base: "vendor-values/app.yaml", Upstream: Upstream{Enabled: true}}}
	for name, tc := range map[string]struct {
		file   string // upstream.app.yaml of the instance, none when empty
		inline M      // upstream.app
		want   string // the error, or the image tag applied
	}{
		"refused inline":      {"", M{"image": M{"tag": "latest"}}, "upstream.app.image.tag: latest is refused"},
		"refused in a file":   {"image: {tag: latest}\n", nil, "upstream.app.image.tag: latest is refused"},
		"inline over a file":  {"image: {tag: latest}\n", M{"image": M{"tag": "5.0"}}, "5.0"},
		"a file over nothing": {"image: {tag: \"5.1\"}\n", nil, "5.1"},
		"a file under inline": {"image: {tag: \"5.1\"}\n", M{"image": M{"tag": "latest"}}, "upstream.app.image.tag: latest is refused"},
	} {
		t.Run(name, func(t *testing.T) {
			in := inputs(M{"upstream": M{"app": tc.inline}})
			if tc.file != "" {
				in.Instance = fstest.MapFS{"upstream.app.yaml": {Data: []byte(tc.file)}}
			}
			res, err := Compile(&d, in)
			if strings.Contains(tc.want, " ") {
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Errorf("got %v, want an error containing %q", err, tc.want)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := dig(res.Values["app"], "image.tag"); got != tc.want {
				t.Errorf("image.tag: got %#v, want %q", got, tc.want)
			}
		})
	}
}

// TestCompileSchemas: the parameters, then the final values of each chart,
// against their schemas; every violation is reported with its path.
func TestCompileSchemas(t *testing.T) {
	schema := `{"$schema": "http://json-schema.org/draft-07/schema#",
		"properties": {"replicaCount": {"type": "integer", "maximum": 3}, "image": {"properties": {"tag": {"type": "string", "pattern": "^[0-9.]+$"}}}}}`
	for name, tc := range map[string]struct {
		files  fstest.MapFS
		values M
		want   string
	}{
		"parameters": {
			nil, M{"replicas": 1.5},
			"values don't meet the specifications of the schema(s) in the following chart(s):\ndemo:\n- at '/replicas': got number, want integer",
		},
		"final values": {
			fstest.MapFS{"vendor/app/values.schema.json": {Data: []byte(schema)}},
			M{"replicas": float64(5), "upstream": M{"app": M{"image": M{"tag": "latest"}}}},
			"compiled values don't meet the specifications of the schema(s) in the following chart(s):\nvendor/app:\n- at '/image/tag': 'latest' does not match pattern '^[0-9.]+$'\n- at '/replicaCount': maximum: got 5, want 3",
		},
		"a $ref to a URL": {
			fstest.MapFS{"vendor/app/values.schema.json": {Data: []byte(`{"$ref": "https://example.org/x.json"}`)}},
			M{}, "the schema may only reference itself",
		},
	} {
		t.Run(name, func(t *testing.T) {
			ch := maps.Clone(chart)
			maps.Copy(ch, tc.files)
			d := *def
			d.Service.Charts = map[string]ChartSpec{"app": {Base: "vendor-values/app.yaml", Upstream: Upstream{Enabled: true}}}
			in := inputs(tc.values)
			in.Chart = ch
			_, err := Compile(&d, in)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
	// A null that deletes a default is checked as Helm checks it: gone.
	ch := maps.Clone(chart)
	ch["vendor/app/values.schema.json"] = &fstest.MapFile{Data: []byte(`{"properties": {"labels": {"type": "object"}}}`)}
	in := inputs(M{"upstream": M{"app": M{"labels": nil}}})
	in.Chart = ch
	d := *def
	d.Service.Charts = map[string]ChartSpec{"app": {Upstream: Upstream{Enabled: true}}}
	res, err := Compile(&d, in)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := res.Values["app"]["labels"]; !ok || v != nil {
		t.Errorf("labels: got %#v, want null (Helm deletes the default)", v)
	}
}

// TestObjects: the objects section (directives included) after the
// descriptor, and the okdp chart: the mapping's own objects, then
// Result.Objects; compiled even when the mapping leaves it out.
func TestObjects(t *testing.T) {
	ch := maps.Clone(chart)
	ch["vendor/okdp/values.yaml"] = &fstest.MapFile{Data: []byte("objects: []\n")}
	mapping := `
output:
  app: {}
  %s
objects:
  - apiVersion: v1
    kind: ConfigMap
    metadata: {name: '${{ okdpFullname("extra") }}', labels: '${{ labels }}'}
  - $if: ${{ values.replicas > 1 }}
    apiVersion: v1
    kind: ConfigMap
    metadata: {name: ha}
  - $spread: "${{ okdpGeneratedSecret({'name': release.name + '-internal', 'keys': [{'key': 'token'}]}) }}"
`
	names := func(objects []any) []string {
		var out []string
		for _, o := range objects {
			out = append(out, str(asMap(o)["kind"])+"/"+str(dig(asMap(o), "metadata.name")))
		}
		return out
	}
	all := []string{"ConfigMap/demo-app-okdp", "ConfigMap/demo-app-extra", "Password/demo-app-internal-token", "ExternalSecret/demo-app-internal"}
	for name, tc := range map[string]struct {
		okdp     string // the okdp entry of output
		declared bool   // service.yaml declares the okdp chart
		want     []string
	}{
		"no okdp chart":              {"", false, nil},
		"okdp chart left out":        {"", true, all},
		"okdp chart left out by $if": {"okdp: {$if: '${{ false }}', objects: [{kind: Secret, metadata: {name: s}}]}", true, all},
		"objects of the mapping first": {
			"okdp: {objects: [{apiVersion: v1, kind: Secret, metadata: {name: s}}]}", true,
			append([]string{"Secret/s"}, all...),
		},
	} {
		t.Run(name, func(t *testing.T) {
			d := *def
			d.Mapping = []byte(fmt.Sprintf(mapping, tc.okdp))
			d.Service.Charts = map[string]ChartSpec{"app": {}}
			if tc.declared {
				d.Service.Charts["okdp"] = ChartSpec{}
			}
			in := inputs()
			in.Chart = ch
			res, err := Compile(&d, in)
			if err != nil {
				t.Fatal(err)
			}
			var objects []any
			for _, o := range res.Objects {
				objects = append(objects, o)
			}
			if got := names(objects); !slices.Equal(got, all) {
				t.Errorf("Result.Objects: got %v, want %v", got, all)
			}
			if lbl := asMap(dig(res.Objects[1], "metadata.labels")); lbl["helm.sh/chart"] != "demo-1.2.3" {
				t.Errorf("labels: %#v", lbl)
			}
			okdp, ok := res.Values["okdp"]
			if !tc.declared {
				if ok {
					t.Errorf("an okdp chart that service.yaml does not declare: %#v", okdp)
				}
				return
			}
			if got := names(asList(okdp["objects"])); !slices.Equal(got, tc.want) {
				t.Errorf("okdp chart objects: got %v, want %v", got, tc.want)
			}
			if got := names(asList(asMap(res.Computed["okdp"])["objects"])); !slices.Equal(got, tc.want) {
				t.Errorf("computed okdp objects: got %v, want %v", got, tc.want)
			}
		})
	}
	for name, tc := range map[string]struct{ mapping, want string }{
		"not a list":      {"objects: {a: 1}\n", "objects: must be a list of objects"},
		"not an object":   {"objects: [{metadata: {name: x}}]\n", "objects[0]: not an object with a kind and a metadata.name"},
		"okdp not a list": {"output: {okdp: {objects: {a: 1}}}\n", "mapping output okdp.objects: must be a list of objects"},
	} {
		t.Run(name, func(t *testing.T) {
			d := *def
			d.Mapping = []byte(tc.mapping)
			d.Service.Charts = map[string]ChartSpec{"okdp": {}}
			in := inputs()
			in.Chart = ch
			if _, err := Compile(&d, in); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

// TestChartAlias: a chart of service.yaml compiled from another vendored
// chart (chart): its defaults and schema, its values keyed by the chart's
// key; Result.Charts maps every chart to the vendored chart rendering it.
func TestChartAlias(t *testing.T) {
	var svc Service
	if err := yaml.Unmarshal([]byte("charts:\n  canary: {chart: app, base: vendor-values/app.yaml}\n"), &svc); err != nil || svc.Charts["canary"].Chart != "app" {
		t.Fatalf("service.yaml: %v %#v", err, svc)
	}
	mapping := "output:\n  app: {}\n  canary: {replicaCount: '${{ values.replicas }}'}\n"
	d := *def
	d.Mapping = []byte(mapping)
	d.Service.Charts = map[string]ChartSpec{"app": {}, "canary": svc.Charts["canary"]}
	res, err := Compile(&d, inputs(M{"replicas": 2}))
	if err != nil {
		t.Fatal(err)
	}
	if want := (map[string]string{"app": "app", "canary": "app"}); !reflect.DeepEqual(res.Charts, want) {
		t.Errorf("charts: got %#v, want %#v", res.Charts, want)
	}
	canary := res.Values["canary"]
	if canary["replicaCount"] != float64(2) || dig(canary, "image.tag") != "4.5" || canary["labels"] == nil {
		t.Errorf("canary: vendor/app's defaults and base values, then the computed ones: %#v", canary)
	}
	// vendor/app's schema, reported under the chart's key.
	ch := maps.Clone(chart)
	ch["vendor/app/values.schema.json"] = &fstest.MapFile{Data: []byte(`{"properties": {"replicaCount": {"maximum": 3}}}`)}
	in := inputs(M{"replicas": 5})
	in.Chart = ch
	if _, err := Compile(&d, in); err == nil || !strings.Contains(err.Error(), "canary (vendor/app):\n- at '/replicaCount': maximum: got 5, want 3") {
		t.Errorf("schema: got %v", err)
	}
	for name, tc := range map[string]struct{ chart, want string }{
		"a path":         {"../app", `service.yaml: charts.canary.chart "../app": not the name of a vendored chart`},
		"a subdirectory": {"app/charts/x", `service.yaml: charts.canary.chart "app/charts/x": not the name`},
		"not vendored":   {"nope", "canary: vendor/nope/values.yaml not found: the chart nope is not vendored"},
	} {
		t.Run(name, func(t *testing.T) {
			d := d
			d.Service.Charts = map[string]ChartSpec{"app": {}, "canary": {Chart: tc.chart}}
			if _, err := Compile(&d, inputs()); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

// TestUpstreamFrom: a chart with upstream.from receives the upstream values
// of the chart it names, files and inline, with that chart's protect and
// append: one set of instance values for two renders of an upstream chart.
func TestUpstreamFrom(t *testing.T) {
	var svc Service
	if err := yaml.Unmarshal([]byte(`
charts:
  app: {upstream: true, protect: [labels], append: [env]}
  canary: {chart: app, upstream: {from: app}}
`), &svc); err != nil {
		t.Fatal(err)
	}
	d := *def
	d.Service = svc
	d.Mapping = []byte(`
output:
  app: {$if: '${{ values.replicas > 1 }}', replicaCount: '${{ values.replicas }}', env: [A]}
  canary: {replicaCount: 1, env: [CANARY]}
`)
	instance := fstest.MapFS{"upstream.app.yaml": {Data: []byte("image: {tag: from-file}\nenv: [F]\n")}}
	in := inputs(M{"replicas": 2, "upstream": M{"app": M{"env": []any{"I"}, "resources": M{"cpu": "1"}}}})
	in.Instance = instance
	res, err := Compile(&d, in)
	if err != nil {
		t.Fatal(err)
	}
	for chart, env := range map[string][]any{"app": {"A", "F", "I"}, "canary": {"CANARY", "F", "I"}} {
		v := res.Values[chart]
		if dig(v, "image.tag") != "from-file" || dig(v, "resources.cpu") != "1" || !reflect.DeepEqual(v["env"], env) {
			t.Errorf("%s: the instance file and inline values of app, env appended: %#v", chart, v)
		}
	}
	if res.Charts["canary"] != "app" || res.Values["canary"]["replicaCount"] != float64(1) {
		t.Errorf("canary: its own computed values, vendor/app: %#v %#v", res.Charts, res.Values["canary"])
	}
	// app's protected paths refuse the value for both renders, also when
	// the mapping leaves app out.
	for name, tc := range map[string]struct {
		replicas int
		want     string
	}{
		"both renders": {2, "app: upstream.app.yaml: labels: labels is set by the platform"},
		"canary alone": {1, "canary: upstream.app.yaml: labels: labels is set by the platform"},
	} {
		t.Run(name, func(t *testing.T) {
			in := inputs(M{"replicas": tc.replicas})
			in.Instance = fstest.MapFS{"upstream.app.yaml": {Data: []byte("labels: {x: y}\n")}}
			if _, err := Compile(&d, in); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
	// The mapping sees app's values once, under upstream.app.
	in = inputs(M{"replicas": 1})
	in.Instance = instance
	d.Mapping = []byte("errors: ['${{ has(values.upstream.canary) || values.upstream.app.image.tag != \"from-file\" ? [\"wrong view\"] : [] }}']\noutput: {canary: {}}\n")
	if _, err := Compile(&d, in); err != nil {
		t.Error(err)
	}
	// What service.yaml may not hold.
	for name, tc := range map[string]struct {
		charts map[string]ChartSpec
		want   string
	}{
		"itself":        {map[string]ChartSpec{"a": {Upstream: Upstream{From: "a"}}}, "charts.a.upstream.from: the chart itself"},
		"unknown":       {map[string]ChartSpec{"a": {Upstream: Upstream{From: "b"}}}, `charts.a.upstream.from "b": not a chart of service.yaml`},
		"no own values": {map[string]ChartSpec{"a": {Upstream: Upstream{From: "b"}}, "b": {}}, `charts.a.upstream.from "b": that chart accepts no upstream values of its own`},
		"a chain":       {map[string]ChartSpec{"a": {Upstream: Upstream{From: "b"}}, "b": {Upstream: Upstream{From: "c"}}, "c": {Upstream: Upstream{Enabled: true}}}, `charts.a.upstream.from "b": that chart accepts no upstream values of its own`},
		"its own rules": {map[string]ChartSpec{"a": {Upstream: Upstream{From: "b"}, Protect: []string{"x"}}, "b": {Upstream: Upstream{Enabled: true}}}, "charts.a: protect and append come with upstream.from, from charts.b"},
	} {
		t.Run(name, func(t *testing.T) {
			d := *def
			d.Service.Charts = tc.charts
			if _, err := Compile(&d, inputs()); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
}
