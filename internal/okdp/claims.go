// Claims: the connections a provider's instance provisions, one per claim
// bound to it. The mapping reads them and publishes an output for each.

package okdp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/okdp/okdp-compiler/chart"
	"github.com/okdp/okdp-compiler/value"
)

// Claim is a connection claimed of a provider. The provider's instance
// provisions it and publishes it as an output of Contract named ID.
type Claim struct {
	// ID is the connection's name: <namespace>.<name> for a named claim,
	// <namespace>.<instance>.<field> for a claim template.
	ID        string `json:"id"`
	Contract  string `json:"contract"`  // a contract the package provisions
	Namespace string `json:"namespace"` // the namespace of the instances that claim it
	// Name is the name of a named claim (claims.<name> of the context), whose
	// references in Namespace all get this one connection; "" for a template.
	Name string `json:"name,omitempty"`
	// Request is what the claim requests, with the claim schema's defaults.
	Request map[string]any `json:"request,omitempty"`
}

// Binding is the provider that the claim of a reference is bound to
// (CompileAll), and what it publishes for the claim.
type Binding struct {
	Namespace, Release string // the provider's instance
	Contract           string // the claim's contract, which is the output's
	ID                 string // the claim's ID, which names the output
	// Output is what the provider publishes for the claim; nil until it compiles.
	Output *Output
}

// ClaimSpec is a claim as written: a claim template, an object of the
// parameters in place of a connection's name, or a named claim of the
// context (claims.<name>).
type ClaimSpec struct {
	// Name is the key of a named claim in claims; "" for a claim template.
	Name string `json:"-" yaml:"-"`
	// Contract is set for a named claim only: a template has its reference's.
	Contract string `json:"contract,omitempty" yaml:"contract,omitempty"`
	// Class names a class of global.okdp.provisioning.<contract>.classes;
	// "" means the default class, or that the claim names a provider.
	Class string `json:"class,omitempty" yaml:"class,omitempty"`
	// Provider names the instance that provisions the claim, by its name or
	// its release: <name> in the namespace, or <namespace>/<name>; "" for none.
	Provider string `json:"provider,omitempty" yaml:"provider,omitempty"`
	// Request is what the claim requests; nil for nothing.
	Request M `json:"request,omitempty" yaml:"request,omitempty"`
}

// parseClaim reads the claim m, written at path. A named claim requires a
// contract; a claim template may not set one, because it has the contract
// of its reference. class and provider exclude each other, and a claim
// that names neither takes the default class of its contract: {} thus
// claims a connection of its own, of the default class. The claim is
// returned even when it fails, as ParseClaim says.
func parseClaim(m M, path string, named bool) (ClaimSpec, error) {
	c := ClaimSpec{Contract: value.Str(m["contract"]), Class: value.Str(m["class"]), Provider: value.Str(m["provider"])}
	if r, ok := m["request"].(M); ok {
		c.Request = r
	}
	keys, allowed := []string{"class", "provider", "request"}, "class, provider and request only"
	if named {
		keys, allowed = []string{"class", "contract", "provider", "request"}, "contract, class, provider and request only"
	}
	for _, k := range slices.Sorted(maps.Keys(m)) {
		if !slices.Contains(keys, k) {
			return c, fmt.Errorf("%s: claim: unknown key %q: %s", path, k, allowed)
		}
		if _, ok := m[k].(M); k == "request" && !ok && m[k] != nil {
			return c, fmt.Errorf("%s: claim: request must be an object, got %s", path, value.ShortAt(k, m[k]))
		}
		if _, ok := m[k].(string); k != "request" && !ok && m[k] != nil {
			return c, fmt.Errorf("%s: claim: %s must be a string, got %s", path, k, value.ShortAt(k, m[k]))
		}
	}
	switch {
	case named && c.Contract == "":
		return c, fmt.Errorf("%s: claim: contract is required, the contract of the connection it claims", path)
	case named && !chart.IsFileName(c.Contract):
		return c, fmt.Errorf("%s: claim: contract %q is not a contract name", path, c.Contract)
	}
	if c.Provider != "" {
		if err := CheckProvider(path+": claim", c.Provider); err != nil {
			return c, err
		}
	}
	if c.Class != "" && c.Provider != "" {
		return c, fmt.Errorf("%s: claim: class or provider, not both", path)
	}
	return c, nil
}

