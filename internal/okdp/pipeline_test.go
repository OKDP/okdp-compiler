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
	"time"

	"github.com/okdp/okdp-compiler/chart"
	"github.com/okdp/okdp-compiler/internal/rules"
	"github.com/okdp/okdp-compiler/value"
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
	platformValues = map[string]any{"global": M{"okdp": M{"cluster": M{"domain": "example.org"}}}}
	appSource      = chart.Ref{Repository: "https://charts.example.org", Chart: "app", Version: "1.0.0"}
	appDep         = chart.Dependency{Name: "app", Repository: appSource.Repository, Version: appSource.Version}
	contracts      = fstest.MapFS{}
	def            = &Definition{
		Chart:   chart.Metadata{APIVersion: "v2", Name: "demo", Version: "1.2.3", Dependencies: []chart.Dependency{appDep}},
		Package: Package{Require: []string{"cluster.domain"}, Defaults: map[string]string{"app": "app.defaults.yaml"}},
		Files:   pkg,
		Okdp: []byte(`
vars:
  host: ${{ release.name + "." + global.okdp.cluster.domain }}
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

// inputs: the in-memory contracts, with these values
// layers: the global of each, a context layer at level 0 (Inputs.Context),
// its connections, one at level 2, the instance's own, its claims, one at
// level 1, the project's, the rest, an instance layer (Inputs.Values).
func inputs(layers ...map[string]any) Inputs {
	in := Inputs{Rules: Mandatory(), Contracts: contracts, Context: []ContextLayer{{Values: platformValues}}, Release: "demo-app", Namespace: "demo"}
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
		if c, ok := l["claims"]; ok {
			in.Context = append(in.Context, ContextLayer{Level: 1, Values: map[string]any{"claims": c}})
			delete(l, "claims")
		}
		in.Values = append(in.Values, l)
	}
	return in
}

// withFiles: def with these package files.
func withFiles(files fstest.MapFS) *Definition {
	d := *def
	d.Files = files
	return &d
}

// TestCompile: final values and what the instance publishes, a compile
// from memory.
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
		"global":       map[string]any{"okdp": map[string]any{"cluster": map[string]any{"domain": "example.org"}}},
	}
	if !reflect.DeepEqual(app, want) {
		t.Errorf("values:\n got %#v\nwant %#v", app, want)
	}
	if res.Objects != nil {
		t.Errorf("objects: got %d, want none", len(res.Objects))
	}
	if want := (Info{URL: "https://demo-app.example.org", Usage: "demo\n"}); !reflect.DeepEqual(res.Info, want) {
		t.Errorf("instance: got %#v, want %#v", res.Info, want)
	}
}

// TestValuesYAML: a package whose values file is values.yaml, loaded then
// compiled: the warning of LoadDefinition given with the result, none
// without; the errors of the file under its name.
func TestValuesYAML(t *testing.T) {
	files := maps.Clone(pkg)
	files["Chart.yaml"] = &fstest.MapFile{Data: []byte("apiVersion: v2\nname: demo\nversion: 1.2.3\ndependencies: [{name: app, repository: https://charts.example.org, version: 1.0.0}]\n")}
	files["values.yaml"] = &fstest.MapFile{Data: []byte("app: {replicaCount: '${{ values.replicas }}'}\n")}
	d, err := LoadDefinition(files, nil)
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
		"alias":      {"app: &a {replicaCount: 1}\ndb: *a\n", "values.yaml: line 2: aliases are not read"},
	} {
		d := *d
		d.Values = []byte(tc.values)
		if _, err := Compile(context.Background(), &d, inputs()); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want an error containing %q", name, err, tc.want)
		}
	}
}

// TestCompileBudget: a compile cancelled, bounded by a cost limit.
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
}

// TestCompileErrors: mapping errors, platform values and the names.
func TestCompileErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		in   Inputs
		want string
	}{
		"mapping error":   {inputs(map[string]any{"replicas": 11}), "at most 10 replicas"},
		"platform value":  {inputs(map[string]any{"global": map[string]any{"okdp": map[string]any{"cluster": M{"domain": ""}}}}), "global.okdp.cluster.domain is required"},
		"missing release": {Inputs{Rules: Mandatory(), Contracts: contracts, Namespace: "demo"}, `Inputs.Release "": a release name`},
		"no namespace":    {Inputs{Rules: Mandatory(), Contracts: contracts, Release: "demo-app"}, `Inputs.Namespace "": a namespace name`},
		"release":         {Inputs{Rules: Mandatory(), Contracts: contracts, Release: "demo_app", Namespace: "demo"}, `Inputs.Release "demo_app": a release name, lowercase letters, digits and "-", starting and ending with a letter or a digit, 53 characters at most (Helm's limit)`},
		"long release":    {Inputs{Rules: Mandatory(), Contracts: contracts, Release: strings.Repeat("a", 54), Namespace: "demo"}, `Inputs.Release "aaaa`},
		"namespace":       {Inputs{Rules: Mandatory(), Contracts: contracts, Release: "demo-app", Namespace: "Demo"}, `Inputs.Namespace "Demo": a namespace name, lowercase letters, digits and "-", starting and ending with a letter or a digit, 63 characters at most`},
		"long namespace":  {Inputs{Rules: Mandatory(), Contracts: contracts, Release: "demo-app", Namespace: strings.Repeat("a", 64)}, `Inputs.Namespace "aaaa`},
		"platform":        {Inputs{Rules: Mandatory(), Contracts: contracts, Release: "demo-app", Namespace: "platform"}, `Inputs.Namespace "platform": the namespace is reserved: platform/ holds the platform's keys of the secret store, which no project writes`},
		"kube-system":     {Inputs{Rules: Mandatory(), Contracts: contracts, Release: "demo-app", Namespace: "kube-system"}, `Inputs.Namespace "kube-system": the namespace is reserved: kube-* is Kubernetes' own (kube-system, kube-public, kube-node-lease)`},
		"kube-node-lease": {Inputs{Rules: Mandatory(), Contracts: contracts, Release: "demo-app", Namespace: "kube-node-lease"}, `Inputs.Namespace "kube-node-lease": the namespace is reserved: kube-* is Kubernetes' own`},
		"flux-system":     {Inputs{Rules: Mandatory(), Contracts: contracts, Release: "demo-app", Namespace: "flux-system"}, `Inputs.Namespace "flux-system": the namespace is reserved: a deployer engine's, or the namespace its target writes in (argocd, flux-system, okdp-releases): a project's releases there would drive the engine`},
		"argocd":          {Inputs{Rules: Mandatory(), Contracts: contracts, Release: "demo-app", Namespace: "argocd"}, `Inputs.Namespace "argocd": the namespace is reserved: a deployer engine's`},
		"okdp-releases":   {Inputs{Rules: Mandatory(), Contracts: contracts, Release: "demo-app", Namespace: "okdp-releases"}, `Inputs.Namespace "okdp-releases": the namespace is reserved: a deployer engine's`},
		"name":            {Inputs{Rules: Mandatory(), Contracts: contracts, Release: "demo-app", Namespace: "demo", Name: "App"}, `Inputs.Name "App": an instance name, lowercase letters, digits and "-", starting and ending with a letter or a digit, 63 characters at most`},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Compile(context.Background(), def, tc.in)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
	// The platform's reserved namespaces (policy.reserved), at level 0:
	// a reservation wins over a privilege; another namespace, default
	// among them, compiles.
	for _, privileged := range []bool{false, true} {
		policy := M{"reserved": []any{"openbao", "demo"}}
		want := `Inputs.Namespace "demo": the namespace is reserved by the platform (global.okdp.policy.reserved): no project has it`
		if privileged {
			policy["privileged"] = []any{"demo"}
			want += ", privileged or not: global.okdp.policy.privileged lists it too, and the reservation wins"
		}
		in := inputs(M{"global": M{"okdp": M{"policy": policy}}})
		if _, err := Compile(context.Background(), def, in); err == nil || err.Error() != want {
			t.Errorf("reserved, privileged %v: got %v, want %s", privileged, err, want)
		}
		in.Namespace = "default"
		if _, err := Compile(context.Background(), def, in); err != nil {
			t.Errorf("default: %v", err)
		}
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

// TestDependencies: references to an instance only, with their parameter
// path, the output named after the instance by the naming convention.
func TestDependencies(t *testing.T) {
	ch := maps.Clone(pkg)
	ch["config.schema.json"] = &fstest.MapFile{Data: []byte(`{"properties": {
		"replicas": {"type": "integer", "default": 1},
		"backends": {"type": "array", "items": {"properties": {
			"database": {"type": "string", "x-okdp-connection-ref": {"contract": "database"}}}}}}}`)}
	in := inputs(map[string]any{
		"backends": []any{
			map[string]any{"database": "ext"},     // an external connection: not a dependency
			map[string]any{"database": "demo-db"}, // an output of an instance
			map[string]any{"database": ""},        // not set
		},
		"connections": map[string]any{"ext": map[string]any{"contract": "database", "host": "db.example.org"}},
	})
	in.Contracts = fstest.MapFS{"database.schema.json": {Data: []byte(`{
		"x-okdp-contract": "database",
		"properties": {"host": {"type": "string"}},
		"required": ["host"]}`)}}
	in.Published = map[string]Info{"demo-db": {Provides: []Output{{Name: "demo-db", Contract: "database", Values: M{"host": "demo-db.demo.svc"}}}}}
	res, err := Compile(context.Background(), withFiles(ch), in)
	if err != nil {
		t.Fatal(err)
	}
	want := []Dependency{{Instance: "demo-db", Contract: "database", Field: "backends[1].database", Output: "demo-db"}}
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

// TestCompileSchemas: the parameters against the package's schema, every
// violation reported with its path; a null of the upstream values is kept
// in the final values, for Helm to delete the chart's default. The final
// values are checked against the upstream charts by the host (okdp-gitops).
func TestCompileSchemas(t *testing.T) {
	d := *def
	d.Package.Upstream = chart.Upstream{All: true}
	_, err := Compile(context.Background(), &d, inputs(M{"replicas": 1.5}))
	if want := "values don't meet the specifications of the schema(s) in the following chart(s):\ndemo:\n- at '/replicas': got number, want integer"; err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("got %v, want an error containing %q", err, want)
	}
	d.Package.Defaults = nil
	res, err := Compile(context.Background(), &d, inputs(M{"upstream": M{"app": M{"labels": nil}}}))
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
		"not written":      {"canary: {replicaCount: 3}\n", []string{"app", "canary"}},
		"no values file":   {"", []string{"app", "canary"}},
		"$if false":        {"app: {$if: '${{ values.replicas > 5 }}'}\n", []string{"canary"}},
		"null":             {"app: ~\n", []string{"canary"}},
		"null expression":  {"app: '${{ null }}'\ncanary: {}\n", []string{"canary"}},
		"section left out": {"$if: ${{ values.replicas > 5 }}\napp: {}\n", []string{"canary"}},
		"$merge":           {"$merge: \"${{ {'app': null} }}\"\n", []string{"canary"}},
		"$for":             {"{$for: \"k in ['canary']\", $key: '${{ k }}', $value: '${{ null }}'}\n", []string{"app"}},
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
			if app, ok := res.Values["app"]; ok && (app["replicaCount"] != nil || value.Dig(app, "image.tag") != "4.5" || value.Dig(app, "labels.team") != "a") {
				t.Errorf("app: OKDP's defaults and its upstream values, not its chart's: %#v", app)
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
// dependency rendered with the instance's global alone: Helm gives it its
// chart's defaults.
func TestChartAlone(t *testing.T) {
	d, err := LoadDefinition(fstest.MapFS{"Chart.yaml": {Data: []byte(`
apiVersion: v2
name: demo
version: 1.0.0
dependencies:
  - {name: app, repository: https://charts.example.org, version: 1.0.0}
  - {name: app, alias: canary, repository: https://charts.example.org, version: 1.0.0}
`)}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Compile(context.Background(), d, inputs())
	if err != nil {
		t.Fatal(err)
	}
	if got := slices.Sorted(maps.Keys(res.Values)); !slices.Equal(got, []string{"app", "canary"}) {
		t.Errorf("rendered: got %v", got)
	}
	for _, k := range []string{"app", "canary"} {
		if v := res.Values[k]; !reflect.DeepEqual(v, map[string]any{"global": platformValues["global"]}) {
			t.Errorf("%s: the global alone: %#v", k, v)
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
	if _, err := Compile(context.Background(), &d, in); err == nil || !strings.Contains(err.Error(), "values layer 2: global.imageRegistry is set by the context (Inputs.Context)") {
		t.Errorf("a key of global: got %v", err)
	}
}

// TestObjects: the objects section (directives included), in
// Result.Objects and in no chart: okdp is a dependency key as another;
// global.okdp.charts names no chart of the objects.
func TestObjects(t *testing.T) {
	names := func(objects []map[string]any) []string {
		var out []string
		for _, o := range objects {
			out = append(out, value.Str(o["kind"])+"/"+value.Str(value.Dig(o, "metadata.name")))
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
	all := []string{"ConfigMap/demo-app-extra", "Password/demo-app-internal-token", "ExternalSecret/demo-app-internal"}
	if got := names(res.Objects); !slices.Equal(got, all) {
		t.Errorf("Result.Objects: got %v, want %v", got, all)
	}
	if lbl := value.AsMap(value.Dig(res.Objects[0], "metadata.labels")); lbl["okdp.io/service"] != "demo" || lbl["app.kubernetes.io/version"] != nil || lbl["helm.sh/chart"] != nil {
		t.Errorf("labels: %#v", lbl)
	}
	if got := slices.Sorted(maps.Keys(res.Charts)); !slices.Equal(got, []string{"app"}) {
		t.Errorf("charts: got %v, want app alone", got)
	}
	// A mapping that renders no dependency: no chart, the objects all the
	// same.
	d.Values = []byte("app: ~\n")
	if res, err = Compile(context.Background(), &d, inputs()); err != nil || len(res.Values) != 0 || len(res.Objects) != len(all) {
		t.Errorf("no output: %v %v", err, res)
	}
	// okdp, the key of a dependency.
	d.Chart.Dependencies = []chart.Dependency{appDep, {Name: "app", Alias: "okdp", Repository: appSource.Repository, Version: appSource.Version}}
	d.Okdp, d.Values = nil, []byte("okdp: {replicaCount: 2}\n")
	if res, err = Compile(context.Background(), &d, inputs()); err != nil || res.Values["okdp"]["replicaCount"] != float64(2) || res.Charts["okdp"] != appSource {
		t.Errorf("the dependency okdp: %v %v", err, res)
	}
	// Finalize, in the compile: what it returns is the final values, its
	// error refuses the instance, what was computed stays apart.
	finalized := map[string]map[string]any{"app": {"final": true}}
	d.Values = []byte("app: {replicaCount: 2}\n")
	d.Chart.Dependencies = []chart.Dependency{appDep}
	d.Okdp = nil
	with := inputs()
	with.Finalize = func(_ context.Context, in FinalInput) (map[string]map[string]any, map[string]chart.Ref, []string, error) {
		if _, ok := in.Specs["app"]; !ok || in.ValuesFile == "" || value.AsMap(in.Output["app"])["replicaCount"] != float64(2) || in.Release != with.Release || in.Namespace != with.Namespace {
			t.Errorf("Finalize's input: %+v", in)
		}
		return finalized, map[string]chart.Ref{"app": appSource}, []string{"finalized"}, nil
	}
	if res, err = Compile(context.Background(), &d, with); err != nil || res.Values["app"]["final"] != true || !slices.Contains(res.Warnings, "finalized") || value.AsMap(res.Computed["app"])["replicaCount"] != float64(2) {
		t.Errorf("Finalize: %v %+v", err, res)
	}
	with.Finalize = func(context.Context, FinalInput) (map[string]map[string]any, map[string]chart.Ref, []string, error) {
		return nil, nil, nil, errors.New("not final")
	}
	if _, err = Compile(context.Background(), &d, with); err == nil || err.Error() != "not final" {
		t.Errorf("Finalize's error: got %v", err)
	}
	// The chart of the objects, by default and the platform's; its key the
	// compiler's.
	if res.ObjectsSource != Mandatory().Charts()["objects"] {
		t.Errorf("the default chart of the objects: %v", res.ObjectsSource)
	}
	own := chart.Ref{Repository: "oci://registry.example.org/okdp", Chart: "own-objects", Version: "2.0.0"}
	if res, err = Compile(context.Background(), &d, inputs(M{"global": M{"okdp": M{"charts": M{"objects": M{"repository": own.Repository, "chart": own.Chart, "version": own.Version}}}}})); err != nil || res.ObjectsSource != own {
		t.Errorf("the platform's chart of the objects: %v %v", err, res)
	}
	d.Chart.Dependencies = append(d.Chart.Dependencies, chart.Dependency{Name: "app", Alias: ObjectsChart, Repository: appSource.Repository, Version: appSource.Version})
	if _, err = Compile(context.Background(), &d, inputs()); err == nil || !strings.Contains(err.Error(), "the chart of the instance's objects is added by the compiler") {
		t.Errorf("the key of the objects listed: got %v", err)
	}
	for name, tc := range map[string]struct {
		okdp  string // okdp.yaml
		layer M
		want  string
	}{
		"not an object": {"objects: [{metadata: {name: x}}]\n", nil, "- at '/objects/0': missing property 'kind'"},
		"null charts":   {"", M{"global": M{"okdp": M{"charts": nil}}}, "- at '/global/okdp/charts': got null, want object"},
		"objects chart": {"", M{"global": M{"okdp": M{"charts": M{"objects": M{"repository": "quay.io"}}}}}, `global.okdp.charts.objects.repository "quay.io": the URL of a chart repository`},
	} {
		t.Run(name, func(t *testing.T) {
			d := *def
			d.Okdp = []byte(tc.okdp)
			if _, err := Compile(context.Background(), &d, inputs(tc.layer)); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

// TestSharedSource: two dependencies of Chart.yaml, one aliased, rendered by
// one upstream chart, each with its own values; its schema, read once from
// the chart cache, checks both; Result.Charts maps every chart to its
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
	if want := (map[string]chart.Ref{"app": appSource, "canary": appSource}); !reflect.DeepEqual(res.Charts, want) {
		t.Errorf("charts: got %#v, want %#v", res.Charts, want)
	}
	canary := res.Values["canary"]
	if canary["replicaCount"] != float64(2) || value.Dig(canary, "image.tag") != "4.5" || canary["labels"] != nil {
		t.Errorf("canary: OKDP's defaults, then the computed ones: %#v", canary)
	}
}

// TestCompileTimeout: Inputs.Timeout bounds one compile, beside the
// context's deadline: past it the instance is refused with its own message,
// not the context's; the caller's context cancelled is still its error; a
// tree compiled together refuses the instance alone; the cost of a compile
// is the result's (Result.Cost), counted under a limit.
func TestCompileTimeout(t *testing.T) {
	in := inputs()
	in.Timeout = time.Nanosecond
	res, err := Compile(context.Background(), def, in)
	if res != nil || err == nil || err.Error() != "the compile takes longer than the instance timeout (1ns)" || errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("past the instance timeout: %v, %v", res, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	in.Timeout = time.Hour
	if _, err := Compile(ctx, def, in); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: %v", err)
	}
	ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if _, err := Compile(ctx, def, in); !errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "instance timeout") {
		t.Errorf("the caller's deadline: %v", err)
	}
	in.Timeout = 0
	if res, err := Compile(context.Background(), def, in); err != nil || res.Cost != 0 {
		t.Errorf("no limit: %v, cost %v", err, res)
	}
	in.CostLimit = 1_000_000
	if res, err := Compile(context.Background(), def, in); err != nil || res.Cost == 0 || res.Cost > in.CostLimit {
		t.Errorf("under a limit: %v, cost %v", err, res)
	}
	slow, fast := Instance{Definition: def, Inputs: inputs(), Name: "slow"}, Instance{Definition: def, Inputs: inputs(), Name: "fast"}
	slow.Inputs.Release, fast.Inputs.Release = "demo-slow", "demo-fast"
	slow.Inputs.Timeout = time.Nanosecond
	out := CompileAll(context.Background(), []Instance{slow, fast})
	if out[0].Err == nil || !strings.Contains(out[0].Err.Error(), "instance timeout") || out[1].Err != nil || out[1].Result == nil {
		t.Errorf("a tree: %v / %v", out[0].Err, out[1].Err)
	}
}

// TestZeroRuleSet: Inputs.Rules that no LoadRules made refuses the compile,
// not a panic, nor a compile without its rules.
func TestZeroRuleSet(t *testing.T) {
	_, err := Compile(context.Background(), &Definition{}, Inputs{Rules: &RuleSet{}, Release: "r", Namespace: "n"})
	if !errors.Is(err, rules.ErrNotLoaded) {
		t.Errorf("got %v", err)
	}
}
