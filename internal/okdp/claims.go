// Claims: the connections a provider's instance provisions, one per claim
// bound to it (Inputs.Claims): the mapping reads them, and publishes an
// output for each.

package okdp

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/okdp/okdp-compiler/internal/chart"
	"github.com/okdp/okdp-compiler/internal/value"
)

// Claim: a connection claimed of a provider, which its package provisions
// (provisions): the instance provisions it and publishes it, an output of
// Contract named ID.
type Claim struct {
	ID        string         `json:"id"`                // the connection's name, DNS-1123 labels joined by dots: <namespace>.<name>, or <namespace>.<release>.<field>
	Contract  string         `json:"contract"`          // a contract the package provisions
	Namespace string         `json:"namespace"`         // the namespace of the instances that claim it
	Name      string         `json:"name,omitempty"`    // a named claim's name, claims.<name> of the context, the connection of every reference to it in Namespace; "" for a claim template's
	Request   map[string]any `json:"request,omitempty"` // what the claim requests, as the contract's claim schema (x-okdp-claim) allows, its defaults applied
}

// Binding: the provider a reference's claim is bound to (CompileAll), and
// what it publishes for the claim.
type Binding struct {
	Namespace, Release string  // the provider's instance
	Contract           string  // the claim's contract: the one the output is of
	ID                 string  // the claim's ID: the name of the output it publishes for it
	Output             *Output // that output, nil when it is not known yet (the provider not compiled)
}

// claimSpec: a claim, as written (parseClaim): an object of the
// parameters in place of a connection's name, a claim template, or a
// named claim of the context (claims.<name>).
type claimSpec struct {
	Contract string // a named claim's contract; "" for a claim template, of its reference's
	Class    string // the class named, of global.okdp.provisioning.<contract>.classes; "" for the default one, or a provider named
	Provider string // the provider named, <release> of the namespace or <namespace>/<release>; "" when none
	Request  M      // what it requests (request), nil when nothing
}

// parseClaim: the claim m at path. A claim template, a reference's: {} a
// connection of its own, of the default class of its contract; class, the
// class it names; provider, the instance that provisions it, <release> of
// the namespace or <namespace>/<release>, not with a class; request, what
// it requests of it, an object the contract's claim schema checks at
// binding. A named claim (named), claims.<name> of the context: contract,
// required, a contract name, class, provider and request. Any other key
// fails, and so does a value that is not a string of that form, or a
// request that is not an object.
func parseClaim(m M, path string, named bool) (*claimSpec, error) {
	keys, allowed := []string{"class", "provider", "request"}, "class, provider and request only"
	if named {
		keys, allowed = []string{"class", "contract", "provider", "request"}, "contract, class, provider and request only"
	}
	for _, k := range slices.Sorted(maps.Keys(m)) {
		if !slices.Contains(keys, k) {
			return nil, fmt.Errorf("%s: claim: unknown key %q: %s", path, k, allowed)
		}
		if _, ok := m[k].(M); k == "request" && !ok && m[k] != nil {
			return nil, fmt.Errorf("%s: claim: request must be an object, got %s", path, value.Short(m[k]))
		}
		if _, ok := m[k].(string); k != "request" && !ok && m[k] != nil {
			return nil, fmt.Errorf("%s: claim: %s must be a string, got %s", path, k, value.Short(m[k]))
		}
	}
	c := &claimSpec{Contract: value.Str(m["contract"]), Class: value.Str(m["class"]), Provider: value.Str(m["provider"])}
	if r, ok := m["request"].(M); ok {
		c.Request = r
	}
	switch {
	case named && c.Contract == "":
		return nil, fmt.Errorf("%s: claim: contract is required, the contract of the connection it claims", path)
	case named && !chart.IsFileName(c.Contract):
		return nil, fmt.Errorf("%s: claim: contract %q is not a contract name", path, c.Contract)
	}
	if _, _, ok := providerName(c.Provider, ""); c.Provider != "" && !ok {
		return nil, fmt.Errorf("%s: claim: provider %q: <release> or <namespace>/<release>", path, c.Provider)
	}
	if c.Class != "" && c.Provider != "" {
		return nil, fmt.Errorf("%s: claim: class or provider, not both", path)
	}
	return c, nil
}

