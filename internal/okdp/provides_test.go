package okdp

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/okdp/okdp-compiler/internal/value"
)

// httpContract: an http contract with an internal naming convention and a
// secret field, required (it is in the Secret, never in the values); and a
// contract that cannot be read.
var httpContract = fstest.MapFS{"http.schema.json": {Data: []byte(`{
	"x-okdp-contract": "http",
	"x-okdp-internal": {"url": "https://${ref}.${suffix}"},
	"properties": {
		"url": {"type": "string"},
		"port": {"type": "integer", "default": 443},
		"token": {"type": "string", "x-okdp-secret": true}
	},
	"required": ["url", "token"]}`)},
	"broken.schema.json": {Data: []byte(`{`)}}

// provideCompile: compiles the in-memory chart with this provides section.
func provideCompile(provides string) (*Result, error) {
	d := *def
	d.Okdp = []byte("instance: {url: ' https://x ', usage: use it}\nprovides:\n" + provides)
	in := inputs()
	in.Contracts = httpContract
	return Compile(context.Background(), &d, in)
}

// TestProvides: what the instance publishes, its url and usage trimmed,
// its outputs completed, named after the release unless named, their
// secretRef; no object without a secret.
func TestProvides(t *testing.T) {
	res, err := provideCompile(`
  - contract: http
    values: {url: https://demo-app.example.org}
  - contract: http
    name: public
    values: {url: https://public.example.org}
    secretRef: {name: public-credentials, extra: dropped}
`)
	if err != nil {
		t.Fatal(err)
	}
	want := Instance{URL: "https://x", Usage: "use it\n", Provides: []Output{
		{Name: "demo-app", Contract: "http", Values: M{"port": float64(443), "url": "https://demo-app.example.org"}},
		{Name: "public", Contract: "http", Values: M{"port": float64(443), "url": "https://public.example.org"}, SecretRef: &SecretRef{Name: "public-credentials"}},
	}}
	if !reflect.DeepEqual(res.Instance, want) {
		t.Errorf("got %#v\nwant %#v", res.Instance, want)
	}
	if res.Objects != nil {
		t.Errorf("objects: %#v", res.Objects)
	}
}

// TestProvidesNone: an instance that provides nothing, without url and
// usage.
func TestProvidesNone(t *testing.T) {
	d := *def
	d.Okdp = []byte("provides: []\n")
	res, err := Compile(context.Background(), &d, inputs())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Instance, Instance{}) {
		t.Errorf("got %#v", res.Instance)
	}
}

