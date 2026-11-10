// Hosts: the host names a project that is not privileged serves, the
// platform's rule: <something>-<namespace>.<global.okdp.ingress.suffix>, or
// a host the platform lists for the project
// (global.okdp.policy.ingressHosts). A host is a name in a namespace shared
// by every project (ingress-nginx merges the rules of one host across
// namespaces, cert-manager signs the certificate of any name from the
// platform's issuer): the sandbox's keycloak.okdp.sandbox, asked by a
// project's Ingress or Certificate, would take the identity provider's
// traffic or a certificate for it.

package okdp

import (
	"fmt"
	ruleset "github.com/okdp/okdp-compiler/internal/rules"
	"maps"
	"slices"
	"strings"

	"github.com/okdp/okdp-compiler/value"
)

// hostRules: the hosts a project may serve (checkIngressHosts).
type hostRules struct {
	rules     *RuleSet
	namespace string
	suffix    string // global.okdp.ingress.suffix, "" when unset
	listed    M      // global.okdp.policy.ingressHosts of namespace, lower case, as a set
}

// newHostRules: the rules of the project of namespace, of okdp, the
// platform's settings.
func newHostRules(rules *RuleSet, okdp M, namespace string) *hostRules {
	r := &hostRules{rules: rules, namespace: namespace, suffix: strings.ToLower(strings.Trim(value.Str(value.Dig(okdp, "ingress.suffix")), ".")), listed: M{}}
	for _, h := range value.AsList(value.AsMap(value.Dig(okdp, "policy.ingressHosts"))[namespace]) {
		r.listed[strings.ToLower(value.Str(h))] = true
	}
	return r
}

// check: the error of the host, found at where, when the project may not
// serve it (the rule set's host check). The case and a final dot do not
// count.
func (r *hostRules) check(where, host string) error {
	return r.rules.Refuse(ruleset.CheckHost, M{"at": where, "raw": host, "host": strings.ToLower(strings.TrimSuffix(host, ".")), "ns": r.namespace, "suffix": r.suffix, "listed": r.listed})
}

// annotationHosts: the first host of the annotations a (a map of
// annotations) that the rules refuse, by annotation key order; nil when
// they all hold.
func (r *hostRules) annotationHosts(p string, a M) error {
	for _, k := range slices.Sorted(maps.Keys(a)) {
		if !r.rules.Has(ruleset.TableHostAnnotations, k) {
			continue
		}
		for _, h := range strings.FieldsFunc(value.Str(a[k]), func(c rune) bool { return c == ',' || c == ' ' || c == '\n' || c == '\t' }) {
			if err := r.check(value.Key(p, k), h); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkIngressHosts: the objects of an instance of svc in namespace, one
// that is not privileged (Result.Objects, the items of a list alike),
// serve the hosts the platform lets the project serve (hostRules): an
// Ingress's rules (a rule without host answers every host) and tls hosts,
// a cert-manager Certificate's dnsNames and commonName, a Gateway API
// route's hostnames, and the annotations that name hosts (the rule set's hostAnnotations)
// of any object. The first refused, in order, fails, named <kind>/<name>,
// its field named.
func checkIngressHosts(rules *RuleSet, svc, namespace string, okdp M, objects []M) error {
	r := newHostRules(rules, okdp, namespace)
	return eachObject(objects, func(o M) error {
		kind, group, name := value.Str(o["kind"]), apiGroup(value.Str(o["apiVersion"])), value.Str(value.Dig(o, "metadata.name"))
		at := func(p string) string { return fmt.Sprintf("%s: objects: %s/%s: %s", svc, kind, name, p) }
		if err := r.annotationHosts("metadata.annotations", value.AsMap(value.Dig(o, "metadata.annotations"))); err != nil {
			return fmt.Errorf("%s: objects: %s/%s: %w", svc, kind, name, err)
		}
		host := func(p string, h any) error {
			return r.check(at(p), value.Str(h))
		}
		list := func(p string, v any) error {
			for i, h := range value.AsList(v) {
				if err := host(value.Index(p, i), h); err != nil {
					return err
				}
			}
			return nil
		}
		switch {
		case group == "networking.k8s.io" && kind == "Ingress":
			for i, rule := range value.AsList(value.Dig(o, "spec.rules")) {
				p := value.Key(value.Index("spec.rules", i), "host")
				if value.Str(value.AsMap(rule)["host"]) == "" {
					return fmt.Errorf("%s: a rule without host answers every host; the platform's projects serve any host (global.okdp.policy.privileged), %s is not one of them", at(p), namespace)
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
		}
		return nil
	})
}
