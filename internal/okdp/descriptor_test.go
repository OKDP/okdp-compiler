package okdp

import (
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
	d.Mapping = []byte("output: {app: {}}\ninstance: {url: ' https://x ', usage: use it}\nprovides:\n" + provides)
	in := inputs()
	in.Contracts = httpContract
	return Compile(&d, in)
}

// TestDescriptor: the ConfigMap, its labels and its outputs (a null secret
// is not set).
func TestDescriptor(t *testing.T) {
	res, err := provideCompile(`
  - contract: http
    values: {url: https://demo-app.example.org}
  - contract: http
    name: public
    values: {url: https://public.example.org}
    secretRef: {name: public-credentials, extra: dropped}
    secret: null
`)
	if err != nil {
		t.Fatal(err)
	}
	cm := res.Objects[0]
	md := value.AsMap(cm["metadata"])
	if md["name"] != "demo-app-okdp" || md["namespace"] != "demo" {
		t.Errorf("metadata: %#v", md)
	}
	labels := value.AsMap(md["labels"])
	for k, v := range map[string]any{
		"helm.sh/chart":             nil,
		"app.kubernetes.io/version": nil,
		"okdp.io/instance":          "demo-app",
		"okdp.io/service":           "demo",
		"okdp.io/provides-http":     "true",
	} {
		if labels[k] != v {
			t.Errorf("label %s: got %#v, want %#v", k, labels[k], v)
		}
	}
	data := value.AsMap(cm["data"])
	if data["url"] != "https://x" || data["usage"] != "use it\n" {
		t.Errorf("url and usage are trimmed: %#v", data)
	}
	want := `- contract: http
  name: demo-app
  values:
    port: 443
    url: https://demo-app.example.org
- contract: http
  name: public
  secretRef:
    name: public-credentials
  values:
    port: 443
    url: https://public.example.org
`
	if data["outputs.yaml"] != want {
		t.Errorf("outputs.yaml:\n%s\nwant:\n%s", data["outputs.yaml"], want)
	}
}

// TestDescriptorNoOutputs: an instance that provides nothing.
func TestDescriptorNoOutputs(t *testing.T) {
	res, err := provideCompile("  []\n")
	if err != nil {
		t.Fatal(err)
	}
	data := value.AsMap(res.Objects[0]["data"])
	if data["outputs.yaml"] != "[]\n" {
		t.Errorf("outputs.yaml: %#v", data["outputs.yaml"])
	}
	for k := range value.AsMap(value.AsMap(res.Objects[0]["metadata"])["labels"]) {
		if strings.HasPrefix(k, "okdp.io/provides-") {
			t.Errorf("unexpected label %s", k)
		}
	}
}

