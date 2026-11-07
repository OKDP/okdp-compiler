package okdp

import (
	"context"
	"strings"
	"testing"

	"github.com/okdp/okdp-compiler/internal/chart"
)

// TestIngressHostsObjects: the objects of an instance of a project that is
// not privileged serve the hosts that end with -<namespace>.<suffix> or
// that the platform lists for it: an Ingress's rules and tls, a
// Certificate's dnsNames and commonName, a Gateway API route's hostnames,
// the annotations that name hosts, the items of a list included; a rule
// without host answers every host. The case and a final dot do not count.
// A privileged project's are not checked.
func TestIngressHostsObjects(t *testing.T) {
	ingress := func(spec string) string {
		return "  - {apiVersion: networking.k8s.io/v1, kind: Ingress, metadata: {name: web}, spec: " + spec + "}\n"
	}
	const refused = "is not served by demo: a host that ends with -demo.example.org, or one listed for it in global.okdp.policy.ingressHosts; the platform's projects serve any host (global.okdp.policy.privileged), demo is not one of them"
	for name, tc := range map[string]struct {
		objects string
		policy  M // global.okdp.policy
		want    string
	}{
		"the project's host":    {ingress("{rules: [{host: web-demo.example.org}], tls: [{hosts: [web-demo.example.org]}]}"), nil, ""},
		"a deeper name":         {ingress("{rules: [{host: a.web-demo.example.org}]}"), nil, ""},
		"case and a final dot":  {ingress("{rules: [{host: Web-Demo.Example.org.}]}"), nil, ""},
		"no ingress":            {"  - {apiVersion: v1, kind: ConfigMap, metadata: {name: a}, data: {host: sso.example.org}}\n", nil, ""},
		"another project's":     {ingress("{rules: [{host: web-other.example.org}]}"), nil, `demo: objects: Ingress/web: spec.rules[0].host: host "web-other.example.org" ` + refused},
		"the identity provider": {ingress("{rules: [{host: web-demo.example.org}, {host: sso.example.org}]}"), nil, `Ingress/web: spec.rules[1].host: host "sso.example.org" ` + refused},
		"the suffix alone":      {ingress("{rules: [{host: demo.example.org}]}"), nil, `host "demo.example.org" ` + refused},
		"a dash only":           {ingress("{rules: [{host: -demo.example.org}]}"), nil, `host "-demo.example.org" ` + refused},
		"a longer suffix":       {ingress("{rules: [{host: web-demo.example.org.evil.com}]}"), nil, `host "web-demo.example.org.evil.com" ` + refused},
		"a rule without host":   {ingress("{rules: [{http: {paths: []}}]}"), nil, "Ingress/web: spec.rules[0].host: a rule without host answers every host"},
		"a tls host":            {ingress("{rules: [{host: web-demo.example.org}], tls: [{hosts: [web-demo.example.org, sso.example.org]}]}"), nil, `spec.tls[0].hosts[1]: host "sso.example.org"`},
		"listed":                {ingress("{rules: [{host: sso.example.org}]}"), M{"ingressHosts": M{"demo": []any{"sso.example.org"}}}, ""},
		"listed for another":    {ingress("{rules: [{host: sso.example.org}]}"), M{"ingressHosts": M{"other": []any{"sso.example.org"}}}, `host "sso.example.org"`},
		"a certificate":         {"  - {apiVersion: cert-manager.io/v1, kind: Certificate, metadata: {name: c}, spec: {commonName: web-demo.example.org, dnsNames: [web-demo.example.org, sso.example.org]}}\n", nil, `Certificate/c: spec.dnsNames[1]: host "sso.example.org"`},
		"a common name":         {"  - {apiVersion: cert-manager.io/v1, kind: Certificate, metadata: {name: c}, spec: {commonName: sso.example.org}}\n", nil, `Certificate/c: spec.commonName: host "sso.example.org"`},
		"a route":               {"  - {apiVersion: gateway.networking.k8s.io/v1, kind: HTTPRoute, metadata: {name: r}, spec: {hostnames: [sso.example.org]}}\n", nil, `HTTPRoute/r: spec.hostnames[0]: host "sso.example.org"`},
		"an annotation":         {"  - {apiVersion: v1, kind: Service, metadata: {name: s, annotations: {external-dns.alpha.kubernetes.io/hostname: 'web-demo.example.org,sso.example.org'}}}\n", nil, `Service/s: metadata.annotations["external-dns.alpha.kubernetes.io/hostname"]: host "sso.example.org"`},
		"a list's item":         {"  - {apiVersion: v1, kind: List, metadata: {name: l}, items: [{apiVersion: networking.k8s.io/v1, kind: Ingress, metadata: {name: i}, spec: {rules: [{host: sso.example.org}]}}]}\n", nil, `Ingress/i: spec.rules[0].host`},
	} {
		t.Run(name, func(t *testing.T) {
			d := *def
			d.Okdp = []byte("objects:\n" + tc.objects)
			global := M{"okdp": M{"ingress": M{"suffix": "example.org"}}}
			if tc.policy != nil {
				global = M{"okdp": M{"policy": tc.policy}}
			}
			_, err := Compile(context.Background(), &d, inputs(map[string]any{"global": global}))
			if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Errorf("got %v, want %q", err, tc.want)
			}
			// A privileged project: not checked.
			privileged := M{"okdp": M{"policy": M{"privileged": []any{"demo"}}}}
			if _, err := Compile(context.Background(), &d, inputs(map[string]any{"global": privileged})); err != nil {
				t.Errorf("privileged: %v", err)
			}
		})
	}
}

