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
	ID        string         `json:"id"`                // the connection's name, DNS-1123 labels joined by dots: <namespace>.<name>, or <namespace>.<instance>.<field>
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

// ClaimSpec: a claim, as written (parseClaim): an object of the
// parameters in place of a connection's name, a claim template, or a
// named claim of the context (claims.<name>, ParseClaim). Its tags are
// the keys it is written with.
type ClaimSpec struct {
	Name     string `json:"-" yaml:"-"`                                   // a named claim's name, its key in claims; "" for a claim template
	Contract string `json:"contract,omitempty" yaml:"contract,omitempty"` // a named claim's contract; "" for a claim template, of its reference's
	Class    string `json:"class,omitempty" yaml:"class,omitempty"`       // the class named, of global.okdp.provisioning.<contract>.classes; "" for the default one, or a provider named
	Provider string `json:"provider,omitempty" yaml:"provider,omitempty"` // the provider named, <name> of the namespace or <namespace>/<name>, an instance's name or its release; "" when none
	Request  M      `json:"request,omitempty" yaml:"request,omitempty"`   // what it requests (request), nil when nothing
}

// parseClaim: the claim m at path. A claim template, a reference's: {} a
// connection of its own, of the default class of its contract; class, the
// class it names; provider, the instance that provisions it, <name> of
// the namespace or <namespace>/<name>, the instance's name or its
// release, not with a class; request, what it requests of it, an object
// the contract's claim schema checks at binding. A named claim (named),
// claims.<name> of the context: contract, required, a contract name,
// class, provider and request. Any other key fails, and so does a value
// that is not a string of that form, or a request that is not an object.
func parseClaim(m M, path string, named bool) (*ClaimSpec, error) {
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
	c := &ClaimSpec{Contract: value.Str(m["contract"]), Class: value.Str(m["class"]), Provider: value.Str(m["provider"])}
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
		return nil, fmt.Errorf("%s: claim: provider %q: %s", path, c.Provider, providerForms)
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
func namedClaims(values M) (map[string]ClaimSpec, error) {
	v, set := values["claims"]
	if !set {
		return nil, nil
	}
	m, ok := v.(M)
	if !ok {
		return nil, fmt.Errorf("claims: the named claims, by name, got %s", value.Short(v))
	}
	conns := value.AsMap(values["connections"])
	out := map[string]ClaimSpec{}
	var errs []error
	for _, name := range slices.Sorted(maps.Keys(m)) {
		_, isMap := m[name].(M)
		if _, conn := conns[name]; conn && isMap && chart.IsDNSLabel(name) {
			errs = append(errs, fmt.Errorf("%s: a connection of that name too (%s)", value.Key("claims", name), value.Key("connections", name)))
			continue
		}
		c, err := ParseClaim(name, m[name])
		if err != nil {
			errs = append(errs, err)
			continue
		}
		out[name] = c
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return out, nil
}

// ParseClaim: the named claim name, v, as written in claims: a name that
// is a DNS-1123 label, an object of the form parseClaim reads. The claim
// holds what v does, each key that is a string of its type, even when it
// fails: a caller lists a claim that does not compile.
func ParseClaim(name string, v any) (ClaimSpec, error) {
	where := value.Key("claims", name)
	decl, ok := v.(M)
	c := ClaimSpec{Name: name, Contract: value.Str(decl["contract"]), Class: value.Str(decl["class"]), Provider: value.Str(decl["provider"])}
	if r, isMap := decl["request"].(M); isMap {
		c.Request = r
	}
	switch {
	case !chart.IsDNSLabel(name):
		return c, fmt.Errorf("%s: claim: its name is a DNS-1123 label", where)
	case !ok:
		return c, fmt.Errorf("%s: a claim, an object, got %s", where, value.Short(v))
	}
	parsed, err := parseClaim(decl, where, true)
	if err != nil {
		return c, err
	}
	parsed.Name = name
	return *parsed, nil
}

// providerForms: what a provider may be, in the messages of one that is
// not of the form (providerName).
const providerForms = "<instance>, <release>, <namespace>/<instance> or <namespace>/<release>"

// providerName: the namespace and the name of the instance a provider
// names, a claim's or a class's: <namespace>/<name>, or <name> of
// namespace, the name an instance's name or its release (tree.provider);
// false when s is not of that form.
func providerName(s, namespace string) (string, string, bool) {
	ns, name, qualified := strings.Cut(s, "/")
	if !qualified {
		ns, name = namespace, ns
	}
	return ns, name, (!qualified || chart.IsDNSLabel(ns)) && chart.IsRelease(name)
}

// checkProvisions: the contracts a package provisions (okdp.yaml's
// provisions), each a contract name, once, its claimers of the form
// CheckClaimers reads.
func checkProvisions(provisions []Provision) error {
	for i, p := range provisions {
		switch {
		case !chart.IsFileName(p.Contract):
			return fmt.Errorf("okdp.yaml: provisions[%d]: %q is not a contract name", i, p.Contract)
		case slices.IndexFunc(provisions, func(o Provision) bool { return o.Contract == p.Contract }) < i:
			return fmt.Errorf("okdp.yaml: provisions: %s is listed twice", p.Contract)
		}
		if err := CheckClaimers(fmt.Sprintf("okdp.yaml: provisions[%d] (%s): claimers", i, p.Contract), p.Claimers); err != nil {
			return err
		}
	}
	return nil
}

// CheckClaimers: claimers, the namespaces whose instances may claim of a
// provider across namespaces (Inputs.Claimers, a provisions entry's), at
// name in the message: each a namespace name, a DNS-1123 label, or "*",
// any namespace, once; the first that is not.
func CheckClaimers(name string, claimers []string) error {
	for i, c := range claimers {
		switch {
		case c != "*" && !chart.IsDNSLabel(c):
			return fmt.Errorf("%s[%d]: %q is not a namespace name, nor \"*\"", name, i, c)
		case slices.Index(claimers, c) < i:
			return fmt.Errorf("%s: %s is listed twice", name, c)
		}
	}
	return nil
}

// consents: whether claimers, those of a provider, let the instances of
// namespace claim of it: it is listed, or "*" is.
func consents(claimers []string, namespace string) bool {
	return slices.Contains(claimers, "*") || slices.Contains(claimers, namespace)
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
	spec                  ClaimSpec
}

// instanceClaims: what CompileAll binds of an instance (claimsOf): the
// claims of its references, the named claims of its context, the classes
// its context defines, global.okdp.provisioning, and those of them the
// platform defines, at level 0.
type instanceClaims struct {
	refs     []claimRef
	named    map[string]ClaimSpec
	classes  M // by contract: {default, classes: {<name>: {provider, request}}}, of the form the platform schema checks
	platform M // the same, of the layers of level 0 alone (platformClasses)
}

// platformClasses: global.okdp.provisioning of the layers of level 0 of
// layers, merged: the platform's classes, which route every namespace to
// their provider, with its consent given (tree.consent). A layer that
// values cannot hold gives none: the layers failed before (valuesLayers).
func platformClasses(layers []ContextLayer) M {
	out := M{}
	for _, l := range layers {
		v, err := value.Normalize(l.Values)
		if l.Level != 0 || err != nil {
			continue
		}
		out = value.Merge(out, value.AsMap(value.Dig(value.AsMap(v), "global.okdp.provisioning")))
	}
	return out
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
	return instanceClaims{out, named, value.AsMap(value.Dig(values, "global.okdp.provisioning")), platformClasses(in.Context)}
}

// claimID: the ID of a claim of the reference at field of the instance
// in namespace (Instance.Name, its release when it has none), or of the
// named claim name: <namespace>.<name>, or
// <namespace>.<instance>.<field>, each part a DNS-1123 label (lowercased,
// every run of other characters than letters and digits one "-", trimmed),
// the parts joined by dots, which no part holds: two sources give two IDs,
// and the dots tell the kind. Longer than 63 characters (chart.IsClaimID):
// cut, and ended with 8 hexadecimal digits of its SHA-256.
func claimID(namespace, instance, field, name string) string {
	parts := []string{namespace, name}
	if name == "" {
		parts = []string{namespace, instance, field}
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
