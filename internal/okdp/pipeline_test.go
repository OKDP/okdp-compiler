package okdp

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"go.yaml.in/yaml/v3"

	"github.com/okdp/okdp-compiler/internal/chart"
	"github.com/okdp/okdp-compiler/internal/value"
)

// A minimal package, in memory: what a caller without a checkout (a
// server compiling from git) passes.
var (
	pkg = fstest.MapFS{
		"config.schema.json": {Data: []byte(`{"properties": {"replicas": {"type": "integer", "default": 1}}}`)},
		"app.defaults.yaml":  {Data: []byte("image: {tag: \"4.5\"}\n")},
	}
	// The platform values every compile gets, the first platform layer of
	// inputs.
	platformValues = map[string]any{"global": M{"okdp": M{
		"ingress": M{"suffix": "example.org"},
		"charts":  M{"objects": M{"repository": "oci://registry.example.org/okdp", "chart": "okdp-objects", "version": "1.0.0"}},
	}}}
	// The chart cache: the upstream charts' defaults.
	charts = fstest.MapFS{
		"app-1.0.0/values.yaml":          {Data: []byte("replicaCount: 1\nimage: {tag: latest}\nlabels: {}\n")},
		"okdp-objects-1.0.0/values.yaml": {Data: []byte("objects: []\n")},
	}
	appSource     = chart.Ref{Repository: "https://charts.example.org", Chart: "app", Version: "1.0.0"}
	objectsSource = chart.Ref{Repository: "oci://registry.example.org/okdp", Chart: "okdp-objects", Version: "1.0.0"}
	contracts     = fstest.MapFS{}
	def           = &Definition{
		Service: Service{
			Name:    "demo",
			Version: "1.2.3",
			Charts:  chart.Specs{"app": {Source: appSource, Defaults: "app.defaults.yaml"}},
			Require: Requirements{{Key: "ingress.suffix"}},
		},
		Files: pkg,
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

// inputs: the in-memory chart cache and contracts, with these values
// layers: the global of each, a platform layer (Inputs.Platform), the rest,
// an instance layer (Inputs.Values).
func inputs(layers ...map[string]any) Inputs {
	in := Inputs{Charts: charts, Contracts: contracts, Platform: []map[string]any{platformValues}, Release: "demo-app", Namespace: "demo"}
	for _, l := range layers {
		if g, ok := l["global"]; ok {
			in.Platform = append(in.Platform, map[string]any{"global": g})
			l = maps.Clone(l)
			delete(l, "global")
		}
		in.Values = append(in.Values, l)
	}
	return in
}

// withCharts: inputs with these files added to the chart cache.
func withCharts(in Inputs, files fstest.MapFS) Inputs {
	cs := maps.Clone(charts)
	maps.Copy(cs, files)
	in.Charts = cs
	return in
}

// withFiles: def with these package files.
func withFiles(files fstest.MapFS) *Definition {
	d := *def
	d.Files = files
	return &d
}

// TestCompile: final values and descriptor of a compile from memory.
func TestCompile(t *testing.T) {
	v, err := chart.ParseValues([]byte("replicas: 3\n"))
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
		"global": map[string]any{"okdp": map[string]any{
			"ingress": map[string]any{"suffix": "example.org"},
			"charts":  map[string]any{"objects": map[string]any{"repository": "oci://registry.example.org/okdp", "chart": "okdp-objects", "version": "1.0.0"}},
		}},
	}
	if !reflect.DeepEqual(app, want) {
		t.Errorf("values:\n got %#v\nwant %#v", app, want)
	}
	if len(res.Objects) != 1 {
		t.Fatalf("objects: got %d, want the descriptor only", len(res.Objects))
	}
	data := value.AsMap(res.Objects[0]["data"])
	if data["url"] != "https://demo-app.example.org" || data["service"] != "demo" || data["version"] != "1.2.3" {
		t.Errorf("descriptor data: %#v", data)
	}
}

// TestCompileErrors: mapping errors, platform values and missing inputs.
// TestCompileContext: a compile cancelled, bounded by a cost limit, and
// without the okdp chart.
func TestCompileContext(t *testing.T) {
	v := M{"replicas": float64(3)}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := CompileContext(ctx, def, inputs(v)); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: got %v, want context.Canceled", err)
	}
	if _, err := CompileContext(context.Background(), def, inputs(v), WithCostLimit(2)); err == nil || !strings.Contains(err.Error(), "exceed its CEL cost limit (2)") {
		t.Errorf("past the cost limit: got %v", err)
	}
	if _, err := CompileContext(context.Background(), def, inputs(v), WithCostLimit(1000)); err != nil {
		t.Errorf("within the cost limit: %v", err)
	}
	// Without the okdp chart, global.okdp.charts.objects is not required.
	bare := inputs(v)
	bare.Platform = []map[string]any{{"global": M{"okdp": M{"ingress": M{"suffix": "example.org"}}}}}
	if _, err := Compile(def, bare); err == nil || !strings.Contains(err.Error(), "global.okdp.charts.objects is required") {
		t.Errorf("with the okdp chart: got %v, want global.okdp.charts.objects required", err)
	}
	res, err := CompileContext(context.Background(), def, bare, WithoutObjectsChart())
	if err != nil {
		t.Fatal(err)
	}
	if got := slices.Sorted(maps.Keys(res.Charts)); !slices.Equal(got, []string{"app"}) {
		t.Errorf("charts: got %v, want app only", got)
	}
	if _, ok := res.Values[ObjectsChart]; ok {
		t.Errorf("values of %s written", ObjectsChart)
	}
	if len(res.Objects) != 1 || res.Objects[0]["kind"] != "ConfigMap" {
		t.Errorf("objects: got %v, want the descriptor", res.Objects)
	}
}

func TestCompileErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		in   Inputs
		want string
	}{
		"mapping error":   {inputs(map[string]any{"replicas": 11}), "at most 10 replicas"},
		"platform value":  {inputs(map[string]any{"global": map[string]any{"okdp": map[string]any{"ingress": nil}}}), "global.okdp.ingress.suffix is required"},
		"missing release": {Inputs{Charts: charts, Contracts: contracts, Namespace: "demo"}, "Release is required"},
		"no chart cache":  {Inputs{Contracts: contracts, Release: "demo-app", Namespace: "demo"}, "Inputs.Charts is required"},
		"release":         {Inputs{Charts: charts, Contracts: contracts, Release: "demo_app", Namespace: "demo"}, `Inputs.Release "demo_app": a release name, lowercase letters, digits and "-", starting and ending with a letter or a digit, 53 characters at most (Helm's limit)`},
		"long release":    {Inputs{Charts: charts, Contracts: contracts, Release: strings.Repeat("a", 54), Namespace: "demo"}, `Inputs.Release "aaaa`},
		"namespace":       {Inputs{Charts: charts, Contracts: contracts, Release: "demo-app", Namespace: "Demo"}, `Inputs.Namespace "Demo": a namespace name, lowercase letters, digits and "-", starting and ending with a letter or a digit, 63 characters at most`},
		"long namespace":  {Inputs{Charts: charts, Contracts: contracts, Release: "demo-app", Namespace: strings.Repeat("a", 64)}, `Inputs.Namespace "aaaa`},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Compile(def, tc.in)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
	// The longest release and namespace.
	in := inputs()
	in.Release, in.Namespace = strings.Repeat("a", 53), strings.Repeat("a", 63)
	if _, err := Compile(def, in); err != nil {
		t.Errorf("53-character release, 63-character namespace: %v", err)
	}
	// A definition built in memory needs its package files.
	if _, err := Compile(&Definition{Service: def.Service}, inputs()); err == nil || !strings.Contains(err.Error(), "Definition.Files is required") {
		t.Errorf("no files: got %v", err)
	}
}

// TestServicePaths: Compile refuses a malformed protect path of a
// definition built in memory; a bracketed path protects the instance's
// upstream values.
func TestServicePaths(t *testing.T) {
	d := *def
	d.Mapping = []byte("output:\n  app: {}\n")
	d.Service.Charts = chart.Specs{"app": {Source: appSource, Protect: []string{"labels.*"}}}
	if _, err := Compile(&d, inputs()); err == nil || !strings.Contains(err.Error(), `charts.app.protect[0] "labels.*"`) {
		t.Errorf("in memory: got %v", err)
	}
	d.Service.Charts = chart.Specs{"app": {Source: appSource, Upstream: chart.Upstream{Enabled: true}, Protect: []string{`labels["okdp.io/*"]`}}}
	in := inputs(M{"upstream": M{"app": M{"labels": M{"okdp.io/instance": "other"}}}})
	if _, err := Compile(&d, in); err == nil || !strings.Contains(err.Error(), `app: upstream.app.labels["okdp.io/instance"]: labels["okdp.io/*"] is set by the platform and cannot be changed`) {
		t.Errorf("a protected key: got %v", err)
	}
	in = inputs(M{"upstream": M{"app": M{"labels": M{"team": "a"}}}})
	res, err := Compile(&d, in)
	if err != nil || value.Dig(res.Values["app"], "labels.team") != "a" {
		t.Errorf("a key the glob does not name: %v %#v", err, res)
	}
}

