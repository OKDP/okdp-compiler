// Hosts: the host names a project that is not privileged may serve:
// <label>.<namespace>.<global.okdp.ingress.suffix>, or a host the
// platform lists for it (global.okdp.policy.ingressHosts).
//
// The rule exists because every project shares one namespace of host
// names. ingress-nginx merges the rules of one host across namespaces,
// and cert-manager signs a certificate for any name from the platform's
// issuer. Without it, a project's Ingress or Certificate that asks for
// keycloak.okdp.sandbox would take the identity provider's traffic, or
// get a certificate for it.

package okdp

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	ruleset "github.com/okdp/okdp-compiler/internal/rules"
	"github.com/okdp/okdp-compiler/value"
)

// hostRules holds the hosts a project may serve.
type hostRules struct {
	rules     *RuleSet
	namespace string
	suffix    string // global.okdp.ingress.suffix, "" when unset
	listed    M      // the hosts global.okdp.policy.ingressHosts gives namespace, in lower case
}

func newHostRules(rules *RuleSet, okdp M, namespace string) *hostRules {
	r := &hostRules{rules: rules, namespace: namespace, suffix: strings.ToLower(strings.Trim(value.Str(value.Dig(okdp, "ingress.suffix")), ".")), listed: M{}}
	for _, h := range value.AsList(value.AsMap(value.Dig(okdp, "policy.ingressHosts"))[namespace]) {
		r.listed[strings.ToLower(value.Str(h))] = true
	}
	return r
}

// check returns an error when the project may not serve host, found at
// where. The case and a final dot do not count.
func (r *hostRules) check(ctx context.Context, where, host string) error {
	return r.rules.Refuse(ctx, ruleset.CheckHost, M{"at": where, "raw": host, "host": strings.ToLower(strings.TrimSuffix(host, ".")), "ns": r.namespace, "suffix": r.suffix, "listed": r.listed})
}

// annotationHosts checks the hosts that the annotations a name. Only
// the annotations of the rule set's hostAnnotations name hosts. p is the
// path of a in the errors.
func (r *hostRules) annotationHosts(ctx context.Context, p string, a M) error {
	for _, k := range slices.Sorted(maps.Keys(a)) {
		if !r.rules.Has(ruleset.TableHostAnnotations, k) {
			continue
		}
		for _, h := range strings.FieldsFunc(value.Str(a[k]), func(c rune) bool { return c == ',' || c == ' ' || c == '\n' || c == '\t' }) {
			if err := r.check(ctx, value.Key(p, k), h); err != nil {
				return err
			}
		}
	}
	return nil
}

// CheckIngressHosts checks that the objects of an instance that is not
// privileged serve only hosts the project may serve. objects is
// Result.Objects; the items of a List are checked too. It checks the
// hosts of an Ingress, a cert-manager Certificate, a Gateway API route
// or Gateway, and the annotations that name hosts. An Ingress rule
// without a host and spec.defaultBackend answer every host, so both are
// refused.
func CheckIngressHosts(ctx context.Context, rules *RuleSet, svc, namespace string, okdp M, objects []M) error {
	r := newHostRules(rules, okdp, namespace)
	return eachObject(objects, func(o M) error {
		kind, group, name := value.Str(o["kind"]), apiGroup(value.Str(o["apiVersion"])), value.Str(value.Dig(o, "metadata.name"))
		at := func(p string) string { return fmt.Sprintf("%s: objects: %s/%s: %s", svc, kind, name, p) }
		if err := r.annotationHosts(ctx, "metadata.annotations", value.AsMap(value.Dig(o, "metadata.annotations"))); err != nil {
			return fmt.Errorf("%s: objects: %s/%s: %w", svc, kind, name, err)
		}
		host := func(p string, h any) error {
			return r.check(ctx, at(p), value.Str(h))
		}
		list := func(p string, v any) error {
			for i, h := range value.AsList(v) {
				if err := host(value.Index(p, i), h); err != nil {
					return err
				}
			}
			return nil
		}
		every := func(p, what string) error {
			return fmt.Errorf("%s: %s answers every host; the platform's projects serve any host (global.okdp.policy.privileged), %s is not one of them", at(p), what, namespace)
		}
		switch {
		case group == "networking.k8s.io" && kind == "Ingress":
			if value.Dig(o, "spec.defaultBackend") != nil {
				return every("spec.defaultBackend", "the default backend")
			}
			for i, rule := range value.AsList(value.Dig(o, "spec.rules")) {
				p := value.Key(value.Index("spec.rules", i), "host")
				if value.Str(value.AsMap(rule)["host"]) == "" {
					return every(p, "a rule without host")
				}
				if err := host(p, value.AsMap(rule)["host"]); err != nil {
					return err
				}
			}
			for i, tls := range value.AsList(value.Dig(o, "spec.tls")) {
				if err := list(value.Key(value.Index("spec.tls", i), "hosts"), value.AsMap(tls)["hosts"]); err != nil {
					return err
				}
			}
		case group == "cert-manager.io" && kind == "Certificate":
			if err := list("spec.dnsNames", value.Dig(o, "spec.dnsNames")); err != nil {
				return err
			}
			if cn := value.Dig(o, "spec.commonName"); cn != nil && cn != "" {
				return host("spec.commonName", cn)
			}
		case group == "gateway.networking.k8s.io" && (kind == "HTTPRoute" || kind == "GRPCRoute" || kind == "TLSRoute"):
			return list("spec.hostnames", value.Dig(o, "spec.hostnames"))
		case group == "gateway.networking.k8s.io" && kind == "Gateway":
			return listeners(ctx, r, at, o, every)
		}
		return nil
	})
}

// listeners checks the listeners of the Gateway o. A listener without a
// hostname whose protocol matches hosts (HTTP, HTTPS, TLS) answers every
// host, so it is refused. A listener of another protocol matches none.
func listeners(ctx context.Context, r *hostRules, at func(string) string, o M, every func(p, what string) error) error {
	for i, l := range value.AsList(value.Dig(o, "spec.listeners")) {
		m := value.AsMap(l)
		p := value.Key(value.Index("spec.listeners", i), "hostname")
		switch h := value.Str(m["hostname"]); {
		case h != "":
			if err := r.check(ctx, at(p), h); err != nil {
				return err
			}
		case slices.Contains([]string{"HTTP", "HTTPS", "TLS"}, value.Str(m["protocol"])):
			return every(p, "a listener without hostname")
		}
	}
	return nil
}
