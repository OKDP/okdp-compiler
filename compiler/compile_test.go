package compiler

import (
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
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
			Charts:  map[string]ChartSpec{"app": {Fixed: "vendor-values/app.yaml"}},
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
