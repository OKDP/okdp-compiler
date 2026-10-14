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
	// The platform values every compile gets, the first context layer of
	// inputs, at level 0.
	platformValues = map[string]any{"global": M{"okdp": M{
		"ingress": M{"suffix": "example.org"},
		"charts":  M{"objects": M{"repository": "oci://registry.example.org/okdp", "chart": "okdp-objects", "version": "1.0.0"}},
	}}}
	// The chart cache: the upstream charts' defaults.
	charts = fstest.MapFS{
		"charts.example.org/app-1.0.0/app/values.yaml":                          {Data: []byte("replicaCount: 1\nimage: {tag: latest}\nlabels: {}\n")},
		"registry.example.org/okdp/okdp-objects-1.0.0/okdp-objects/values.yaml": {Data: []byte("objects: []\n")},
	}
	appSource     = chart.Ref{Repository: "https://charts.example.org", Chart: "app", Version: "1.0.0"}
	appDep        = chart.Dependency{Name: "app", Repository: appSource.Repository, Version: appSource.Version}
	objectsSource = chart.Ref{Repository: "oci://registry.example.org/okdp", Chart: "okdp-objects", Version: "1.0.0"}
	contracts     = fstest.MapFS{}
	def           = &Definition{
		Chart:   chart.Metadata{APIVersion: "v2", Name: "demo", Version: "1.2.3", Dependencies: []chart.Dependency{appDep}},
		Package: Package{Require: []string{"ingress.suffix"}, Defaults: map[string]string{"app": "app.defaults.yaml"}},
		Files:   pkg,
		Okdp: []byte(`
vars:
  host: ${{ release.name + "." + global.okdp.ingress.suffix }}
errors:
  - {$if: '${{ values.replicas > 10 }}', $value: at most 10 replicas}
instance:
  url: https://${{ host }}
  usage: demo
`),
		Values: []byte(`
app:
  replicaCount: ${{ values.replicas }}
  labels:
    tier: {$if: "${{ values.replicas > 1 }}", $value: ha}
`),
	}
)

// defService: the Service of def.
var defService = func() *Service {
	svc, err := def.service()
	if err != nil {
		panic(err)
	}
	return svc
}()

// upstreamOf: okdp.yaml's upstream, the entry k of the dependency key only.
func upstreamOf(key string, k chart.UpstreamKey) chart.Upstream {
	return chart.Upstream{Keys: map[string]chart.UpstreamKey{key: k}}
}