// namedClaims returns the named claims of the merged values of an
// instance, by name. They are its context's: its own values set none.
// Every problem is reported, in name order, with the claims that hold.
func namedClaims(values M) (map[string]ClaimSpec, error) {
	v, set := values["claims"]
	if !set {
		return nil, nil
	}
	m, ok := v.(M)
	if !ok {
		return nil, fmt.Errorf("claims: the named claims, by name, got %s", value.ShortAt("claims", v))
	}
	conns := value.AsMap(values["connections"])
	out := map[string]ClaimSpec{}
	var errs []error
	for _, name := range slices.Sorted(maps.Keys(m)) {
		_, isMap := m[name].(M)
		if _, conn := conns[name]; conn && isMap && chart.IsDNSLabel(name) {
			errs = append(errs, atField(value.Key("claims", name), fmt.Errorf("%s: a connection of that name too (%s)", value.Key("claims", name), value.Key("connections", name))))
			continue
		}
		c, err := ParseClaim(name, m[name])
		if err != nil {
			errs = append(errs, atField(value.Key("claims", name), err))
			continue
		}
		out[name] = c
	}
	return out, errors.Join(errs...)
}

// ParseClaim reads the named claim name from v, its entry as written in
// claims. Even when it fails, the claim returned holds the keys of v that
// are of their type, so that a caller can list it.
func ParseClaim(name string, v any) (ClaimSpec, error) {
	where := value.Key("claims", name)
	decl, ok := v.(M)
	c, err := parseClaim(decl, where, true)
	c.Name = name
	switch {
	case !chart.IsDNSLabel(name):
		return c, fmt.Errorf("%s: claim: its name is a DNS-1123 label", where)
	case !ok:
		return c, fmt.Errorf("%s: a claim, an object, got %s", where, value.ShortAt(name, v))
	}
	return c, err
}

const providerForms = "<instance>, <release>, <namespace>/<instance> or <namespace>/<release>"

// providerName splits s, the provider a claim or a class names, into the
// namespace and the name of an instance. A bare <name> is of namespace.
func providerName(s, namespace string) (string, string, bool) {
	ns, name, qualified := strings.Cut(s, "/")
	if !qualified {
		ns, name = namespace, ns
	}
	return ns, name, (!qualified || chart.IsDNSLabel(ns)) && chart.IsRelease(name)
}

// checkProvisions checks the contracts a package provisions (okdp.yaml):
// each is a contract name, listed once, with valid claimers.
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

// CheckClaimers checks claimers, the namespaces that may claim of a
// provider from another namespace: each is a namespace name or "*", listed
// once. name is what the error calls the list.
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

func consents(claimers []string, namespace string) bool {
	return slices.Contains(claimers, "*") || slices.Contains(claimers, namespace)
}

