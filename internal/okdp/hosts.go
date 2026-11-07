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
	"maps"
	"slices"
	"strings"

	"github.com/okdp/okdp-compiler/internal/value"
)

// hostRules: the hosts a project may serve (checkIngressHosts).
type hostRules struct {
	namespace string
	suffix    string   // global.okdp.ingress.suffix, "" when unset
	listed    []string // global.okdp.policy.ingressHosts of namespace, lower case
}

// newHostRules: the rules of the project of namespace, of okdp, the
// platform's settings.
func newHostRules(okdp M, namespace string) *hostRules {
	r := &hostRules{namespace: namespace, suffix: strings.ToLower(strings.Trim(value.Str(value.Dig(okdp, "ingress.suffix")), "."))}
	for _, h := range value.AsList(value.AsMap(value.Dig(okdp, "policy.ingressHosts"))[namespace]) {
		r.listed = append(r.listed, strings.ToLower(value.Str(h)))
	}
	return r
}

// allowed: whether host may be served by the project: a host the platform
// lists for it, or one that ends with -<namespace>.<suffix> (a name before
// the dash). The case and a final dot do not count.
func (r *hostRules) allowed(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if slices.Contains(r.listed, host) {
		return true
	}
	if r.suffix == "" {
		return false
	}
	rest, ok := strings.CutSuffix(host, "-"+r.namespace+"."+r.suffix)
	return ok && rest != "" && !strings.HasSuffix(rest, ".")
}

// refusal: the error of the host host, found at where, which the project
// may not serve.
func (r *hostRules) refusal(where, host string) error {
	form := "a host that ends with -" + r.namespace + "." + r.suffix
	if r.suffix == "" {
		form = "any host, global.okdp.ingress.suffix being unset, but those listed"
	}
	return fmt.Errorf("%s: host %q is not served by %s: %s, or one listed for it in global.okdp.policy.ingressHosts; the platform's projects serve any host (global.okdp.policy.privileged), %s is not one of them", where, host, r.namespace, form, r.namespace)
}

// hostAnnotations: the annotations that name hosts, an Ingress's or a
// Service's: cert-manager's common name and alternative names, ingress-nginx's
// server aliases, external-dns's hostnames; their values lists of hosts
// separated by commas and spaces.
var hostAnnotations = map[string]bool{
	"cert-manager.io/common-name":               true,
	"cert-manager.io/alt-names":                 true,
	"nginx.ingress.kubernetes.io/server-alias":  true,
	"external-dns.alpha.kubernetes.io/hostname": true,
}

// annotationHosts: the first host of the annotations a (a map of
// annotations) that the rules refuse, by annotation key order; nil when
// they all hold.
func (r *hostRules) annotationHosts(p string, a M) error {
	for _, k := range slices.Sorted(maps.Keys(a)) {
		if !hostAnnotations[k] {
			continue
		}
		for _, h := range strings.FieldsFunc(value.Str(a[k]), func(c rune) bool { return c == ',' || c == ' ' || c == '\n' || c == '\t' }) {
			if !r.allowed(h) {
				return r.refusal(value.Key(p, k), h)
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
// route's hostnames, and the annotations that name hosts (hostAnnotations)
// of any object. The first refused, in order, fails, named <kind>/<name>,
// its field named.
func checkIngressHosts(svc, namespace string, okdp M, objects []M) error {
	r := newHostRules(okdp, namespace)
	return eachObject(objects, func(o M) error {
		kind, group, name := value.Str(o["kind"]), apiGroup(value.Str(o["apiVersion"])), value.Str(value.Dig(o, "metadata.name"))
		at := func(p string) string { return fmt.Sprintf("%s: objects: %s/%s: %s", svc, kind, name, p) }
		if err := r.annotationHosts("metadata.annotations", value.AsMap(value.Dig(o, "metadata.annotations"))); err != nil {
			return fmt.Errorf("%s: objects: %s/%s: %w", svc, kind, name, err)
		}
		host := func(p string, h any) error {
			if s := value.Str(h); !r.allowed(s) {
				return r.refusal(at(p), s)
			}
			return nil
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

// ingressKeys: the keys of the values of a chart that hold its ingress.
var ingressKeys = map[string]bool{"ingress": true, "ingresses": true}

// checkIngressValues: the final values of the charts of an instance of
// svc in namespace, one that is not privileged (Result.Values, by chart),
// name no host the platform keeps from the project (hostRules) under a key
// ingress (ingresses) at any depth: a host, hostname or commonName, the
// strings of hosts, hostnames, dnsNames and extraHosts, the name of an
// extraHosts item, and the annotations that name hosts (hostAnnotations).
// The values are the package's computed ones and the instance's upstream
// values over them: the upstream chart's own defaults are not seen. The
// first refused, charts in name order, keys sorted, fails, named
// <chart>: <path>.
func checkIngressValues(namespace string, okdp M, values map[string]map[string]any) error {
	r := newHostRules(okdp, namespace)
	for _, chart := range slices.Sorted(maps.Keys(values)) {
		var walk func(v any, p, key string, in bool) error
		check := func(p string, h any) error {
			if s, ok := h.(string); ok && s != "" && !r.allowed(s) {
				return r.refusal(chart+": "+p, s)
			}
			return nil
		}
		walk = func(v any, p, key string, in bool) error {
			switch t := v.(type) {
			case M:
				if in {
					if err := r.annotationHosts(value.Key(p, "annotations"), value.AsMap(t["annotations"])); err != nil {
						return fmt.Errorf("%s: %w", chart, err)
					}
				}
				for _, k := range slices.Sorted(maps.Keys(t)) {
					kp := value.Key(p, k)
					if in {
						switch k {
						case "host", "hostname", "commonName":
							if err := check(kp, t[k]); err != nil {
								return err
							}
						case "name":
							if key == "extraHosts" {
								if err := check(kp, t[k]); err != nil {
									return err
								}
							}
						case "hosts", "hostnames", "dnsNames", "extraHosts":
							for i, h := range value.AsList(t[k]) {
								if err := check(value.Index(kp, i), h); err != nil {
									return err
								}
							}
							if err := check(kp, t[k]); err != nil {
								return err
							}
						}
					}
					if err := walk(t[k], kp, k, in || ingressKeys[k]); err != nil {
						return err
					}
				}
			case []any:
				for i, x := range t {
					if err := walk(x, value.Index(p, i), key, in); err != nil {
						return err
					}
				}
			}
			return nil
		}
		if err := walk(values[chart], "", "", false); err != nil {
			return err
		}
	}
	return nil
}