// inputs: the in-memory chart cache and contracts, with these values
// layers: the global of each, a context layer at level 0 (Inputs.Context),
// its connections, one at level 2, the instance's own, the rest, an
// instance layer (Inputs.Values).
func inputs(layers ...map[string]any) Inputs {
	in := Inputs{Charts: charts, Contracts: contracts, Context: []ContextLayer{{Values: platformValues}}, Release: "demo-app", Namespace: "demo"}
	for _, l := range layers {
		l = maps.Clone(l)
		if g, ok := l["global"]; ok {
			in.Context = append(in.Context, ContextLayer{Values: map[string]any{"global": g}})
			delete(l, "global")
		}
		if c, ok := l["connections"]; ok {
			in.Context = append(in.Context, ContextLayer{Level: 2, Values: map[string]any{"connections": c}})
			delete(l, "connections")
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
	res, err := Compile(context.Background(), def, inputs(v))
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

// TestValuesYAML: a package whose values file is values.yaml, loaded then
// compiled: the warning of LoadDefinition given with the result, none
// without; the errors of the file under its name.
func TestValuesYAML(t *testing.T) {
	files := maps.Clone(pkg)
	files["Chart.yaml"] = &fstest.MapFile{Data: []byte("apiVersion: v2\nname: demo\nversion: 1.2.3\ndependencies: [{name: app, repository: https://charts.example.org, version: 1.0.0}]\n")}
	files["values.yaml"] = &fstest.MapFile{Data: []byte("app: {replicaCount: '${{ values.replicas }}'}\n")}
	d, err := LoadDefinition(files)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Compile(context.Background(), d, inputs(M{"replicas": 2}))
	if err != nil || res.Values["app"]["replicaCount"] != float64(2) {
		t.Fatalf("got %v (%v)", res, err)
	}
	if want := []string{"values.yaml holds expressions or directives (${{ }}, $-keys): name it values.cel.yaml"}; !slices.Equal(res.Warnings, want) {
		t.Errorf("Result.Warnings: got %q, want %q", res.Warnings, want)
	}
	if res, err := Compile(context.Background(), def, inputs()); err != nil || res.Warnings != nil {
		t.Errorf("no warning: got %q (%v)", res.Warnings, err)
	}
	for name, tc := range map[string]struct{ values, want string }{
		"expression": {"app: {replicaCount: '${{ nope }}'}\n", "values.yaml: app: replicaCount: ${{ nope }}: ERROR: <input>:1:2: undeclared reference to 'nope'"},
		"key":        {"app: {}\ndb: {}\n", "values.yaml: db: not a dependency of Chart.yaml"},
		"okdp":       {"okdp: {}\n", "values.yaml: okdp: the chart of OKDP's own objects is compiled from the objects section: remove it"},
		"alias":      {"app: &a {replicaCount: 1}\ndb: *a\n", "values.yaml: line 2: aliases are not read: bind the value in vars"},
	} {
		d := *d
		d.Values = []byte(tc.values)
		if _, err := Compile(context.Background(), &d, inputs()); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want an error containing %q", name, err, tc.want)
		}
	}
}

// TestCompileBudget: a compile cancelled, bounded by a cost limit; the
// okdp chart required.
func TestCompileBudget(t *testing.T) {
	v := M{"replicas": float64(3)}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Compile(ctx, def, inputs(v)); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: got %v, want context.Canceled", err)
	}
	in := inputs(v)
	in.CostLimit = 2
	if _, err := Compile(context.Background(), def, in); err == nil || !strings.Contains(err.Error(), "exceed its CEL cost limit (2)") {
		t.Errorf("past the cost limit: got %v", err)
	}
	in.CostLimit = 1000
	if _, err := Compile(context.Background(), def, in); err != nil {
		t.Errorf("within the cost limit: %v", err)
	}
	bare := inputs(v)
	bare.Context = []ContextLayer{{Values: map[string]any{"global": M{"okdp": M{"ingress": M{"suffix": "example.org"}}}}}}
	if _, err := Compile(context.Background(), def, bare); err == nil || !strings.Contains(err.Error(), "global.okdp.charts.objects is required") {
		t.Errorf("with the okdp chart: got %v, want global.okdp.charts.objects required", err)
	}
}

// TestCompileErrors: mapping errors, platform values and the names.
func TestCompileErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		in   Inputs
		want string
	}{
		"mapping error":   {inputs(map[string]any{"replicas": 11}), "at most 10 replicas"},
		"platform value":  {inputs(map[string]any{"global": map[string]any{"okdp": map[string]any{"ingress": M{"suffix": ""}}}}), "global.okdp.ingress.suffix is required"},
		"missing release": {Inputs{Charts: charts, Contracts: contracts, Namespace: "demo"}, `Inputs.Release "": a release name`},
		"no namespace":    {Inputs{Charts: charts, Contracts: contracts, Release: "demo-app"}, `Inputs.Namespace "": a namespace name`},
		"release":         {Inputs{Charts: charts, Contracts: contracts, Release: "demo_app", Namespace: "demo"}, `Inputs.Release "demo_app": a release name, lowercase letters, digits and "-", starting and ending with a letter or a digit, 53 characters at most (Helm's limit)`},
		"long release":    {Inputs{Charts: charts, Contracts: contracts, Release: strings.Repeat("a", 54), Namespace: "demo"}, `Inputs.Release "aaaa`},
		"namespace":       {Inputs{Charts: charts, Contracts: contracts, Release: "demo-app", Namespace: "Demo"}, `Inputs.Namespace "Demo": a namespace name, lowercase letters, digits and "-", starting and ending with a letter or a digit, 63 characters at most`},
		"long namespace":  {Inputs{Charts: charts, Contracts: contracts, Release: "demo-app", Namespace: strings.Repeat("a", 64)}, `Inputs.Namespace "aaaa`},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Compile(context.Background(), def, tc.in)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
	// The longest release and namespace.
	in := inputs()
	in.Release, in.Namespace = strings.Repeat("a", 53), strings.Repeat("a", 63)
	if _, err := Compile(context.Background(), def, in); err != nil {
		t.Errorf("53-character release, 63-character namespace: %v", err)
	}
}

