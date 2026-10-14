package okdp

import (
	"context"
	"crypto/sha256"
	"fmt"
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

// testInstance: the instance whose mapping sees scope, as a test writes
// it: its release, service and OAuth client mode read back from it.
func testInstance(scope M) *instance {
	oidc := value.AsMap(value.Dig(scope, "platform.oidc"))
	return &instance{
		release:   value.Str(value.Dig(scope, "release.name")),
		namespace: value.Str(value.Dig(scope, "release.namespace")),
		svc:       &Service{Name: value.Str(value.Dig(scope, "service.name")), Version: value.Str(value.Dig(scope, "service.version"))},
		oidc:      oidc,
		dcr:       value.Dig(oidc, "dcr.enabled") == true,
		scope:     scope,
	}
}

// newSession: the session of a compile on the instance whose mapping sees
// scope (testInstance), as Compile opens it, without a cost limit.
func newSession(scope M) *compile.Session {
	var s *compile.Session
	s = compile.NewSession(context.Background(), 0, functions(testInstance(scope), &s)...)
	return s
}

// mapping: the sources of a package's mapping (Definition.sources): its
// okdp.yaml and values.cel.yaml, each when not "".
func mapping(okdp, values string) []compile.Source {
	var d Definition
	if okdp != "" {
		d.Okdp = []byte(okdp)
	}
	if values != "" {
		d.Values = []byte(values)
	}
	return d.sources()
}

// eval: the CEL expression expr (without ${{ }}) on the variables of scope,
// in the session s: the value v of the template {values: {v: ${{ expr }}}}.
func eval(s *compile.Session, expr string, scope M) (any, error) {
	v, err := evalTemplate(s, "v.cel.yaml", []byte("values:\n  v: |-\n    ${{ "+expr+" }}\n"), scope)
	return v["v"], err
}

// TestMappingSections: vars in order, output, instance and provides, read
// from okdp.yaml, its package keys skipped, and values.cel.yaml.
func TestMappingSections(t *testing.T) {
	res, err := runMapping(newSession(mapCtx()), mapCtx(), mapping(`
roles: [web]
require: [a.b]
upstream: {x: {protect: [a]}}
vars:
  a: 1.0
  b: ${{ a + 1.0 }}
instance:
  url: https://${{ release.name }}
provides:
  - contract: http
    values: {url: '${{ "u" + str(b) }}'}
`, "x: {b: '${{ b }}'}\n")...)
	if err != nil {
		t.Fatal(err)
	}
	if got := value.AsMap(value.Dig(res, "output.x"))["b"]; got != float64(2) {
		t.Errorf("output: got %#v, want 2", got)
	}
	if got := value.Dig(res, "instance.url"); got != "https://demo-x" {
		t.Errorf("instance: got %#v", got)
	}
	want := []any{M{"contract": "http", "values": M{"url": "u2"}}}
	if !reflect.DeepEqual(res["provides"], want) {
		t.Errorf("provides: got %#v", res["provides"])
	}
}

// TestMappingFunctions: OKDP's host functions beside the language's, next
// to a vars binding named fullname (the function is okdpFullname).
func TestMappingFunctions(t *testing.T) {
	res, err := runMapping(newSession(mapCtx()), mapCtx(), mapping("vars:\n  fullname: ${{ release.name }}-mine\n", `
x:
  name: ${{ fullname }}
  okdp: ${{ okdpFullname('app') }}
  quoted: ${{ quote(values.items[0].n) }}
`)...)
	if err != nil {
		t.Fatal(err)
	}
	want := M{"name": "demo-x-mine", "okdp": "demo-x-app", "quoted": `"1"`}
	if got := value.AsMap(value.Dig(res, "output.x")); !reflect.DeepEqual(got, want) {
		t.Errorf("got  %#v\nwant %#v", got, want)
	}
}

// TestMappingService: the package metadata and labels in the context; vars
// and $vars bindings and $for variables shadow them.
func TestMappingService(t *testing.T) {
	ctx := mapCtx()
	maps.Copy(ctx, M{"service": M{"name": "demo", "version": "1.0.0"}, "labels": labels("demo", "demo-x", "", false), "selectorLabels": labels("demo", "demo-x", "", true)})
	src := `
x:
  version: ${{ service.version }}
  labels: ${{ labels }}
  selector: ${{ okdpSelectorLabels('db') }}
  loop: [{$for: "service in ['a']", $vars: {selectorLabels: '${{ service }}'}, $each: '${{ selectorLabels }}'}]
`
	res, err := runMapping(newSession(ctx), ctx, mapping("", src)...)
	if err != nil {
		t.Fatal(err)
	}
	x := value.AsMap(value.Dig(res, "output.x"))
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
		t.Errorf("$for and $vars shadow service and selectorLabels: %#v", x["loop"])
	}
	// A mapping's own labels binding wins over the context's.
	res, err = runMapping(newSession(ctx), ctx, mapping("vars:\n  labels: {mine: 'yes'}\n", src)...)
	if err != nil {
		t.Fatal(err)
	}
	if want := (M{"mine": "yes"}); !reflect.DeepEqual(value.AsMap(value.Dig(res, "output.x"))["labels"], want) {
		t.Errorf("vars labels: %#v", value.AsMap(value.Dig(res, "output.x"))["labels"])
	}
}

