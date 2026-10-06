package okdp

import (
	"maps"
	"reflect"
	"strings"
	"testing"

	"github.com/okdp/okdp-compiler/internal/compile"
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
	res, err := runMapping(nil, []byte(`
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
	res, err := runMapping(nil, []byte(`
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
	maps.Copy(ctx, serviceContext(serviceMeta{Name: "demo", Version: "1.0.0"}, "demo-x"))
	src := `
output:
  x:
    version: ${{ service.version }}
    labels: ${{ labels }}
    selector: ${{ okdpSelectorLabels('db') }}
    loop: [{$for: "service in ['a']", $let: {selectorLabels: '${{ service }}'}, $each: '${{ selectorLabels }}'}]
`
	res, err := runMapping(nil, []byte(src), ctx)
	if err != nil {
		t.Fatal(err)
	}
	x := value.AsMap(res.output["x"])
	wantLabels := M{
		"app.kubernetes.io/name":       "demo",
		"app.kubernetes.io/instance":   "demo-x",
		"app.kubernetes.io/managed-by": "Helm",
		"app.kubernetes.io/part-of":    "okdp",
		"okdp.io/instance":             "demo-x",
		"okdp.io/service":              "demo",
	}
	if x["version"] != "1.0.0" || !reflect.DeepEqual(x["labels"], wantLabels) {
		t.Errorf("service and labels: %#v", x)
	}
	if want := (M{"app.kubernetes.io/name": "demo", "app.kubernetes.io/instance": "demo-x", "app.kubernetes.io/component": "db"}); !reflect.DeepEqual(x["selector"], want) {
		t.Errorf("selector: %#v", x["selector"])
	}
	if want := []any{"a"}; !reflect.DeepEqual(x["loop"], want) {
		t.Errorf("$for and $let shadow service and selectorLabels: %#v", x["loop"])
	}
	// A mapping's own labels binding wins over the context's.
	res, err = runMapping(nil, []byte("let:\n  labels: {mine: 'yes'}\n"+src), ctx)
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
	res, err := runMapping(nil, []byte("output: {x: '${{ null }}', web: ~, z: {}}\ninstance: ~\nprovides: '${{ null }}'\n"), mapCtx())
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
		"url not a string":       {"instance: {url: 8080, usage: ~}\n", `instance: url: must be a string, got 8080`},
		"usage not a string":     {"instance: {usage: [a]}\n", `instance: usage: must be a string, got []interface {}{"a"}`},
		"provides not a list":    {"provides: {contract: http}\n", `provides: must be a list of outputs, got map[string]interface {}{"contract":"http"}`},
		"objects not a list":     {"objects: x\n", `objects: must be a list of objects, got "x"`},
		"oidcClients not a list": {"oidcClients: {a: 1}\n", "oidcClients: must be a list of OAuth clients"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := runMapping(nil, []byte(tc.src), mapCtx())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

// TestGeneratedSecretArgs: okdpGeneratedSecret refuses an argument, a key
// field, of another type, rather than render it as empty or printed; a
// number or a boolean written as text is taken as Helm prints it.
func TestGeneratedSecretArgs(t *testing.T) {
	funcs := functions(mapCtx())
	for name, tc := range map[string]struct{ args, want string }{
		"not a map":        {`'s'`, `okdp.generatedSecret: must be a map of arguments (name, keys, stringData, labels, annotations, type, namespace, refreshInterval, apiVersion), got "s"`},
		"unknown":          {`{'name': 's', 'key': [], 'ctx': null}`, `okdp.generatedSecret: unknown argument(s) key (allowed: name, keys, `},
		"name":             {`{'name': 5}`, `okdp.generatedSecret: name must be a string, got 5`},
		"string":           {`{'name': 's', 'refreshInterval': 30}`, `okdp.generatedSecret: refreshInterval must be a string, got 30`},
		"keys":             {`{'name': 's', 'keys': {'key': 'k'}}`, `okdp.generatedSecret: keys must be a list of generated keys, got map[string]interface {}{"key":"k"}`},
		"stringData":       {`{'name': 's', 'stringData': ['a']}`, `okdp.generatedSecret: stringData must be a map of strings, numbers or booleans, got []interface {}{"a"}`},
		"stringData value": {`{'name': 's', 'stringData': {'a': 'x', 'b': null}}`, `okdp.generatedSecret: stringData "b" must be a string, a number or a boolean, got <nil>`},
		"labels":           {`{'name': 's', 'labels': {'team': 3}}`, `okdp.generatedSecret: labels "team" must be a string, got 3`},
		"key not a map":    {`{'name': 's', 'keys': ['k']}`, `okdp.generatedSecret: keys[0]: must be a map (key, length, digits, symbols, noUpper, transform), got "k"`},
		"key field":        {`{'name': 's', 'keys': [{'key': 'k', 'lenght': 20}]}`, `okdp.generatedSecret: keys[0]: unknown field(s) lenght (allowed: key, length, `},
		"key type":         {`{'name': 's', 'keys': [{'key': 5}]}`, `okdp.generatedSecret: keys[0]: key must be a string, got 5`},
		"length":           {`{'name': 's', 'keys': [{'key': 'k'}, {'key': 'l', 'length': 'long'}]}`, `okdp.generatedSecret: keys[1]: length must be a whole number >= 0, got "long"`},
		"digits null":      {`{'name': 's', 'keys': [{'key': 'k', 'digits': null}]}`, `okdp.generatedSecret: keys[0]: digits must be a whole number >= 0, got <nil>`},
		"symbols":          {`{'name': 's', 'keys': [{'key': 'k', 'symbols': -1}]}`, `okdp.generatedSecret: keys[0]: symbols must be a whole number >= 0, got -1`},
		"noUpper":          {`{'name': 's', 'keys': [{'key': 'k', 'noUpper': 1}]}`, `okdp.generatedSecret: keys[0]: noUpper must be true or false, got 1`},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := compile.Eval("okdpGeneratedSecret("+tc.args+")", M{}, funcs...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
	got, err := compile.Eval(`okdpGeneratedSecret({'name': 's', 'keys': [{'key': 'k', 'length': '20', 'noUpper': 'true'}]})`, M{}, funcs...)
	if err != nil {
		t.Fatal(err)
	}
	if spec := value.AsMap(value.AsMap(value.AsList(got)[0])["spec"]); spec["length"] != float64(20) || spec["noUpper"] != true {
		t.Errorf("Password spec: %#v", spec)
	}
}