// namedClaims: the named claims of the merged values of an instance,
// claims of its context (valuesLayers: never the instance's own values),
// by name: each name a DNS-1123 label and no connection's, each claim of
// the form parseClaim reads. Every problem, one line each, in name order.
func namedClaims(values M) (map[string]claimSpec, error) {
	v, set := values["claims"]
	if !set {
		return nil, nil
	}
	m, ok := v.(M)
	if !ok {
		return nil, fmt.Errorf("claims: the named claims, by name, got %s", value.Short(v))
	}
	conns := value.AsMap(values["connections"])
	out := map[string]claimSpec{}
	var errs []error
	for _, name := range slices.Sorted(maps.Keys(m)) {
		where := value.Key("claims", name)
		decl, ok := m[name].(M)
		switch _, conn := conns[name]; {
		case !chart.IsDNSLabel(name):
			errs = append(errs, fmt.Errorf("%s: claim: its name is a DNS-1123 label", where))
		case !ok:
			errs = append(errs, fmt.Errorf("%s: a claim, an object, got %s", where, value.Short(m[name])))
		case conn:
			errs = append(errs, fmt.Errorf("%s: a connection of that name too (%s)", where, value.Key("connections", name)))
		default:
			c, err := parseClaim(decl, where, true)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			out[name] = *c
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return out, nil
}

// providerName: the instance a provider names, a claim's or a class's:
// <namespace>/<release>, or <release> of namespace; false when s is not of
// that form.
func providerName(s, namespace string) (string, string, bool) {
	ns, release, qualified := strings.Cut(s, "/")
	if !qualified {
		ns, release = namespace, ns
	}
	return ns, release, (!qualified || chart.IsDNSLabel(ns)) && chart.IsRelease(release)
}

// checkProvisions: the contracts a package provisions (okdp.yaml's
// provisions), each a contract name, once.
func checkProvisions(provisions []string) error {
	for i, c := range provisions {
		switch {
		case !chart.IsFileName(c):
			return fmt.Errorf("okdp.yaml: provisions[%d]: %q is not a contract name", i, c)
		case slices.Index(provisions, c) < i:
			return fmt.Errorf("okdp.yaml: provisions: %s is listed twice", c)
		}
	}
	return nil
}

// checkClaims: the claims bound to an instance of svc: each a claim ID
// (chart.IsClaimID) starting with the namespace of its claimers, a
// DNS-1123 label, of a contract the package provisions, once, its request
// what values hold (value.Normalize).
func checkClaims(svc *Service, claims []Claim) error {
	var errs []error
	for i, c := range claims {
		_, err := value.Normalize(c.Request)
		switch where := fmt.Sprintf("Inputs.Claims[%d] (%s)", i, c.ID); {
		case !chart.IsClaimID(c.ID):
			errs = append(errs, fmt.Errorf("%s: the ID is a claim ID, DNS-1123 labels joined by dots, 63 characters at most", where))
		case !chart.IsDNSLabel(c.Namespace):
			errs = append(errs, fmt.Errorf("%s: the namespace is a DNS-1123 label", where))
		case !strings.HasPrefix(c.ID, c.Namespace+"."):
			errs = append(errs, fmt.Errorf("%s: the ID starts with the claimers' namespace, %q", where, c.Namespace+"."))
		case !slices.Contains(svc.Provisions, c.Contract):
			errs = append(errs, fmt.Errorf("%s: the package provisions no %s connection (okdp.yaml's provisions)", where, c.Contract))
		case slices.IndexFunc(claims, func(o Claim) bool { return o.ID == c.ID }) < i:
			errs = append(errs, fmt.Errorf("%s: claimed twice", where))
		case err != nil:
			errs = append(errs, fmt.Errorf("%s: request: %w", where, err))
		}
	}
	return errors.Join(errs...)
}

// claimed: nil when outputs hold, for each claim, an output of its
// contract named its ID; else an error per claim without one, in order.
func claimed(claims []Claim, outputs []Output) error {
	var errs []error
	for _, c := range claims {
		if !slices.ContainsFunc(outputs, func(o Output) bool { return o.Contract == c.Contract && o.Name == c.ID }) {
			errs = append(errs, fmt.Errorf("claim %q (%s): provides publishes no %s output of that name", c.ID, c.Contract, c.Contract))
		}
	}
	return errors.Join(errs...)
}

// claimsScope: the claims as the mapping sees them (claims): {id,
// contract, namespace, name, request}, name "" for a claim template's,
// request {} when it has none; checked (checkClaims).
func claimsScope(claims []Claim) []any {
	out := []any{}
	for _, c := range claims {
		request, _ := value.Normalize(c.Request)
		if request == nil {
			request = M{}
		}
		out = append(out, M{"id": c.ID, "contract": c.Contract, "namespace": c.Namespace, "name": c.Name, "request": request})
	}
	return out
}

// claimRef: a claim of a reference of an instance, as written: its
// parameter path and its contract; the claim template (spec, its request
// the instance's merged over the package's), or the named claim of the
// context it names (name, spec its declaration).
type claimRef struct {
	field, contract, name string
	spec                  claimSpec
}

// instanceClaims: what CompileAll binds of an instance (claimsOf): the
// claims of its references, the named claims of its context, and the
// classes its context defines, global.okdp.provisioning.
type instanceClaims struct {
	refs    []claimRef
	named   map[string]claimSpec
	classes M // by contract: {default, classes: {<name>: {provider, request}}}, of the form the platform schema checks
}

// claimsOf: the claims of the references of an instance of def, in
// parameter path order: its parameters (stage 1), each map of a reference
// that takes claims, its request the instance's merged over what the
// package fixes (x-okdp-connection-ref's request), and each name of a
// named claim of its contract that is no connection; the named claims of
// its context (namedClaims); and its classes, global.okdp.provisioning of
// the same merged values, the context's (valuesLayers: never the
// instance's).
// None when the parameters, the platform values (platformSchema) or a
// claim fail: the compile says why.
func claimsOf(def *Definition, in Inputs) instanceClaims {
	svc, err := def.service()
	if err != nil {
		return instanceClaims{}
	}
	schema, values, err := parameters(def.Files, in, svc)
	if err != nil {
		return instanceClaims{}
	}
	named, err := namedClaims(values)
	if err != nil {
		return instanceClaims{}
	}
	if _, err := schemaValues(platformSchema, "", M{"global": value.Copy(values["global"])}); err != nil {
		return instanceClaims{}
	}
	var out []claimRef
	refs(schema.Doc, value.Copy(values), "", func(ref M, v any, path string) any {
		if !value.Truthy(ref["provision"]) {
			return v
		}
		contract := value.Str(ref["contract"])
		switch t := v.(type) {
		case M:
			if spec, err := parseClaim(t, path, false); err == nil {
				if fixed, ok := ref["request"].(M); ok {
					spec.Request = value.Merge(value.AsMap(value.Copy(fixed)), spec.Request)
				}
				out = append(out, claimRef{path, contract, "", *spec})
			}
		case string:
			if spec, ok := named[t]; ok && spec.Contract == contract {
				out = append(out, claimRef{path, contract, t, spec})
			}
		}
		return v
	})
	return instanceClaims{out, named, value.AsMap(value.Dig(values, "global.okdp.provisioning"))}
}

// claimID: the ID of a claim of the reference at field of release in
// namespace, or of the named claim name: <namespace>.<name>, or
// <namespace>.<release>.<field>, each part a DNS-1123 label (lowercased,
// every run of other characters than letters and digits one "-", trimmed),
// the parts joined by dots, which no part holds: two sources give two IDs,
// and the dots tell the kind. Longer than 63 characters (chart.IsClaimID):
// cut, and ended with 8 hexadecimal digits of its SHA-256.
func claimID(namespace, release, field, name string) string {
	parts := []string{namespace, name}
	if name == "" {
		parts = []string{namespace, release, field}
	}
	for i, p := range parts {
		var b strings.Builder
		for _, r := range strings.ToLower(p) {
			if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
				b.WriteRune(r)
			} else if !strings.HasSuffix(b.String(), "-") {
				b.WriteByte('-')
			}
		}
		parts[i] = strings.Trim(b.String(), "-")
	}
	id := strings.Join(parts, ".")
	if len(id) > 63 {
		sum := sha256.Sum256([]byte(id))
		id = strings.TrimRight(id[:54], "-.") + "-" + hex.EncodeToString(sum[:4])
	}
	return id
}