// TestMappingNulls: a null section is empty; a null chart of output, a null
// key of a client, are refused by their types.
func TestMappingNulls(t *testing.T) {
	res, err := runMapping(newSession(mapCtx()), mapCtx(), mapping("instance: ~\nprovides: '${{ null }}'\n", "z: {}\n")...)
	if err != nil {
		t.Fatal(err)
	}
	if want := (M{"z": M{}}); !reflect.DeepEqual(res["output"], want) {
		t.Errorf("output: got %#v, want %#v", res["output"], want)
	}
	if res["instance"] != nil || res["provides"] != nil || res["objects"] != nil {
		t.Errorf("sections: %#v", res)
	}
	want := "the mapping's sections don't meet the specifications of mapping.schema.json:\n- at '/oidcClients/0/scope': got null, want string\n- at '/output': validation failed\n  - at '/output/web': got null, want object\n  - at '/output/x': got null, want object"
	if _, err := runMapping(newSession(mapCtx()), mapCtx(), mapping("oidcClients: [{grantTypes: [a], scope: ~}]\n", "{x: '${{ null }}', web: ~, z: {}}\n")...); err == nil || err.Error() != want {
		t.Errorf("got %v, want %q", err, want)
	}
}

// TestMappingErrors: OKDP's sections and functions, and where their errors
// point; a section of the wrong shape (mapping.schema.json), or none of
// OKDP's, is refused.
func TestMappingErrors(t *testing.T) {
	for name, tc := range map[string]struct{ okdp, values, want string }{
		"function error":         {"", "x: {v: '${{ okdpGeneratedSecret({}) }}'}\n", "values.cel.yaml: x: v: ${{ okdpGeneratedSecret({}) }}: okdpGeneratedSecret: the arguments don't meet the specifications of mapping.schema.json:\n- at '/okdpGeneratedSecret': missing property 'name'"},
		"section error":          {"provides: [{values: '${{ nope }}'}]\n", "", "okdp.yaml: provides[0]: values: ${{ nope }}:"},
		"output not a map":       {"", "[x]\n", "the mapping's sections don't meet the specifications of mapping.schema.json:\n- at '/output': got array, want object"},
		"chart a scalar":         {"", "{x: {}, web: 'on'}\n", "- at '/output/web': got string, want object"},
		"instance not a map":     {"instance: [https://x]\n", "", "- at '/instance': got array, want object"},
		"url not a string":       {"instance: {url: 8080, usage: ~}\n", "", "- at '/instance/url': got number, want string"},
		"provides not a list":    {"provides: {contract: http}\n", "", "- at '/provides': got object, want array"},
		"objects not a list":     {"objects: x\n", "", "- at '/objects': got string, want array"},
		"oidcClients not a list": {"oidcClients: {a: 1}\n", "", "- at '/oidcClients': got object, want array"},
		"no grant types":         {"oidcClients: [{grantTypes: []}, {scope: s}]\n", "", "- at '/oidcClients/0/grantTypes': minItems: got 0, want 1\n- at '/oidcClients/1': missing property 'grantTypes'"},
		"client name":            {"oidcClients: [{grantTypes: [a], name: Console}]\n", "", "- at '/oidcClients/0/name': 'Console' does not match pattern"},
		"client name length":     {"oidcClients: [{grantTypes: [a], name: " + strings.Repeat("a", 55) + "}]\n", "", "- at '/oidcClients/0/name': maxLength: got 55, want 54"},
		"client key":             {"oidcClients: [{grantTypes: [a], redirectUri: x}]\n", "", "- at '/oidcClients/0': additional properties 'redirectUri' not allowed"},
		"unknown section":        {"outptu: {x: {}}\n", "", "okdp.yaml: outptu: unknown section (allowed: vars, errors, output, instance, provides, objects, oidcClients)"},
		"output in okdp.yaml":    {"output: {}\n", "{}\n", "values.cel.yaml: output: okdp.yaml gives it too"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := runMapping(newSession(mapCtx()), mapCtx(), mapping(tc.okdp, tc.values)...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

// TestGeneratedSecretArgs: okdpGeneratedSecret refuses an argument, a key
// field, of another type (mapping.schema.json), rather than render it as
// empty or printed, a null one included.
func TestGeneratedSecretArgs(t *testing.T) {
	s := newSession(mapCtx())
	for name, tc := range map[string]struct{ args, want string }{
		"not a map": {`'s'`, "okdpGeneratedSecret: the arguments don't meet the specifications of mapping.schema.json:\n- at '/okdpGeneratedSecret': got string, want object"},
		"unknown":   {`{'name': 's', 'key': []}`, "- at '/okdpGeneratedSecret': additional properties 'key' not allowed"},
		"key field": {`{'name': 's', 'keys': [{'key': 'k', 'lenght': 20}]}`, "- at '/okdpGeneratedSecret/keys/0': additional properties 'lenght' not allowed"},
		"as text":   {`{'name': 's', 'keys': [{'key': 'k', 'length': '20'}]}`, "- at '/okdpGeneratedSecret/keys/0/length': got string, want integer"},
		"transform": {`{'name': 's', 'keys': [{'key': 'k', 'transform': 'md5'}]}`, "- at '/okdpGeneratedSecret/keys/0/transform': value must be one of '', 'sha256', 'b64enc'"},
		"null":      {`{'name': 's', 'keys': [{'key': 'k', 'digits': null}], 'stringData': {'a': null}}`, "- at '/okdpGeneratedSecret/keys/0/digits': got null, want integer\n- at '/okdpGeneratedSecret/stringData/a': got null, want boolean or number or string"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := eval(s, "okdpGeneratedSecret("+tc.args+")", M{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
	got, err := eval(s, `okdpGeneratedSecret({'name': 's', 'keys': [{'key': 'k'}]})`, M{})
	if err != nil {
		t.Fatal(err)
	}
	if spec := value.AsMap(value.AsList(got)[0])["spec"]; value.AsMap(spec)["digits"] != float64(6) {
		t.Errorf("Password spec: %#v", spec)
	}
}

// TestFullname: okdpFullname, <release>-<suffix> cut to 63 characters
// without a trailing "-"; the release alone without a suffix.
func TestFullname(t *testing.T) {
	release := strings.Repeat("r", 41)
	s := newSession(M{"release": M{"name": release}})
	for suffix, want := range map[string]string{
		"":                       release,
		"app":                    release + "-app",
		strings.Repeat("s", 21):  release + "-" + strings.Repeat("s", 21),
		strings.Repeat("s", 22):  release + "-" + strings.Repeat("s", 21),
		"abcdefghijklmnopqrst-u": release + "-abcdefghijklmnopqrst",
	} {
		if got, err := eval(s, "okdpFullname(x)", M{"x": suffix}); err != nil || got != want {
			t.Errorf("okdpFullname(%q): got %v (%v), want %q", suffix, got, err, want)
		}
	}
	if got, err := eval(s, "okdpFullname()", M{}); err != nil || got != release {
		t.Errorf("okdpFullname(): got %v (%v)", got, err)
	}
}

// TestGeneratedSecretNames: a generator is named <name>-<key>, lowercased,
// "_" and "." as "-"; past 63 characters, cut to 54 and suffixed with 8 hex
// characters of the key's SHA-256. Two keys of one name fail.
func TestGeneratedSecretNames(t *testing.T) {
	s := newSession(mapCtx())
	long := strings.Repeat("k", 62)
	got, err := eval(s, `okdpGeneratedSecret({'name': 's', 'keys': [{'key': 'My_Key.x'}, {'key': '`+long+`'}]})`, M{})
	if err != nil {
		t.Fatal(err)
	}
	objects := value.AsList(got)
	sum := fmt.Sprintf("%x", sha256.Sum256([]byte(long)))[:8]
	for i, want := range []string{"s-my-key-x", ("s-" + long)[:54] + "-" + sum} {
		if name := value.Dig(value.AsMap(objects[i]), "metadata.name"); name != want {
			t.Errorf("generator %d: got %v, want %s", i, name, want)
		}
	}
	want := `okdpGeneratedSecret: s: keys "a_b" and "a.b" give the same generator name "s-a-b"`
	if _, err := eval(s, `okdpGeneratedSecret({'name': 's', 'keys': [{'key': 'a_b'}, {'key': 'a.b'}]})`, M{}); err == nil || !strings.HasSuffix(err.Error(), want) {
		t.Errorf("got %v, want an error containing %q", err, want)
	}
}