// TestProvidesErrors: every output is checked, its shape
// (mapping.schema.json), then against its contract.
func TestProvidesErrors(t *testing.T) {
	for name, tc := range map[string]struct{ provides, want string }{
		"missing contract": {"  - {contract: ftp, values: {}}\n", `provides "demo-app" (ftp): the ftp contract cannot be read: ftp.schema.json: file does not exist`},
		"unreadable":       {"  - {contract: broken, values: {}}\n", `provides "demo-app" (broken): the broken contract cannot be read: broken.schema.json: unexpected EOF`},
		"type":             {"  - {contract: http, name: other, values: {url: u, port: '443'}}\n", "provides \"other\" (http): the fields violate the http contract:\n- at '/port': got string, want integer"},
		"unknown field":    {"  - {contract: http, values: {url: u, path: /}}\n", `field "path" is not part of the http contract`},
		"secret field":     {"  - {contract: http, values: {url: u, token: t}}\n", `"token" is a secret field`},
		"required field":   {"  - {contract: http, name: other, values: {}}\n", `field "url" is required by the http contract`},
		"empty secret":     {"  - {contract: http, name: other, values: {url: u}, secret: {stringData: {}}}\n", `demo: output "other": secret needs stringData or generate`},
		"two outputs":      {"  - {contract: http, values: {url: https://demo-app.example.org}}\n  - {contract: http, name: demo-app, values: {url: https://demo-app.example.org}}\n", `two http outputs are named "demo-app"`},
		"two names twice":  {"  - {contract: http, name: b, values: {url: u}}\n  - {contract: http, name: a, values: {url: u}}\n  - {contract: http, name: a, values: {url: u}}\n  - {contract: http, name: b, values: {url: u}}\n", "two http outputs are named \"b\"\ntwo http outputs are named \"a\""},
		"internal mismatch": {
			"  - {contract: http, values: {url: https://elsewhere}}\n",
			`url is "https://elsewhere" but internal references to "demo-app" resolve it to "https://demo-app.example.org"`,
		},
		"not a map":            {"  - {contract: http, name: other, values: {url: u}}\n  - ~\n", "the mapping's sections don't meet the specifications of mapping.schema.json:\n- at '/provides/1': got null, want object"},
		"values not a map":     {"  - {contract: http, values: [url]}\n", "- at '/provides/0/values': got array, want object"},
		"secretRef not a map":  {"  - {contract: http, name: other, values: {url: u}, secretRef: creds}\n", "- at '/provides/0/secretRef': got string, want object"},
		"secret not a map":     {"  - {contract: http, name: other, values: {url: u}, secret: [token]}\n", "- at '/provides/0/secret': got array, want object"},
		"a null secret":        {"  - {contract: http, name: other, values: {url: u}, secret: ~}\n", "- at '/provides/0/secret': got null, want object"},
		"generate not a list":  {"  - {contract: http, name: other, values: {url: u}, secret: {generate: {token: 20}}}\n", "- at '/provides/0/secret/generate': got object, want array"},
		"generated key":        {"  - {contract: http, name: other, values: {url: u}, secret: {generate: [{key: token, length: long}]}}\n", "- at '/provides/0/secret/generate/0/length': got string, want integer"},
		"stringData not a map": {"  - {contract: http, name: other, values: {url: u}, secret: {stringData: [token]}}\n", "- at '/provides/0/secret/stringData': got array, want object"},
		"stringData a number":  {"  - {contract: http, name: other, values: {url: u}, secret: {stringData: {token: 5}}}\n", "- at '/provides/0/secret/stringData/token': got number, want string"},
		"secretRef no name":    {"  - {contract: http, name: other, values: {url: u}, secretRef: {key: token}, secret: {stringData: {token: t}}}\n", "- at '/provides/0/secretRef': missing property 'name'"},
		"secretRef name":       {"  - {contract: http, name: other, values: {url: u}, secretRef: {name: 1}}\n", "- at '/provides/0/secretRef/name': got number, want string"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := provideCompile(tc.provides)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

// TestProvidesReport: the first error of the outputs, in the order of
// provides, the fields of each in key order: the same from one compile to
// the next.
func TestProvidesReport(t *testing.T) {
	provides := "  - {contract: http, name: a, values: {url: u}}\n  - {contract: http, name: b, values: {url: u, zone: z, path: /}}\n  - {contract: ftp}\n"
	want := `provides "b" (http): field "path" is not part of the http contract`
	for range 20 {
		if _, err := provideCompile(provides); err == nil || err.Error() != want {
			t.Fatalf("got %v, want %q", err, want)
		}
	}
}

// TestProvidesTypes: the values of the first output that fails, completed,
// against the contract's types: every violation at once, sorted by field.
func TestProvidesTypes(t *testing.T) {
	provides := "  - {contract: http, name: a, values: {url: u}}\n  - {contract: http, name: b, values: {url: 1, port: '443'}}\n  - {contract: http, name: c, values: {url: 2}}\n"
	want := `provides "b" (http): the fields violate the http contract:
- at '/port': got string, want integer
- at '/url': got number, want string`
	for range 20 {
		if _, err := provideCompile(provides); err == nil || err.Error() != want {
			t.Fatalf("got %v\nwant %s", err, want)
		}
	}
}

// TestProvidesSecret: an output with a secret gets a secretRef, and its
// credentials Secret is an object.
func TestProvidesSecret(t *testing.T) {
	res, err := provideCompile("  - {contract: http, name: other, values: {url: u}, secret: {stringData: {token: t}}}\n")
	if err != nil {
		t.Fatal(err)
	}
	if ref := res.Instance.Provides[0].SecretRef; ref == nil || ref.Name != "other-http-credentials" {
		t.Errorf("secretRef: %#v", ref)
	}
	if len(res.Objects) != 1 || res.Objects[0]["kind"] != "Secret" || value.Dig(res.Objects[0], "metadata.name") != "other-http-credentials" {
		t.Fatalf("objects: %#v", res.Objects)
	}
	if l := value.AsMap(value.AsMap(res.Objects[0]["metadata"])["labels"]); l["okdp.io/credentials-http"] != "true" {
		t.Errorf("labels: %#v", l)
	}
	// Generated, the Secret's stringData is written as text, a number too.
	res, err = provideCompile("  - {contract: http, name: other, values: {url: u}, secret: {generate: [{key: token}], stringData: {port: 443}}}\n")
	if err != nil {
		t.Fatal(err)
	}
	if es := res.Objects[len(res.Objects)-1]; value.Dig(es, "spec.target.template.data.port") != "443" {
		t.Errorf("ExternalSecret: %#v", es)
	}
}