// TestServicePaths: Compile refuses a malformed protect path of a
// definition built in memory; a bracketed path protects the instance's
// upstream values.
func TestServicePaths(t *testing.T) {
	d := *def
	d.Values = []byte("app: {}\n")
	d.Package.Upstream = upstreamOf("app", chart.UpstreamKey{Accept: true, Protect: []string{"labels.*"}})
	if _, err := Compile(context.Background(), &d, inputs()); err == nil || !strings.Contains(err.Error(), `okdp.yaml: upstream.app.protect[0] "labels.*"`) {
		t.Errorf("in memory: got %v", err)
	}
	d.Package.Upstream = upstreamOf("app", chart.UpstreamKey{Accept: true, Protect: []string{`labels["okdp.io/*"]`}})
	in := inputs(M{"upstream": M{"app": M{"labels": M{"okdp.io/instance": "other"}}}})
	if _, err := Compile(context.Background(), &d, in); err == nil || !strings.Contains(err.Error(), `app: upstream.app.labels["okdp.io/instance"]: labels["okdp.io/*"] is set by the platform and cannot be changed`) {
		t.Errorf("a protected key: got %v", err)
	}
	in = inputs(M{"upstream": M{"app": M{"labels": M{"team": "a"}}}})
	res, err := Compile(context.Background(), &d, in)
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
	res, err := Compile(context.Background(), withFiles(ch), in)
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
		upstream bool
		want     map[string]any // the keys of the final values checked
	}{
		"disabled": {false, map[string]any{"image": map[string]any{"tag": "4.5"}, "env": nil}},
		"enabled":  {true, map[string]any{"image": map[string]any{"tag": "inline"}, "env": []any{"C"}}},
	} {
		t.Run(name, func(t *testing.T) {
			d := *def
			d.Package.Upstream = upstreamOf("app", chart.UpstreamKey{Accept: tc.upstream, Append: []string{"env"}})
			res, err := Compile(context.Background(), &d, inputs(inline))
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
	d.Package.Upstream = upstreamOf("app", chart.UpstreamKey{Accept: true, Protect: []string{"labels"}})
	if _, err := Compile(context.Background(), &d, inputs(M{"upstream": M{"app": M{"labels": M{"x": "y"}}}})); err == nil || !strings.Contains(err.Error(), "app: upstream.app.labels: labels is set by the platform") {
		t.Errorf("a protected path: got %v", err)
	}
}

// TestUpstreamSeenByMapping: the mapping sees the instance's upstream
// values, so that a rule of its errors section covers them.
func TestUpstreamSeenByMapping(t *testing.T) {
	d := *def
	d.Okdp = []byte(`
errors:
  - $if: ${{ values.?upstream.?app.?image.?tag.orValue('') == 'latest' }}
    $value: 'demo: upstream.app.image.tag: latest is refused'
`)
	d.Values = []byte("app: {}\n")
	d.Package.Upstream = chart.Upstream{All: true}
	for name, tc := range map[string]struct {
		inline M      // upstream.app
		want   string // the error, or the image tag applied
	}{
		"refused":  {M{"image": M{"tag": "latest"}}, "upstream.app.image.tag: latest is refused"},
		"accepted": {M{"image": M{"tag": "5.0"}}, "5.0"},
	} {
		t.Run(name, func(t *testing.T) {
			res, err := Compile(context.Background(), &d, inputs(M{"upstream": M{"app": tc.inline}}))
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
			fstest.MapFS{"charts.example.org/app-1.0.0/app/values.schema.json": {Data: []byte(schema)}},
			M{"replicas": float64(5), "upstream": M{"app": M{"image": M{"tag": "latest"}}}},
			"compiled values don't meet the specifications of the schema(s) in the following chart(s):\napp (app-1.0.0):\n- at '/image/tag': 'latest' does not match pattern '^[0-9.]+$'\n- at '/replicaCount': maximum: got 5, want 3",
		},
		"a $ref to a URL": {
			fstest.MapFS{"charts.example.org/app-1.0.0/app/values.schema.json": {Data: []byte(`{"$ref": "https://example.org/x.json"}`)}},
			M{}, `failing loading "https://example.org/x.json": no URLLoader registered for "https://example.org/x.json"`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			d := *def
			d.Package.Upstream = chart.Upstream{All: true}
			_, err := Compile(context.Background(), &d, withCharts(inputs(tc.values), tc.files))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
	// A null that deletes a default is checked as Helm checks it: gone.
	d := *def
	d.Package.Defaults, d.Package.Upstream = nil, chart.Upstream{All: true}
	in := withCharts(inputs(M{"upstream": M{"app": M{"labels": nil}}}), fstest.MapFS{"charts.example.org/app-1.0.0/app/values.schema.json": {Data: []byte(`{"properties": {"labels": {"type": "object"}}}`)}})
	res, err := Compile(context.Background(), &d, in)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := res.Values["app"]["labels"]; !ok || v != nil {
		t.Errorf("labels: got %#v, want null (Helm deletes the default)", v)
	}
}

// TestRendered: every dependency of Chart.yaml is rendered, with {} as
// computed values when the values file does not write its key, but one
// whose key it writes that is left out ($if, a section left out whole) or
// null; a key of a top-level $merge or $for is written when it gives it.
func TestRendered(t *testing.T) {
	d := *def
	d.Chart.Dependencies = []chart.Dependency{appDep, {Name: "app", Alias: "canary", Repository: appSource.Repository, Version: appSource.Version}}
	d.Package.Upstream = chart.Upstream{All: true}
	in := inputs(M{"replicas": 2, "upstream": M{"app": M{"labels": M{"team": "a"}}}})
	for name, tc := range map[string]struct {
		values string   // values.cel.yaml, none when ""
		want   []string // the charts rendered
	}{
		"not written":      {"canary: {replicaCount: 3}\n", []string{"app", "canary", "okdp"}},
		"no values file":   {"", []string{"app", "canary", "okdp"}},
		"$if false":        {"app: {$if: '${{ values.replicas > 5 }}'}\n", []string{"canary", "okdp"}},
		"null":             {"app: ~\n", []string{"canary", "okdp"}},
		"null expression":  {"app: '${{ null }}'\ncanary: {}\n", []string{"canary", "okdp"}},
		"section left out": {"$if: ${{ values.replicas > 5 }}\napp: {}\n", []string{"canary", "okdp"}},
		"$merge":           {"$merge: \"${{ {'app': null} }}\"\n", []string{"canary", "okdp"}},
		"$for":             {"{$for: \"k in ['canary']\", $key: '${{ k }}', $value: '${{ null }}'}\n", []string{"app", "okdp"}},
	} {
		t.Run(name, func(t *testing.T) {
			d := d
			d.Values = nil
			if tc.values != "" {
				d.Values = []byte(tc.values)
			}
			res, err := Compile(context.Background(), &d, in)
			if err != nil {
				t.Fatal(err)
			}
			if got := slices.Sorted(maps.Keys(res.Values)); !slices.Equal(got, tc.want) {
				t.Errorf("rendered: got %v, want %v", got, tc.want)
			}
			if app, ok := res.Values["app"]; ok && (app["replicaCount"] != float64(1) || value.Dig(app, "image.tag") != "4.5" || value.Dig(app, "labels.team") != "a") {
				t.Errorf("app: its chart's defaults, OKDP's and its upstream values: %#v", app)
			}
		})
	}
	// A key that is not a dependency fails, null or not.
	for _, src := range []string{"x: {}\n", "x: ~\n"} {
		d.Values = []byte(src)
		if _, err := Compile(context.Background(), &d, in); err == nil || err.Error() != "values.cel.yaml: x: not a dependency of Chart.yaml" {
			t.Errorf("%q: got %v", src, err)
		}
	}
}

// TestChartAlone: a package that is Chart.yaml alone compiles, every
// dependency rendered with its chart's defaults.
func TestChartAlone(t *testing.T) {
	d, err := LoadDefinition(fstest.MapFS{"Chart.yaml": {Data: []byte(`
apiVersion: v2
name: demo
version: 1.0.0
dependencies:
  - {name: app, repository: https://charts.example.org, version: 1.0.0}
  - {name: app, alias: canary, repository: https://charts.example.org, version: 1.0.0}
`)}})
	if err != nil {
		t.Fatal(err)
	}
	res, err := Compile(context.Background(), d, inputs())
	if err != nil {
		t.Fatal(err)
	}
	if got := slices.Sorted(maps.Keys(res.Values)); !slices.Equal(got, []string{"app", "canary", "okdp"}) {
		t.Errorf("rendered: got %v", got)
	}
	for _, k := range []string{"app", "canary"} {
		if v := res.Values[k]; v["replicaCount"] != float64(1) || value.Dig(v, "image.tag") != "latest" || !reflect.DeepEqual(v["labels"], M{}) {
			t.Errorf("%s: its chart's defaults: %#v", k, v)
		}
	}
}

// TestCompileWithoutSchema: a package without config.schema.json compiles
// with the empty schema: no defaults, any parameter, connections and the
// upstream values of any key accepted; an instance sets no key of global.
func TestCompileWithoutSchema(t *testing.T) {
	d := *def
	d.Files = fstest.MapFS{"app.defaults.yaml": pkg["app.defaults.yaml"]}
	d.Okdp = nil
	d.Values = []byte(`
app:
  replicaCount: ${{ values.?replicas.orValue('none') }}
  labels: ${{ values.labels }}
`)
	d.Package.Upstream = chart.Upstream{All: true}
	in := inputs(M{"labels": M{"tier": "gold"}, "upstream": M{"app": M{"image": M{"tag": "5.0"}}, "other": M{"x": "y"}}})
	in.Context = append(in.Context, ContextLayer{Values: map[string]any{"connections": M{"ext": M{"contract": "database", "host": "db.example.org"}}}})
	res, err := Compile(context.Background(), &d, in)
	if err != nil {
		t.Fatal(err)
	}
	app := res.Values["app"]
	if app["replicaCount"] != "none" || value.Dig(app, "labels.tier") != "gold" || value.Dig(app, "image.tag") != "5.0" {
		t.Errorf("values: %#v", app)
	}
	in.Values = append(in.Values, map[string]any{"global": M{"imageRegistry": "registry.example.org"}})
	if _, err := Compile(context.Background(), &d, in); err == nil || !strings.Contains(err.Error(), "values layer 2: global.imageRegistry is set by the context (--context, Inputs.Context)") {
		t.Errorf("a key of global: got %v", err)
	}
}

// TestObjects: the objects section (directives included) after the
// descriptor, and the okdp chart the compiler adds to every compile:
// Result.Objects as its values, rendered by the platform's objects chart;
// Chart.yaml and values.cel.yaml may not list it.
func TestObjects(t *testing.T) {
	names := func(objects []any) []string {
		var out []string
		for _, o := range objects {
			out = append(out, value.Str(value.AsMap(o)["kind"])+"/"+value.Str(value.Dig(value.AsMap(o), "metadata.name")))
		}
		return out
	}
	d := *def
	d.Values = []byte("app: {}\n")
	d.Okdp = []byte(`
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
	res, err := Compile(context.Background(), &d, inputs())
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
	// A mapping that renders no dependency: the okdp chart all the same.
	d.Okdp, d.Values = []byte("instance: {url: https://x}\n"), []byte("app: ~\n")
	if res, err = Compile(context.Background(), &d, inputs()); err != nil || !slices.Equal(slices.Sorted(maps.Keys(res.Values)), []string{"okdp"}) {
		t.Errorf("no output: %v %v", err, res)
	}
	okdpDep := chart.Dependency{Name: "okdp-objects", Alias: "okdp", Repository: objectsSource.Repository, Version: objectsSource.Version}
	for name, tc := range map[string]struct {
		okdp, values string // okdp.yaml, values.cel.yaml
		deps         []chart.Dependency
		layer        M
		want         string
	}{
		"not an object": {"objects: [{metadata: {name: x}}]\n", "", nil, nil, "- at '/objects/0': missing property 'kind'"},
		"in values":     {"", "okdp: {}\n", nil, nil, "values.cel.yaml: okdp: the chart of OKDP's own objects is compiled from the objects section: remove it"},
		"okdp null":     {"", "okdp: ~\n", nil, nil, "values.cel.yaml: okdp: the chart of OKDP's own objects is compiled from the objects section: remove it"},
		"in Chart.yaml": {"", "", []chart.Dependency{appDep, okdpDep}, nil, "Chart.yaml: dependencies[1] (okdp): the chart of OKDP's own objects is added by the compiler: remove it"},
		"null charts":   {"", "", nil, M{"global": M{"okdp": M{"charts": nil}}}, "- at '/global/okdp/charts': got null, want object"},
		"a range":       {"", "", nil, M{"global": M{"okdp": M{"charts": M{"objects": M{"version": "1.x"}}}}}, `global.okdp.charts.objects.version "1.x": a pinned chart version`},
		"oidcDcr":       {"", "", nil, M{"global": M{"okdp": M{"charts": M{"oidcDcr": M{"repository": "quay.io"}}}}}, `global.okdp.charts.oidcDcr.repository "quay.io": the URL of a chart repository`},
		"not in cache":  {"", "", nil, M{"global": M{"okdp": M{"charts": M{"objects": M{"version": "2.0.0"}}}}}, "okdp: chart okdp-objects-2.0.0 is not in the chart cache"},
	} {
		t.Run(name, func(t *testing.T) {
			d := *def
			d.Okdp, d.Values = []byte(tc.okdp), []byte(tc.values)
			if tc.deps != nil {
				d.Chart.Dependencies = tc.deps
			}
			if _, err := Compile(context.Background(), &d, inputs(tc.layer)); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

// TestSharedSource: two dependencies of Chart.yaml, one aliased, rendered by
// one upstream chart: its defaults and schema, read once from the chart
// cache, each with its own values; Result.Charts maps every chart to its
// source.
func TestSharedSource(t *testing.T) {
	d := *def
	d.Chart.Dependencies = []chart.Dependency{appDep, {Name: "app", Alias: "canary", Repository: "https://charts.example.org", Version: "1.0.0"}}
	d.Package.Defaults = map[string]string{"canary": "app.defaults.yaml"}
	d.Okdp, d.Values = nil, []byte("app: {}\ncanary: {replicaCount: '${{ values.replicas }}'}\n")
	res, err := Compile(context.Background(), &d, inputs(M{"replicas": 2}))
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
	in := withCharts(inputs(M{"replicas": 5}), fstest.MapFS{"charts.example.org/app-1.0.0/app/values.schema.json": {Data: []byte(`{"properties": {"replicaCount": {"maximum": 3}}}`)}})
	if _, err := Compile(context.Background(), &d, in); err == nil || !strings.Contains(err.Error(), "canary (app-1.0.0):\n- at '/replicaCount': maximum: got 5, want 3") {
		t.Errorf("schema: got %v", err)
	}
}