// checkClaims checks the claims bound to an instance of svc: each has a
// claim ID that starts with its claimers' namespace, is of a contract the
// package provisions and appears once. Every claim that fails is reported.
func checkClaims(svc *Service, claims []Claim) error {
	var errs []error
	for i, c := range claims {
		_, err := value.Normalize(c.Request)
		switch where := fmt.Sprintf("Inputs.Claims[%d] (%s)", i, c.ID); {
		case !claimIDForm(c.ID):
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

// claimed checks that outputs hold, for each claim, an output of its
// contract named after its ID.
func claimed(claims []Claim, outputs []Output) error {
	var errs []error
	for _, c := range claims {
		if !slices.ContainsFunc(outputs, func(o Output) bool { return o.Contract == c.Contract && o.Name == c.ID }) {
			errs = append(errs, fmt.Errorf("claim %q (%s): provides publishes no %s output of that name", c.ID, c.Contract, c.Contract))
		}
	}
	return errors.Join(errs...)
}

// claimsScope returns the claims as the mapping sees them. name is "" for
// a claim template, and request is {} when the claim has none.
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

// claimRef is a claim at field: of a reference of an instance, at its
// parameter path, or a named claim, at claims.<name>. name is "" for a
// claim template, whose spec holds the instance's request merged over the
// one the package fixes.
type claimRef struct {
	field, contract, name string
	spec                  ClaimSpec
}

// instanceClaims is what CompileAll binds of an instance (claimsOf).
type instanceClaims struct {
	refs  []claimRef           // the claims of its references
	named map[string]ClaimSpec // the named claims of its context
	// classes are the classes its context defines, by contract:
	//
	//	{default, classes: {<name>: {provider, request}}}
	classes  M
	platform M // those of them the platform defines (level 0)
}

// platformClasses returns global.okdp.provisioning merged from the layers
// of level 0 alone. A class of the platform routes every namespace to its
// provider, and needs no consent from it. A layer that does not normalize
// is skipped: valuesLayers has already reported it.
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

// claimsOf returns what CompileAll binds of an instance of def: the
// claims of its references, in parameter path order, the named claims of
// its context, and its classes, which come from the context's layers
// alone. A claim template carries the instance's request merged over what
// the package fixes. It returns nothing when the parameters, the platform
// values or a claim fail: the compile says why.
func claimsOf(ctx context.Context, def *Definition, in Inputs) instanceClaims {
	svc, err := def.service()
	if err != nil {
		return instanceClaims{}
	}
	schema, values, settings, err := in.memo.paramsOf().get(ctx, def.Files, in, svc)
	if err != nil {
		return instanceClaims{}
	}
	named, err := namedClaims(values)
	if err != nil {
		return instanceClaims{}
	}
	var out []claimRef
	refs(schema.Doc, value.Copy(values), "", "", func(ref M, v any, path, _ string) any {
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
				out = append(out, claimRef{path, contract, "", spec})
			}
		case string:
			if spec, ok := named[t]; ok && spec.Contract == contract {
				out = append(out, claimRef{path, contract, t, spec})
			}
		}
		return v
	})
	return instanceClaims{out, named, value.AsMap(settings["provisioning"]), platformClasses(in.Context)}
}

// claimID builds the ID of a claim from its namespace and its parts:
//
//	<namespace>.<name>              a named claim: claimID(namespace, name)
//	<namespace>.<instance>.<field>  the claim template at field: claimID(namespace, instance, field)
//
// Each part is made a DNS-1123 label, so no part holds a dot: two sources
// give two IDs, and the dots tell the kind.
//
// An ID longer than 63 characters is shortened: the namespace is kept
// whole, the rest is cut, and 8 hexadecimal digits of the SHA-256 of the
// whole ID end it. A namespace too long to keep its prefix gives an ID cut
// whole, which checkClaims refuses.
func claimID(namespace string, parts ...string) string {
	parts = append([]string{namespace}, parts...)
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
		suffix := hex.EncodeToString(sum[:4])
		ns, rest := parts[0]+".", id[len(parts[0])+1:]
		switch keep := 54 - len(ns); {
		case keep > 0:
			if cut := strings.TrimRight(rest[:keep], "-."); cut != "" {
				return ns + cut + "-" + suffix
			}
			return ns + suffix
		case len(ns)+len(suffix) <= 63:
			return ns + suffix
		}
		id = strings.TrimRight(id[:54], "-.") + "-" + suffix
	}
	return id
}

// CheckDeclarations checks the declarations of a context that has no
// instance, a project's (in.Namespace, in.Contracts, in.Context), as
// CompileAll checks those of an instance's context: the layers, the
// platform's settings, every connection and every named claim. The
// provider of a claim is not looked up: a claim of a project without
// instance is bound to no provider until an instance's context declares it.
func CheckDeclarations(ctx context.Context, in Inputs) error {
	layers, platform, err := contextLayers(ctx, in.rules(), in.Context)
	if err != nil {
		return err
	}
	settings, err := platformSettings(in.rules(), platform)
	if err != nil {
		return err
	}
	if err := upstreamGlobals(ctx, in, layers, settings); err != nil {
		return err
	}
	if err := checkDeclared(in, layers, platform, nil); err != nil {
		return err
	}
	merged := M{}
	for _, l := range layers {
		merged = value.Merge(merged, value.AsMap(value.Copy(l)))
	}
	// The claims that fail are reported first, one error each.
	named, err := namedClaims(merged)
	var errs []error
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		errs = j.Unwrap()
	} else {
		errs = append(errs, err)
	}
	classes := value.AsMap(settings["provisioning"])
	for _, name := range slices.Sorted(maps.Keys(named)) {
		spec, where := named[name], value.Key("claims", name)
		site := claimSite{in.Namespace, claimRef{where, spec.Contract, name, spec}}
		ch, _, _, err := claimClass(classes, site)
		if err == nil {
			_, err = claimRequest(in, site, ch)
		}
		if err != nil {
			errs = append(errs, atField(where, err))
		}
	}
	return errors.Join(errs...)
}
