package compiler

import (
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

// httpContract: an http contract with an internal naming convention and a
// secret field.
var httpContract = fstest.MapFS{"http.schema.json": {Data: []byte(`{
	"x-okdp-contract": "http",
	"x-okdp-internal": {"url": "https://${ref}.${suffix}"},
	"properties": {
		"url": {"type": "string"},
		"port": {"type": "integer", "default": 443},
		"token": {"type": "string", "x-okdp-secret": true}
	},
	"required": ["url"]}`)}}

// provideCompile: compiles the in-memory chart with this provides section.
func provideCompile(provides string) (*Result, error) {
	d := *def
	d.Mapping = []byte("output: {app: {}}\ninstance: {url: ' https://x ', usage: use it}\nprovides:\n" + provides)
	in := inputs()
	in.Contracts = httpContract
	return Compile(&d, in)
}

// TestDescriptor: the ConfigMap, its labels and its outputs.
func TestDescriptor(t *testing.T) {
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
	cm := res.Objects[0]
	md := asMap(cm["metadata"])
	if md["name"] != "demo-app-okdp" || md["namespace"] != "demo" {
		t.Errorf("metadata: %#v", md)
	}
	labels := asMap(md["labels"])
	for k, v := range map[string]any{
		"helm.sh/chart":             nil,
		"app.kubernetes.io/version": "4.5",
		"okdp.io/instance":          "demo-app",
		"okdp.io/service":           "demo",
		"okdp.io/provides-http":     "true",
	} {
		if labels[k] != v {
			t.Errorf("label %s: got %#v, want %#v", k, labels[k], v)
		}
	}
	data := asMap(cm["data"])
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
	data := asMap(res.Objects[0]["data"])
	if data["outputs.yaml"] != "[]\n" {
		t.Errorf("outputs.yaml: %#v", data["outputs.yaml"])
	}
	for k := range asMap(asMap(res.Objects[0]["metadata"])["labels"]) {
		if strings.HasPrefix(k, "okdp.io/provides-") {
			t.Errorf("unexpected label %s", k)
		}
	}
}

// TestDescriptorErrors: every output is checked against its contract.
func TestDescriptorErrors(t *testing.T) {
	for name, tc := range map[string]struct{ provides, want string }{
		"unknown contract": {"  - {contract: ftp, values: {}}\n", `provides "demo-app" (ftp): unknown contract`},
		"unknown field":    {"  - {contract: http, values: {url: u, path: /}}\n", `field "path" is not part of the http contract`},
		"secret field":     {"  - {contract: http, values: {url: u, token: t}}\n", `"token" is a secret field`},
		"required field":   {"  - {contract: http, name: other, values: {}}\n", `field "url" is required by the http contract`},
		"empty secret":     {"  - {contract: http, name: other, values: {url: u}, secret: {stringData: {}}}\n", `demo: output "other": secret needs stringData or generate`},
		"two outputs":      {"  - {contract: http, values: {url: https://demo-app.example.org}}\n  - {contract: http, name: demo-app, values: {url: u}}\n", `two http outputs are named "demo-app"`},
		"internal mismatch": {
			"  - {contract: http, values: {url: https://elsewhere}}\n",
			`url is "https://elsewhere" but internal references to "demo-app" resolve it to "https://demo-app.example.org"`,
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

// TestDescriptorSecret: an output with a secret gets a secretRef, and its
// credentials Secret follows the descriptor.
func TestDescriptorSecret(t *testing.T) {
	res, err := provideCompile("  - {contract: http, name: other, values: {url: u}, secret: {stringData: {token: t}}}\n")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(str(asMap(res.Objects[0]["data"])["outputs.yaml"]), "secretRef:\n    name: other-http-credentials\n") {
		t.Errorf("outputs.yaml: %s", asMap(res.Objects[0]["data"])["outputs.yaml"])
	}
	if len(res.Objects) != 2 || res.Objects[1]["kind"] != "Secret" || dig(res.Objects[1], "metadata.name") != "other-http-credentials" {
		t.Fatalf("objects: %#v", res.Objects)
	}
	if l := asMap(asMap(res.Objects[1]["metadata"])["labels"]); l["okdp.io/credentials-http"] != "true" {
		t.Errorf("labels: %#v", l)
	}
}

// TestTrunc: cut to n bytes, short strings unchanged.
func TestTrunc(t *testing.T) {
	if got := trunc("abcdef", 3); got != "abc" {
		t.Errorf("got %q", got)
	}
	if got := trunc("ab", 3); got != "ab" {
		t.Errorf("got %q", got)
	}
}

// TestLoadDefinition: both files are required, service.yaml must parse and
// name the service and its version; the package's other files are read by
// each compile.
func TestLoadDefinition(t *testing.T) {
	ok := fstest.MapFS{
		"service.yaml": {Data: []byte(`name: demo
version: 1.0.0
appVersion: 1.10
roles: [web]
dependsOn: [ingress]
require: [a]
charts: {app: {base: f.yaml, protect: [p]}}
`)},
		"mapping.cel.yaml": {Data: []byte("output: {}\n")},
	}
	d, err := LoadDefinition(ok)
	if err != nil {
		t.Fatal(err)
	}
	want := Service{
		Name: "demo", Version: "1.0.0", AppVersion: "1.10", Roles: []string{"web"}, DependsOn: []string{"ingress"},
		Require: []string{"a"}, Charts: map[string]ChartSpec{"app": {Base: "f.yaml", Protect: []string{"p"}}},
	}
	if !reflect.DeepEqual(d.Service, want) || string(d.Mapping) != "output: {}\n" || d.Files == nil {
		t.Errorf("got %#v", d)
	}
	for name, tc := range map[string]struct {
		fsys fstest.MapFS
		want string
	}{
		"no service.yaml":   {fstest.MapFS{"mapping.cel.yaml": ok["mapping.cel.yaml"]}, "service.yaml"},
		"no mapping":        {fstest.MapFS{"service.yaml": ok["service.yaml"]}, "mapping.cel.yaml"},
		"bad service.yaml":  {fstest.MapFS{"service.yaml": {Data: []byte("charts: [")}, "mapping.cel.yaml": ok["mapping.cel.yaml"]}, "service.yaml: "},
		"wrong field types": {fstest.MapFS{"service.yaml": {Data: []byte("charts: [a]\n")}, "mapping.cel.yaml": ok["mapping.cel.yaml"]}, "service.yaml: "},
		"no name":           {fstest.MapFS{"service.yaml": {Data: []byte("version: 1.0.0\n")}, "mapping.cel.yaml": ok["mapping.cel.yaml"]}, "service.yaml: name is required"},
		"no version":        {fstest.MapFS{"service.yaml": {Data: []byte("name: demo\n")}, "mapping.cel.yaml": ok["mapping.cel.yaml"]}, "service.yaml: version is required"},
	} {
		if _, err := LoadDefinition(tc.fsys); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want an error containing %q", name, err, tc.want)
		}
	}
	// A definition built in memory needs its package files.
	if _, err := Compile(&Definition{Service: want}, inputs()); err == nil || !strings.Contains(err.Error(), "Definition.Files is required") {
		t.Errorf("no files: got %v", err)
	}
}