// TestDependencies: internal references only, with their parameter path.
func TestDependencies(t *testing.T) {
	ch := maps.Clone(pkg)
	ch["config.schema.json"] = &fstest.MapFile{Data: []byte(`{"properties": {
		"replicas": {"type": "integer", "default": 1},
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
	in.Contracts = fstest.MapFS{"database.schema.json": {Data: []byte(`{
		"x-okdp-contract": "database",
		"x-okdp-internal": {"host": "${ref}-db.${namespace}.svc"},
		"properties": {"host": {"type": "string"}},
		"required": ["host"]}`)}}
	res, err := Compile(withFiles(ch), in)
	if err != nil {
		t.Fatal(err)
	}
	want := []Dependency{{Instance: "demo-db", Contract: "database", Field: "backends[1].database"}}
	if !reflect.DeepEqual(res.Dependencies, want) {
		t.Errorf("got %#v, want %#v", res.Dependencies, want)
	}
}

// TestUpstreamValues: the instance's upstream values of a chart that
// accepts them, over its computed values, checked against protect and
// append; none for a chart that does not.
func TestUpstreamValues(t *testing.T) {
	inline := map[string]any{"upstream": map[string]any{"app": map[string]any{"image": map[string]any{"tag": "inline"}, "env": []any{"C"}}}}
	for name, tc := range map[string]struct {
		upstream chart.Upstream
		want     map[string]any // the keys of the final values checked
	}{
		"disabled": {chart.Upstream{}, map[string]any{"image": map[string]any{"tag": "4.5"}, "env": nil}},
		"enabled":  {chart.Upstream{Enabled: true}, map[string]any{"image": map[string]any{"tag": "inline"}, "env": []any{"C"}}},
	} {
		t.Run(name, func(t *testing.T) {
			d := *def
			d.Service.Charts = chart.Specs{"app": {Source: appSource, Defaults: "app.defaults.yaml", Upstream: tc.upstream, Append: []string{"env"}}}
			res, err := Compile(&d, inputs(inline))
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
	d := *def
	d.Service.Charts = chart.Specs{"app": {Source: appSource, Upstream: chart.Upstream{Enabled: true}, Protect: []string{"labels"}}}
	if _, err := Compile(&d, inputs(M{"upstream": M{"app": M{"labels": M{"x": "y"}}}})); err == nil || !strings.Contains(err.Error(), "app: upstream.app.labels: labels is set by the platform") {
		t.Errorf("a protected path: got %v", err)
	}
}

// TestUpstreamSeenByMapping: the mapping sees the instance's upstream
// values, so that a rule of its errors section covers them.
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
	d.Service.Charts = chart.Specs{"app": {Source: appSource, Defaults: "app.defaults.yaml", Upstream: chart.Upstream{Enabled: true}}}
	for name, tc := range map[string]struct {
		inline M      // upstream.app
		want   string // the error, or the image tag applied
	}{
		"refused":  {M{"image": M{"tag": "latest"}}, "upstream.app.image.tag: latest is refused"},
		"accepted": {M{"image": M{"tag": "5.0"}}, "5.0"},
	} {
		t.Run(name, func(t *testing.T) {
			res, err := Compile(&d, inputs(M{"upstream": M{"app": tc.inline}}))
			if strings.Contains(tc.want, " ") {
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Errorf("got %v, want an error containing %q", err, tc.want)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := value.Dig(res.Values["app"], "image.tag"); got != tc.want {
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
			fstest.MapFS{"app-1.0.0/values.schema.json": {Data: []byte(schema)}},
			M{"replicas": float64(5), "upstream": M{"app": M{"image": M{"tag": "latest"}}}},
			"compiled values don't meet the specifications of the schema(s) in the following chart(s):\napp (app-1.0.0):\n- at '/image/tag': 'latest' does not match pattern '^[0-9.]+$'\n- at '/replicaCount': maximum: got 5, want 3",
		},
		"a $ref to a URL": {
			fstest.MapFS{"app-1.0.0/values.schema.json": {Data: []byte(`{"$ref": "https://example.org/x.json"}`)}},
			M{}, "the schema may only reference itself",
		},
	} {
		t.Run(name, func(t *testing.T) {
			d := *def
			d.Service.Charts = chart.Specs{"app": {Source: appSource, Defaults: "app.defaults.yaml", Upstream: chart.Upstream{Enabled: true}}}
			_, err := Compile(&d, withCharts(inputs(tc.values), tc.files))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
	// A null that deletes a default is checked as Helm checks it: gone.
	d := *def
	d.Service.Charts = chart.Specs{"app": {Source: appSource, Upstream: chart.Upstream{Enabled: true}}}
	in := withCharts(inputs(M{"upstream": M{"app": M{"labels": nil}}}), fstest.MapFS{"app-1.0.0/values.schema.json": {Data: []byte(`{"properties": {"labels": {"type": "object"}}}`)}})
	res, err := Compile(&d, in)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := res.Values["app"]["labels"]; !ok || v != nil {
		t.Errorf("labels: got %#v, want null (Helm deletes the default)", v)
	}
}

// TestObjects: the objects section (directives included) after the
// descriptor, and the okdp chart the compiler adds to every compile:
// Result.Objects as its values, rendered by the platform's objects chart;
// service.yaml and the mapping's output may not list it.
func TestObjects(t *testing.T) {
	names := func(objects []any) []string {
		var out []string
		for _, o := range objects {
			out = append(out, value.Str(value.AsMap(o)["kind"])+"/"+value.Str(value.Dig(value.AsMap(o), "metadata.name")))
		}
		return out
	}
	d := *def
	d.Mapping = []byte(`
output:
  app: {}
objects:
  - apiVersion: v1
    kind: ConfigMap
    metadata: {name: '${{ okdpFullname("extra") }}', labels: '${{ labels }}'}
  - $if: ${{ values.replicas > 1 }}
    apiVersion: v1
    kind: ConfigMap
    metadata: {name: ha}
  - $spread: "${{ okdpGeneratedSecret({'name': release.name + '-internal', 'keys': [{'key': 'token'}]}) }}"
`)
	res, err := Compile(&d, inputs())
	if err != nil {
		t.Fatal(err)
	}
	var objects []any
	for _, o := range res.Objects {
		objects = append(objects, o)
	}
	all := []string{"ConfigMap/demo-app-okdp", "ConfigMap/demo-app-extra", "Password/demo-app-internal-token", "ExternalSecret/demo-app-internal"}
	if got := names(objects); !slices.Equal(got, all) {
		t.Errorf("Result.Objects: got %v, want %v", got, all)
	}
	if lbl := value.AsMap(value.Dig(res.Objects[1], "metadata.labels")); lbl["okdp.io/service"] != "demo" || lbl["app.kubernetes.io/version"] != nil || lbl["helm.sh/chart"] != nil {
		t.Errorf("labels: %#v", lbl)
	}
	if res.Charts["okdp"] != objectsSource {
		t.Errorf("okdp chart: rendered by %#v", res.Charts["okdp"])
	}
	if got := names(value.AsList(res.Values["okdp"]["objects"])); !slices.Equal(got, all) {
		t.Errorf("okdp chart objects: got %v, want %v", got, all)
	}
	// A mapping that outputs no chart: the okdp chart all the same.
	d.Mapping = []byte("instance: {url: https://x}\n")
	if res, err = Compile(&d, inputs()); err != nil || !slices.Equal(slices.Sorted(maps.Keys(res.Values)), []string{"okdp"}) {
		t.Errorf("no output: %v %v", err, res)
	}
	// The descriptor's name in another namespace, or for a kind of another
	// API group: other objects.
	d.Mapping = []byte("objects:\n  - {apiVersion: v1, kind: ConfigMap, metadata: {name: demo-app-okdp, namespace: other}}\n  - {apiVersion: example.org/v1, kind: ConfigMap, metadata: {name: demo-app-okdp}}\n")
	if res, err = Compile(&d, inputs()); err != nil || len(res.Objects) != 3 {
		t.Errorf("other objects: %v %v", err, res)
	}
	for name, tc := range map[string]struct {
		mapping string
		charts  chart.Specs
		values  M
		want    string
	}{
		"not a list":      {"objects: {a: 1}\n", nil, nil, "objects: must be a list of objects"},
		"not an object":   {"objects: [{metadata: {name: x}}]\n", nil, nil, "objects[0]: not an object with a kind and a metadata.name"},
		"the descriptor":  {"objects: [{apiVersion: v1, kind: ConfigMap, metadata: {name: demo-app-okdp}}]\n", nil, nil, `objects[0]: the descriptor is the same object, ConfigMap "demo-app-okdp" in namespace "demo": give each object its own name`},
		"another version": {"objects:\n  - {apiVersion: external-secrets.io/v1beta1, kind: ExternalSecret, metadata: {name: x, namespace: demo}}\n  - {apiVersion: external-secrets.io/v1, kind: ExternalSecret, metadata: {name: x}}\n", nil, nil, `objects[1]: objects[0] is the same object, ExternalSecret.external-secrets.io "x" in namespace "demo"`},
		"in output":       {"output: {okdp: {}}\n", nil, nil, `mapping output "okdp": the chart of OKDP's own objects is compiled from the objects section: remove it from output`},
		"in service.yaml": {"{}\n", chart.Specs{"okdp": {Source: objectsSource}}, nil, "service.yaml: charts.okdp: the chart of OKDP's own objects is added by the compiler: remove it from service.yaml"},
		"no objects chart": {
			"{}\n", nil, M{"global": M{"okdp": M{"charts": nil}}},
			"global.okdp.charts.objects is required: the chart OKDP's own objects are rendered with (okdp-objects: repository, chart, version). It is a platform value: set it in the platform's values (--platform, Inputs.Platform)",
		},
		"not a map":    {"{}\n", nil, M{"global": M{"okdp": M{"charts": M{"objects": "okdp-objects"}}}}, `global.okdp.charts.objects must be a map (repository, chart, version), got "okdp-objects"`},
		"not a string": {"{}\n", nil, M{"global": M{"okdp": M{"charts": M{"objects": M{"version": 1}}}}}, "global.okdp.charts.objects.version must be a string, got 1"},
		"a range":      {"{}\n", nil, M{"global": M{"okdp": M{"charts": M{"objects": M{"version": "1.x"}}}}}, `global.okdp.charts.objects.version "1.x": a pinned chart version`},
		"oidcDcr":      {"{}\n", nil, M{"global": M{"okdp": M{"charts": M{"oidcDcr": M{"repository": "quay.io"}}}}}, `global.okdp.charts.oidcDcr.repository "quay.io": the URL of a chart repository`},
		"unknown key":  {"{}\n", nil, M{"global": M{"okdp": M{"charts": M{"oidcDCR": M{}, "dcr": M{}}}}}, "global.okdp.charts: unknown key(s) dcr, oidcDCR (allowed: objects, oidcDcr)"},
		"not in cache": {"{}\n", nil, M{"global": M{"okdp": M{"charts": M{"objects": M{"version": "2.0.0"}}}}}, "okdp: chart okdp-objects-2.0.0 is not in the chart cache"},
	} {
		t.Run(name, func(t *testing.T) {
			d := *def
			d.Mapping = []byte(tc.mapping)
			if tc.charts != nil {
				d.Service.Charts = tc.charts
			}
			if _, err := Compile(&d, inputs(tc.values)); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

// TestSharedSource: two charts of service.yaml rendered by one upstream
// chart: its defaults and schema, read once from the chart cache, each with
// its own values; Result.Charts maps every chart to its source.
func TestSharedSource(t *testing.T) {
	var svc Service
	if err := yaml.Unmarshal([]byte("charts:\n  canary: {source: {repository: https://charts.example.org, chart: app, version: 1.0.0}, defaults: app.defaults.yaml}\n"), &svc); err != nil || svc.Charts["canary"].Source != appSource {
		t.Fatalf("service.yaml: %v %#v", err, svc)
	}
	mapping := "output:\n  app: {}\n  canary: {replicaCount: '${{ values.replicas }}'}\n"
	d := *def
	d.Mapping = []byte(mapping)
	d.Service.Charts = chart.Specs{"app": {Source: appSource}, "canary": svc.Charts["canary"]}
	res, err := Compile(&d, inputs(M{"replicas": 2}))
	if err != nil {
		t.Fatal(err)
	}
	if want := (map[string]chart.Ref{"app": appSource, "canary": appSource, "okdp": objectsSource}); !reflect.DeepEqual(res.Charts, want) {
		t.Errorf("charts: got %#v, want %#v", res.Charts, want)
	}
	canary := res.Values["canary"]
	if canary["replicaCount"] != float64(2) || value.Dig(canary, "image.tag") != "4.5" || canary["labels"] == nil {
		t.Errorf("canary: app's defaults and OKDP's, then the computed ones: %#v", canary)
	}
	// app's schema, reported under each chart's key.
	in := withCharts(inputs(M{"replicas": 5}), fstest.MapFS{"app-1.0.0/values.schema.json": {Data: []byte(`{"properties": {"replicaCount": {"maximum": 3}}}`)}})
	if _, err := Compile(&d, in); err == nil || !strings.Contains(err.Error(), "canary (app-1.0.0):\n- at '/replicaCount': maximum: got 5, want 3") {
		t.Errorf("schema: got %v", err)
	}
}

// TestUpstreamFrom: a chart with upstream.from receives the upstream values
// of the chart it names, with that chart's protect and append: one set of
// instance values for two renders of an upstream chart.
func TestUpstreamFrom(t *testing.T) {
	var svc Service
	if err := yaml.Unmarshal([]byte(`
name: demo
version: 1.2.3
charts:
  app:
    source: &app {repository: https://charts.example.org, chart: app, version: 1.0.0}
    upstream: true
    protect: [labels]
    append: [env]
  canary: {source: *app, upstream: {from: app}}
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
	in := inputs(M{"replicas": 2, "upstream": M{"app": M{"image": M{"tag": "inline"}, "env": []any{"I"}, "resources": M{"cpu": "1"}}}})
	res, err := Compile(&d, in)
	if err != nil {
		t.Fatal(err)
	}
	for key, env := range map[string][]any{"app": {"A", "I"}, "canary": {"CANARY", "I"}} {
		v := res.Values[key]
		if value.Dig(v, "image.tag") != "inline" || value.Dig(v, "resources.cpu") != "1" || !reflect.DeepEqual(v["env"], env) {
			t.Errorf("%s: the upstream values of app, env appended: %#v", key, v)
		}
	}
	if res.Charts["canary"] != appSource || res.Values["canary"]["replicaCount"] != float64(1) {
		t.Errorf("canary: its own computed values, rendered by app: %#v %#v", res.Charts, res.Values["canary"])
	}
	// app's protected paths refuse the value for both renders, also when
	// the mapping leaves app out.
	for name, tc := range map[string]struct {
		replicas int
		want     string
	}{
		"both renders": {2, "app: upstream.app.labels: labels is set by the platform"},
		"canary alone": {1, "canary: upstream.app.labels: labels is set by the platform"},
	} {
		t.Run(name, func(t *testing.T) {
			in := inputs(M{"replicas": tc.replicas, "upstream": M{"app": M{"labels": M{"x": "y"}}}})
			if _, err := Compile(&d, in); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
	// What service.yaml may not hold.
	for name, tc := range map[string]struct {
		charts chart.Specs
		want   string
	}{
		"itself":        {chart.Specs{"a": {Source: appSource, Upstream: chart.Upstream{From: "a"}}}, "charts.a.upstream.from: the chart itself"},
		"unknown":       {chart.Specs{"a": {Source: appSource, Upstream: chart.Upstream{From: "b"}}}, `charts.a.upstream.from "b": not a chart of service.yaml`},
		"no own values": {chart.Specs{"a": {Source: appSource, Upstream: chart.Upstream{From: "b"}}, "b": {Source: appSource}}, `charts.a.upstream.from "b": that chart accepts no upstream values of its own`},
		"a chain":       {chart.Specs{"a": {Source: appSource, Upstream: chart.Upstream{From: "b"}}, "b": {Source: appSource, Upstream: chart.Upstream{From: "c"}}, "c": {Source: appSource, Upstream: chart.Upstream{Enabled: true}}}, `charts.a.upstream.from "b": that chart accepts no upstream values of its own`},
		"its own rules": {chart.Specs{"a": {Source: appSource, Upstream: chart.Upstream{From: "b"}, Protect: []string{"x"}}, "b": {Source: appSource, Upstream: chart.Upstream{Enabled: true}}}, "charts.a: protect and append come with upstream.from, from charts.b"},
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
