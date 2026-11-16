package okdp

import (
	"context"
	"strings"
	"testing"

	"github.com/okdp/okdp-compiler/internal/testrules"
)

// TestIngressHostsObjects: the objects of an instance of a project that is
// not privileged serve the hosts of the form <label>.<namespace>.<suffix>,
// one DNS label, or that the platform lists for it: an Ingress's rules and
// tls, a Certificate's dnsNames and commonName, a Gateway API route's
// hostnames, a Gateway's listeners' hostname, the annotations that name
// hosts, the items of a list included; a rule without host, a default
// backend, a listener without hostname of a protocol that matches hosts
// answer every host. The case and a final dot do not count. A privileged
// project's are not checked.
func TestIngressHostsObjects(t *testing.T) {
	ingress := func(spec string) string {
		return "  - {apiVersion: networking.k8s.io/v1, kind: Ingress, metadata: {name: web}, spec: " + spec + "}\n"
	}
	gateway := func(listener string) string {
		return "  - {apiVersion: gateway.networking.k8s.io/v1, kind: Gateway, metadata: {name: g}, spec: {gatewayClassName: c, listeners: [" + listener + "]}}\n"
	}
	const refused = "is not served by demo: a host <label>.demo.example.org (one DNS label), or one listed for it in global.okdp.policy.ingressHosts; the platform's projects serve any host (global.okdp.policy.privileged), demo is not one of them"
	for name, tc := range map[string]struct {
		objects string
		policy  M // global.okdp.policy
		want    string
	}{
		"the project's host":    {ingress("{rules: [{host: web.demo.example.org}], tls: [{hosts: [web.demo.example.org]}]}"), nil, ""},
		"a deeper name":         {ingress("{rules: [{host: a.web.demo.example.org}]}"), nil, `host "a.web.demo.example.org" ` + refused},
		"the old form":          {ingress("{rules: [{host: web-demo.example.org}]}"), nil, `host "web-demo.example.org" ` + refused},
		"case and a final dot":  {ingress("{rules: [{host: Web.Demo.Example.org.}]}"), nil, ""},
		"no ingress":            {"  - {apiVersion: v1, kind: ConfigMap, metadata: {name: a}, data: {host: sso.example.org}}\n", nil, ""},
		"another project's":     {ingress("{rules: [{host: web.other.example.org}]}"), nil, `demo: objects: Ingress/web: spec.rules[0].host: host "web.other.example.org" ` + refused},
		"the identity provider": {ingress("{rules: [{host: web.demo.example.org}, {host: sso.example.org}]}"), nil, `Ingress/web: spec.rules[1].host: host "sso.example.org" ` + refused},
		"the suffix alone":      {ingress("{rules: [{host: demo.example.org}]}"), nil, `host "demo.example.org" ` + refused},
		"a dash only":           {ingress("{rules: [{host: -.demo.example.org}]}"), nil, `host "-.demo.example.org" ` + refused},
		"a label's edges":       {ingress("{rules: [{host: -web.demo.example.org}]}"), nil, `host "-web.demo.example.org" ` + refused},
		"a longer suffix":       {ingress("{rules: [{host: web.demo.example.org.evil.com}]}"), nil, `host "web.demo.example.org.evil.com" ` + refused},
		"a rule without host":   {ingress("{rules: [{http: {paths: []}}]}"), nil, "Ingress/web: spec.rules[0].host: a rule without host answers every host"},
		"a default backend":     {ingress("{defaultBackend: {service: {name: s, port: {number: 80}}}, rules: [{host: web.demo.example.org}]}"), nil, "Ingress/web: spec.defaultBackend: the default backend answers every host"},
		"a gateway's listener":  {gateway("{hostname: sso.example.org, protocol: HTTPS}"), nil, `Gateway/g: spec.listeners[0].hostname: host "sso.example.org" ` + refused},
		"a listener's wildcard": {gateway("{hostname: '*.demo.example.org', protocol: HTTPS}"), nil, `spec.listeners[0].hostname: host "*.demo.example.org" ` + refused},
		"a listener without":    {gateway("{protocol: HTTP}"), nil, "Gateway/g: spec.listeners[0].hostname: a listener without hostname answers every host"},
		"a TCP listener":        {gateway("{protocol: TCP, port: 5432}"), nil, ""},
		"a listener's host":     {gateway("{hostname: web.demo.example.org, protocol: HTTPS}"), nil, ""},
		"a tls host":            {ingress("{rules: [{host: web.demo.example.org}], tls: [{hosts: [web.demo.example.org, sso.example.org]}]}"), nil, `spec.tls[0].hosts[1]: host "sso.example.org"`},
		"listed":                {ingress("{rules: [{host: sso.example.org}]}"), M{"ingressHosts": M{"demo": []any{"sso.example.org"}}}, ""},
		"listed for another":    {ingress("{rules: [{host: sso.example.org}]}"), M{"ingressHosts": M{"other": []any{"sso.example.org"}}}, `host "sso.example.org"`},
		"a certificate":         {"  - {apiVersion: cert-manager.io/v1, kind: Certificate, metadata: {name: c}, spec: {commonName: web.demo.example.org, dnsNames: [web.demo.example.org, sso.example.org]}}\n", nil, `Certificate/c: spec.dnsNames[1]: host "sso.example.org"`},
		"a common name":         {"  - {apiVersion: cert-manager.io/v1, kind: Certificate, metadata: {name: c}, spec: {commonName: sso.example.org}}\n", nil, `Certificate/c: spec.commonName: host "sso.example.org"`},
		"a route":               {"  - {apiVersion: gateway.networking.k8s.io/v1, kind: HTTPRoute, metadata: {name: r}, spec: {hostnames: [sso.example.org]}}\n", nil, `HTTPRoute/r: spec.hostnames[0]: host "sso.example.org"`},
		"an annotation":         {"  - {apiVersion: v1, kind: Service, metadata: {name: s, annotations: {external-dns.alpha.kubernetes.io/hostname: 'web.demo.example.org,sso.example.org'}}}\n", nil, `Service/s: metadata.annotations["external-dns.alpha.kubernetes.io/hostname"]: host "sso.example.org"`},
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
			_, err := platformSettings(testrules.Standard(), M{"global": M{"okdp": M{"policy": tc.policy}}})
			if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Errorf("got %v, want %q", err, tc.want)
			}
		})
	}
}