// TestDescriptorErrors: every output is checked, its shape, then against
// its contract.
func TestDescriptorErrors(t *testing.T) {
	for name, tc := range map[string]struct{ provides, want string }{
		"unknown contract": {"  - {contract: ftp, values: {}}\n", `provides "demo-app" (ftp): unknown contract`},
		"unreadable":       {"  - {contract: broken, values: {}}\n", `provides "demo-app" (broken): the broken contract cannot be read: broken.schema.json: unexpected EOF`},
		"type":             {"  - {contract: http, name: other, values: {url: u, port: '443'}}\n", `provides "other" (http): field "port" violates the http contract: got string, want integer`},
		"unknown field":    {"  - {contract: http, values: {url: u, path: /}}\n", `field "path" is not part of the http contract`},
		"secret field":     {"  - {contract: http, values: {url: u, token: t}}\n", `"token" is a secret field`},
		"required field":   {"  - {contract: http, name: other, values: {}}\n", `field "url" is required by the http contract`},
		"empty secret":     {"  - {contract: http, name: other, values: {url: u}, secret: {stringData: {}}}\n", `demo: output "other": secret needs stringData or generate`},
		"two outputs":      {"  - {contract: http, values: {url: https://demo-app.example.org}}\n  - {contract: http, name: demo-app, values: {url: u}}\n", `two http outputs are named "demo-app"`},
		"internal mismatch": {
			"  - {contract: http, values: {url: https://elsewhere}}\n",
			`url is "https://elsewhere" but internal references to "demo-app" resolve it to "https://demo-app.example.org"`,
		},
		"not a map":            {"  - {contract: http, name: other, values: {url: u}}\n  - ~\n", "provides[1]: must be a map (contract, name, values, secretRef, secret), got <nil>"},
		"values not a map":     {"  - {contract: http, values: [url]}\n", `provides[0]: values must be a map, got []interface {}{"url"}`},
		"secretRef not a map":  {"  - {contract: http, name: other, values: {url: u}, secretRef: creds}\n", `provides[0]: secretRef must be a map, got "creds"`},
		"secret not a map":     {"  - {contract: http, name: other, values: {url: u}, secret: [token]}\n", `provides[0]: secret must be a map, got []interface {}{"token"}`},
		"generate not a list":  {"  - {contract: http, name: other, values: {url: u}, secret: {generate: {token: 20}}}\n", `provides[0]: secret: generate must be a list, got map[string]interface {}{"token":20}`},
		"generated key":        {"  - {contract: http, name: other, values: {url: u}, secret: {generate: [{key: token, length: long}]}}\n", `provides[0]: secret: generate[0]: length must be a whole number >= 0, got "long"`},
		"stringData not a map": {"  - {contract: http, name: other, values: {url: u}, secret: {stringData: [token]}}\n", `provides[0]: secret: stringData must be a map of strings, got []interface {}{"token"}`},
		"stringData a number":  {"  - {contract: http, name: other, values: {url: u}, secret: {stringData: {token: 5}}}\n", `provides[0]: secret: stringData "token" must be a string, got 5`},
		"secretRef no name":    {"  - {contract: http, name: other, values: {url: u}, secretRef: {key: token}, secret: {stringData: {token: t}}}\n", `provides "other" (http): secretRef.name must be the name of a Secret, got <nil>`},
		"secretRef name":       {"  - {contract: http, name: other, values: {url: u}, secretRef: {name: 1}}\n", `provides "other" (http): secretRef.name must be the name of a Secret, got 1`},
		"one Secret": {
			"  - {contract: http, name: a, values: {url: u}, secretRef: {name: s}, secret: {stringData: {token: t}}}\n  - {contract: http, name: b, values: {url: u}, secretRef: {name: s}, secret: {stringData: {token: t}}}\n",
			`the credentials Secret of provides "b" (http): the credentials Secret of provides "a" (http) is the same object, Secret "s" in namespace "demo"`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := provideCompile(tc.provides)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

// TestDescriptorReport: the first error of the outputs, in the order of
// provides, the fields of each in key order: the same from one compile to
// the next.
func TestDescriptorReport(t *testing.T) {
	provides := "  - {contract: http, name: a, values: {url: u}}\n  - {contract: http, name: b, values: {url: u, zone: z, path: /}}\n  - {contract: ftp}\n"
	want := `provides "b" (http): field "path" is not part of the http contract`
	for range 20 {
		if _, err := provideCompile(provides); err == nil || err.Error() != want {
			t.Fatalf("got %v, want %q", err, want)
		}
	}
}

// TestDescriptorTypes: the values of the first output that fails, completed,
// against the contract's types: every violation at once, sorted by field.
func TestDescriptorTypes(t *testing.T) {
	provides := "  - {contract: http, name: a, values: {url: u}}\n  - {contract: http, name: b, values: {url: 1, port: '443'}}\n  - {contract: http, name: c, values: {url: 2}}\n"
	want := `provides "b" (http): field "port" violates the http contract: got string, want integer
provides "b" (http): field "url" violates the http contract: got number, want string`
	for range 20 {
		if _, err := provideCompile(provides); err == nil || err.Error() != want {
			t.Fatalf("got %v\nwant %s", err, want)
		}
	}
}

// TestDescriptorSecret: an output with a secret gets a secretRef, and its
// credentials Secret follows the descriptor.
func TestDescriptorSecret(t *testing.T) {
	res, err := provideCompile("  - {contract: http, name: other, values: {url: u}, secret: {stringData: {token: t}}}\n")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(value.Str(value.AsMap(res.Objects[0]["data"])["outputs.yaml"]), "secretRef:\n    name: other-http-credentials\n") {
		t.Errorf("outputs.yaml: %s", value.AsMap(res.Objects[0]["data"])["outputs.yaml"])
	}
	if len(res.Objects) != 2 || res.Objects[1]["kind"] != "Secret" || value.Dig(res.Objects[1], "metadata.name") != "other-http-credentials" {
		t.Fatalf("objects: %#v", res.Objects)
	}
	if l := value.AsMap(value.AsMap(res.Objects[1]["metadata"])["labels"]); l["okdp.io/credentials-http"] != "true" {
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
