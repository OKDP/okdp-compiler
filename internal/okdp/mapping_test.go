package okdp

import (
	"maps"
	"reflect"
	"strings"
	"testing"

	"github.com/okdp/okdp-compiler/internal/value"
)

// mapCtx: a small context for mapping tests.
func mapCtx() M {
	return M{
		"release":  M{"name": "demo-x", "namespace": "demo"},
		"values":   M{"items": []any{M{"name": "a", "n": float64(1)}}},
		"global":   M{"okdp": M{}},
		"platform": M{},
	}
}

// TestMappingSections: let in order, instance and provides.
func TestMappingSections(t *testing.T) {
	res, err := runMapping([]byte(`
let:
  a: 1.0
  b: ${{ a + 1.0 }}
output:
  x: {b: '${{ b }}'}
instance:
  url: https://${{ release.name }}
provides:
  - contract: http
    values: {url: '${{ "u" + str(b) }}'}
`), mapCtx())
	if err != nil {
		t.Fatal(err)
	}
	if got := value.AsMap(res.output["x"])["b"]; got != float64(2) {
		t.Errorf("output: got %#v, want 2", got)
	}
	if got := res.instance["url"]; got != "https://demo-x" {
		t.Errorf("instance: got %#v", got)
	}
	want := []any{M{"contract": "http", "values": M{"url": "u2"}}}
	if !reflect.DeepEqual(res.provides, want) {
		t.Errorf("provides: got %#v", res.provides)
	}
}

// TestMappingFunctions: OKDP's host functions beside the language's, next
// to a let binding named fullname (the function is okdpFullname).
func TestMappingFunctions(t *testing.T) {
	res, err := runMapping([]byte(`
let:
  fullname: ${{ release.name }}-mine
output:
  x:
    name: ${{ fullname }}
    okdp: ${{ okdpFullname('app') }}
    quoted: ${{ quote(values.items[0].n) }}
`), mapCtx())
	if err != nil {
		t.Fatal(err)
	}
	want := M{"name": "demo-x-mine", "okdp": "demo-x-app", "quoted": `"1"`}
	if got := value.AsMap(res.output["x"]); !reflect.DeepEqual(got, want) {
		t.Errorf("got  %#v\nwant %#v", got, want)
	}
}

// TestMappingService: the package metadata and labels in the context; let
// and $let bindings and $for variables shadow them.
func TestMappingService(t *testing.T) {
	ctx := mapCtx()
	maps.Copy(ctx, serviceContext(serviceMeta{Name: "demo", Version: "1.0.0", AppVersion: "4.5"}, "demo-x"))
	src := `
output:
  x:
    tag: ${{ service.appVersion }}
    labels: ${{ labels }}
    selector: ${{ okdpSelectorLabels('db') }}
    loop: [{$for: "service in ['a']", $let: {selectorLabels: '${{ service }}'}, $each: '${{ selectorLabels }}'}]
`
	res, err := runMapping([]byte(src), ctx)
	if err != nil {
		t.Fatal(err)
	}
	x := value.AsMap(res.output["x"])
	wantLabels := M{
		"app.kubernetes.io/name":       "demo",
		"app.kubernetes.io/instance":   "demo-x",
		"app.kubernetes.io/version":    "4.5",
		"app.kubernetes.io/managed-by": "Helm",
		"app.kubernetes.io/part-of":    "okdp",
		"okdp.io/instance":             "demo-x",
		"okdp.io/service":              "demo",
	}
	if x["tag"] != "4.5" || !reflect.DeepEqual(x["labels"], wantLabels) {
		t.Errorf("service and labels: %#v", x)
	}
	if want := (M{"app.kubernetes.io/name": "demo", "app.kubernetes.io/instance": "demo-x", "app.kubernetes.io/component": "db"}); !reflect.DeepEqual(x["selector"], want) {
		t.Errorf("selector: %#v", x["selector"])
	}
	if want := []any{"a"}; !reflect.DeepEqual(x["loop"], want) {
		t.Errorf("$for and $let shadow service and selectorLabels: %#v", x["loop"])
	}
	// A mapping's own labels binding wins over the context's.
	res, err = runMapping([]byte("let:\n  labels: {mine: 'yes'}\n"+src), ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := (M{"mine": "yes"}); !reflect.DeepEqual(value.AsMap(res.output["x"])["labels"], want) {
		t.Errorf("let labels: %#v", value.AsMap(res.output["x"])["labels"])
	}
}

// TestMappingNulls: a null section is empty; a null chart of output is left
// out, as by $if.
func TestMappingNulls(t *testing.T) {
	res, err := runMapping([]byte("output: {x: '${{ null }}', web: ~, z: {}}\ninstance: ~\nprovides: '${{ null }}'\n"), mapCtx())
	if err != nil {
		t.Fatal(err)
	}
	if want := (M{"z": M{}}); !reflect.DeepEqual(res.output, want) {
		t.Errorf("output: got %#v, want %#v", res.output, want)
	}
	if len(res.instance) != 0 || res.provides != nil || res.objects != nil || res.clients != nil {
		t.Errorf("sections: %#v", res)
	}
}

// TestMappingErrors: OKDP's sections and functions, and where their errors
// point; a section of the wrong shape is refused.
func TestMappingErrors(t *testing.T) {
	for name, tc := range map[string]struct{ src, want string }{
		"function error":         {"output: {x: {v: '${{ okdpGeneratedSecret({}) }}'}}\n", "output: x: v: ${{ okdpGeneratedSecret({}) }}: okdp.generatedSecret: name is required"},
		"section error":          {"provides: [{values: '${{ nope }}'}]\n", "provides[0]: values: ${{ nope }}:"},
		"output not a map":       {"output: [x]\n", `output: must be a map of charts to their values, got []interface {}{"x"}`},
		"chart a scalar":         {"output: {x: {}, web: 'on'}\n", `output: web: must be a map of the chart's values, got "on"`},
		"chart a list":           {"output: {x: \"${{ ['a'] }}\"}\n", `output: x: must be a map of the chart's values, got []interface {}{"a"}`},
		"instance not a map":     {"instance: [https://x]\n", `instance: must be a map (url, usage), got []interface {}{"https://x"}`},
		"provides not a list":    {"provides: {contract: http}\n", `provides: must be a list of outputs, got map[string]interface {}{"contract":"http"}`},
		"objects not a list":     {"objects: x\n", `objects: must be a list of objects, got "x"`},
		"oidcClients not a list": {"oidcClients: {a: 1}\n", "oidcClients: must be a list of OAuth clients"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := runMapping([]byte(tc.src), mapCtx())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
}