// TestIngressHostsValues: the final values of a chart, the package's and
// the instance's upstream values, name under ingress (at any depth) the
// hosts the project serves: host, hostname, commonName, the strings of
// hosts, hostnames, dnsNames, extraHosts, the name of an extraHosts item,
// the annotations that name hosts; a key outside an ingress is not a host.
func TestIngressHostsValues(t *testing.T) {
	const prefix = `app: `
	for name, tc := range map[string]struct {
		user M // upstream.app
		want string
	}{
		"the project's":      {M{"ingress": M{"hostname": "app-demo.example.org", "hosts": []any{M{"host": "app-demo.example.org"}, "b-demo.example.org"}, "tls": []any{M{"hosts": []any{"app-demo.example.org"}}}}}, ""},
		"outside an ingress": {M{"database": M{"host": "db.example.org", "hosts": []any{"x.example.org"}}, "name": "x"}, ""},
		"a host":             {M{"master": M{"ingress": M{"host": "sso.example.org"}}}, prefix + `master.ingress.host: host "sso.example.org" is not served by demo`},
		"a hostname":         {M{"ingress": M{"hostname": "sso.example.org"}}, prefix + `ingress.hostname: host "sso.example.org"`},
		"hosts":              {M{"ingress": M{"web": M{"hosts": []any{"app-demo.example.org", "sso.example.org"}}}}, prefix + `ingress.web.hosts[1]: host "sso.example.org"`},
		"an item's host":     {M{"ingress": M{"hosts": []any{M{"host": "sso.example.org", "paths": []any{"/"}}}}}, prefix + `ingress.hosts[0].host: host "sso.example.org"`},
		"extra hosts":        {M{"ingress": M{"extraHosts": []any{M{"name": "sso.example.org"}}}}, prefix + `ingress.extraHosts[0].name: host "sso.example.org"`},
		"extra tls":          {M{"ingress": M{"extraTls": []any{M{"hosts": []any{"sso.example.org"}}}}}, prefix + `ingress.extraTls[0].hosts[0]: host "sso.example.org"`},
		"ingresses":          {M{"ingresses": M{"http": M{"host": "sso.example.org"}}}, prefix + `ingresses.http.host: host "sso.example.org"`},
		"a name outside":     {M{"ingress": M{"name": "sso.example.org"}}, ""},
		"an annotation":      {M{"ingress": M{"annotations": M{"cert-manager.io/alt-names": "a-demo.example.org sso.example.org"}}}, prefix + `ingress.annotations["cert-manager.io/alt-names"]: host "sso.example.org"`},
		"a boolean":          {M{"ingress": M{"host": true, "hostname": ""}}, ""},
		"listed":             {M{"ingress": M{"host": "sso.example.org"}}, ""},
	} {
		t.Run(name, func(t *testing.T) {
			d := *def
			d.Package.Upstream = chart.Upstream{All: true}
			d.Values = []byte("app: {}\n")
			ok := M{"okdp": M{"ingress": M{"suffix": "example.org"}}}
			if name == "listed" {
				ok = M{"okdp": M{"ingress": M{"suffix": "example.org"}, "policy": M{"ingressHosts": M{"demo": []any{"sso.example.org"}}}}}
			}
			_, err := Compile(context.Background(), &d, inputs(map[string]any{"global": ok, "upstream": M{"app": tc.user}}))
			if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Errorf("got %v, want %q", err, tc.want)
			}
			// A privileged project: not checked.
			ok["okdp"].(M)["policy"] = M{"privileged": []any{"demo"}}
			if _, err := Compile(context.Background(), &d, inputs(map[string]any{"global": ok, "upstream": M{"app": tc.user}})); err != nil {
				t.Errorf("privileged: %v", err)
			}
		})
	}
	// The package's own values are held to the rule: a host that does not
	// end with -<namespace>.<suffix>, the package's default included.
	d := *def
	d.Values = []byte("app:\n  ingress: {hostname: '${{ release.name }}.example.org'}\n")
	if _, err := Compile(context.Background(), &d, inputs()); err == nil || !strings.Contains(err.Error(), `app: ingress.hostname: host "demo-app.example.org" is not served by demo`) {
		t.Errorf("a package's host: got %v", err)
	}
	// No suffix: only the listed hosts.
	d.Values = []byte("app:\n  ingress: {hostname: app-demo.example.org}\n")
	in := inputs()
	in.Context = []ContextLayer{{Values: map[string]any{"global": M{"okdp": M{"ingress": M{"suffix": ""}}}}}}
	d.Package.Require = nil
	if _, err := Compile(context.Background(), &d, in); err == nil || !strings.Contains(err.Error(), "any host, global.okdp.ingress.suffix being unset, but those listed") {
		t.Errorf("no suffix: got %v", err)
	}
}

// TestIngressHostsSchema: global.okdp.policy.ingressHosts maps namespaces
// to host names.
func TestIngressHostsSchema(t *testing.T) {
	for name, tc := range map[string]struct {
		policy any
		want   string
	}{
		"hosts":      {M{"ingressHosts": M{"demo": []any{"sso.example.org"}, "other": []any{}}}, ""},
		"a wildcard": {M{"ingressHosts": M{"demo": []any{"*.example.org"}}}, "/global/okdp/policy/ingressHosts/demo/0"},
		"a scheme":   {M{"ingressHosts": M{"demo": []any{"https://sso.example.org"}}}, "/global/okdp/policy/ingressHosts/demo/0"},
		"a string":   {M{"ingressHosts": M{"demo": "sso.example.org"}}, "/global/okdp/policy/ingressHosts/demo"},
		"a name":     {M{"ingressHosts": M{"Demo_": []any{"a.example.org"}}}, "invalid propertyName 'Demo_'"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := platformSettings(M{"global": M{"okdp": M{"policy": tc.policy}}})
			if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Errorf("got %v, want %q", err, tc.want)
			}
		})
	}
}
