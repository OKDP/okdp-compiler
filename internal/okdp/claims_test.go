package okdp

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"go.yaml.in/yaml/v3"

	"github.com/okdp/okdp-compiler/internal/value"
)

// provisioner: def, a package that provisions api connections, one output
// per claim, its URL after the claim and the namespace that claims it.
func provisioner(provides string) (*Definition, Inputs) {
	d := *def
	d.Package.Provisions = []Provision{{Contract: "api"}}
	d.Okdp = []byte("provides:\n" + provides)
	in := inputs()
	in.Contracts = httpContract
	return &d, in
}

const claimOutputs = `  - $for: c in claims
    $each: {contract: api, name: '${{ c.id }}', values: {url: 'https://${{ c.id }}.${{ c.namespace }}${{ c.name == "" ? "" : "/" + c.name }}'}}
`

// TestClaims: the mapping reads the claims bound to the instance, in
// order, and publishes an output for each.
func TestClaims(t *testing.T) {
	d, in := provisioner(claimOutputs)
	in.Claims = []Claim{{ID: "demo.web.db", Contract: "api", Namespace: "demo"}, {ID: "data.shared", Contract: "api", Namespace: "data", Name: "shared"}}
	res, err := Compile(context.Background(), d, in)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, o := range res.Info.Provides {
		got = append(got, o.Name+" "+o.Values["url"].(string))
	}
	if want := []string{"demo.web.db https://demo.web.db.demo", "data.shared https://data.shared.data/shared"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
	// What a claim requests: the mapping reads request, {} when none.
	d, in = provisioner("  - $for: c in claims\n    $each: {contract: api, name: '${{ c.id }}', values: {url: 'https://${{ c.id }}/${{ \"size\" in c.request ? c.request.size : \"none\" }}'}}\n")
	in.Claims = []Claim{{ID: "demo.a", Contract: "api", Namespace: "demo", Request: map[string]any{"size": "5Gi"}}, {ID: "demo.b", Contract: "api", Namespace: "demo"}}
	if res, err := Compile(context.Background(), d, in); err != nil || res.Info.Provides[0].Values["url"] != "https://demo.a/5Gi" || res.Info.Provides[1].Values["url"] != "https://demo.b/none" {
		t.Errorf("request: %v %v", res, err)
	}
	// None bound: claims is empty.
	in.Claims = nil
	if res, err := Compile(context.Background(), d, in); err != nil || len(res.Info.Provides) != 0 {
		t.Errorf("no claim: %v %v", res, err)
	}
}

// TestClaimsErrors: a claim the package does not provision, whose ID is
// no claim ID, whose namespace is no label or does not start its ID, given
// twice, whose request values cannot hold; a claim the mapping publishes no
// output for.
func TestClaimsErrors(t *testing.T) {
	d, in := provisioner(claimOutputs)
	claim := func(id, contract string) Claim { return Claim{ID: id, Contract: contract, Namespace: "demo"} }
	in.Claims = []Claim{
		claim("demo.a", "s3"), claim("demo.B", "api"), claim("demo.c", "api"), claim("demo.c", "api"),
		{ID: "demo.d", Contract: "api", Namespace: "demo", Request: map[string]any{"f": func() {}}},
		claim("demo..f", "api"), claim("demo."+strings.Repeat("g", 60), "api"),
		{ID: "Demo.h", Contract: "api", Namespace: "Demo"}, claim("other.i", "api"), claim("demo-j", "api"), {ID: "k", Contract: "api"},
	}
	want := `Inputs.Claims[0] (demo.a): the package provisions no s3 connection (okdp.yaml's provisions)
Inputs.Claims[1] (demo.B): the ID is a claim ID, DNS-1123 labels joined by dots, 63 characters at most
Inputs.Claims[3] (demo.c): claimed twice
Inputs.Claims[4] (demo.d): request: json: cannot marshal from Go func() within "/f"
Inputs.Claims[5] (demo..f): the ID is a claim ID, DNS-1123 labels joined by dots, 63 characters at most
Inputs.Claims[6] (demo.gggggggggggggggggggggggggggggggggggggggggggggggggggggggggggg): the ID is a claim ID, DNS-1123 labels joined by dots, 63 characters at most
Inputs.Claims[7] (Demo.h): the ID is a claim ID, DNS-1123 labels joined by dots, 63 characters at most
Inputs.Claims[8] (other.i): the ID starts with the claimers' namespace, "demo."
Inputs.Claims[9] (demo-j): the ID starts with the claimers' namespace, "demo."
Inputs.Claims[10] (k): the namespace is a DNS-1123 label`
	if _, err := Compile(context.Background(), d, in); err == nil || err.Error() != want {
		t.Errorf("got %v\nwant %s", err, want)
	}
	// A namespace that is no label, its ID a claim ID.
	in.Claims = []Claim{{ID: "demo_x.h", Contract: "api", Namespace: "demo_x"}}
	if _, err := Compile(context.Background(), d, in); err == nil || err.Error() != "Inputs.Claims[0] (demo_x.h): the ID is a claim ID, DNS-1123 labels joined by dots, 63 characters at most" {
		t.Errorf("a namespace that is no label: %v", err)
	}
	d, in = provisioner("  - {contract: api, name: demo.other, values: {url: u}}\n")
	in.Claims = []Claim{claim("demo.a", "api"), claim("demo.other", "api"), claim("demo.b", "api")}
	want = `claim "demo.a" (api): provides publishes no api output of that name
claim "demo.b" (api): provides publishes no api output of that name`
	if _, err := Compile(context.Background(), d, in); err == nil || err.Error() != want {
		t.Errorf("got %v\nwant %s", err, want)
	}
}

// TestProvisions: provisions in okdp.yaml, a list of contracts, each its
// name or {contract, claimers}; one that is no file name, or listed
// twice, a claimer that is no namespace name nor "*", or listed twice,
// fail when the package is loaded, and so does another key or form.
func TestProvisions(t *testing.T) {
	p, err := decodePackage([]byte("provisions: [database-server, {contract: s3, claimers: [demo, '*']}, {contract: api}]\n"))
	want := []Provision{{Contract: "database-server"}, {Contract: "s3", Claimers: []string{"demo", "*"}}, {Contract: "api"}}
	if err != nil || !reflect.DeepEqual(p.Provisions, want) {
		t.Errorf("got %#v (%v)", p.Provisions, err)
	}
	if got := p.claimers("s3"); !reflect.DeepEqual(got, []string{"demo", "*"}) {
		t.Errorf("claimers of s3: got %q", got)
	}
	if got := p.claimers("database-server"); got != nil {
		t.Errorf("claimers of database-server: got %q", got)
	}
	for list, want := range map[string]string{
		"[a/b]":                                  `okdp.yaml: provisions[0]: "a/b" is not a contract name`,
		"[s3, '']":                               `okdp.yaml: provisions[1]: "" is not a contract name`,
		"[s3, s3]":                               "okdp.yaml: provisions: s3 is listed twice",
		"[s3, {contract: s3, claimers: [a]}]":    "okdp.yaml: provisions: s3 is listed twice",
		"[{claimers: [a]}]":                      `okdp.yaml: provisions[0]: "" is not a contract name`,
		"[{contract: s3, claimers: [Demo]}]":     `okdp.yaml: provisions[0] (s3): claimers[0]: "Demo" is not a namespace name, nor "*"`,
		"[{contract: s3, claimers: [a, a]}]":     "okdp.yaml: provisions[0] (s3): claimers: a is listed twice",
		"[{contract: s3, claimers: ['*', '*']}]": `okdp.yaml: provisions[0] (s3): claimers: * is listed twice`,
	} {
		d := *def
		p, err := decodePackage([]byte("provisions: " + list + "\n"))
		if err != nil {
			t.Fatal(err)
		}
		d.Package.Provisions = p.Provisions
		if _, err := d.service(); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v, want %s", list, err, want)
		}
	}
	for list, want := range map[string]string{
		"[{contract: s3, size: 1}]":     `okdp.yaml: provisions (line 1): unknown key "size": contract and claimers only`,
		"[[s3]]":                        "okdp.yaml: provisions (line 1): an entry is a contract name, or {contract, claimers}",
		"[{contract: s3, claimers: a}]": "okdp.yaml: yaml: unmarshal errors:\n  line 1: cannot unmarshal !!str `a` into []string",
	} {
		if _, err := decodePackage([]byte("provisions: " + list + "\n")); err == nil || err.Error() != want {
			t.Errorf("%s: got %v, want %s", list, err, want)
		}
	}
}

// TestInputsClaimers: Inputs.Claimers, each a namespace name or "*", once:
// another fails the compile.
func TestInputsClaimers(t *testing.T) {
	for claimers, want := range map[string]string{
		"demo,*":    "",
		"demo,Demo": `Inputs.Claimers[1]: "Demo" is not a namespace name, nor "*"`,
		"demo,demo": "Inputs.Claimers: demo is listed twice",
	} {
		in := inputs()
		in.Claimers = strings.Split(claimers, ",")
		_, err := Compile(context.Background(), def, in)
		if got := fmt.Sprint(err); want == "" && err != nil || want != "" && got != want {
			t.Errorf("%s: got %v, want %q", claimers, err, want)
		}
	}
}

// claimCtx: the context of the in-memory package with a reference that
// takes claims, db, and one that does not, main; these bindings.
func claimCtx(values M, bindings map[string]Binding) (M, []Dependency, error) {
	inst, err := claimedInstance(values, bindings)
	if err != nil {
		return nil, nil, err
	}
	return value.AsMap(inst.scope["values"]), inst.deps, nil
}

// claimedInstance: the instance of claimCtx.
func claimedInstance(values M, bindings map[string]Binding) (*instance, error) {
	ch := maps.Clone(pkg)
	ch["config.schema.json"] = &fstest.MapFile{Data: []byte(`{"properties": {
		"db": {"type": ["string", "object"], "x-okdp-connection-ref": {"contract": "server", "provision": true}},
		"main": {"x-okdp-connection-ref": {"contract": "database"}}}}`)}
	in := inputs(values)
	in.Contracts, in.Bindings = outputContracts, bindings
	return newInstance(ch, in, defService)
}

// pgOutput: what data/demo-pg publishes for the claim demo.demo-app.db.
var pgOutput = &Output{Name: "demo.demo-app.db", Contract: "server", Values: M{"host": "demo-pg-rw.data.svc", "port": 5433}, SecretRef: &SecretRef{Name: "demo.demo-app.db-credentials"}}

// TestResolveClaim: a claim resolves to the output its binding gives, as
// an output: its values completed, its secretRef; the provider a
// dependency, its namespace when it is another's, and then its
// credentials a copy of the instance's, read from the platform's secret
// store.
func TestResolveClaim(t *testing.T) {
	for name, tc := range map[string]struct {
		namespace, secret string
		okdp              M
		deps              []Dependency
		transfers         []transfer
	}{
		"another namespace": {
			"data", "demo-app-demo.demo-app.db-credentials",
			M{"ref": "demo.demo-app.db", "contract": "server", "source": "claim", "instance": "demo-pg", "namespace": "data"},
			[]Dependency{{Instance: "demo-pg", Namespace: "data", Contract: "server", Field: "db", Output: "demo.demo-app.db"}},
			[]transfer{{secret: "demo-app-demo.demo-app.db-credentials", key: "data/demo.demo-app.db", contract: "server", fields: []string{"password"}}},
		},
		"its namespace": {
			"demo", "demo.demo-app.db-credentials",
			M{"ref": "demo.demo-app.db", "contract": "server", "source": "claim", "instance": "demo-pg"},
			[]Dependency{{Instance: "demo-pg", Contract: "server", Field: "db", Output: "demo.demo-app.db"}},
			nil,
		},
	} {
		t.Run(name, func(t *testing.T) {
			b := map[string]Binding{"db": {Namespace: tc.namespace, Release: "demo-pg", Contract: "server", ID: "demo.demo-app.db", Output: pgOutput}}
			inst, err := claimedInstance(M{"db": M{}, "global": M{"okdp": M{"secrets": M{"store": "okdp-secrets"}}}}, b)
			if err != nil {
				t.Fatal(err)
			}
			got := value.AsMap(inst.scope["values"])["db"]
			want := M{"host": "demo-pg-rw.data.svc", "port": float64(5433), "secretRef": M{"name": tc.secret}, "okdp": tc.okdp}
			if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(inst.deps, tc.deps) || !reflect.DeepEqual(inst.transfers, tc.transfers) {
				t.Errorf("got %#v, %#v, %#v\nwant %#v, %#v, %#v", got, inst.deps, inst.transfers, want, tc.deps, tc.transfers)
			}
		})
	}
}

// TestResolveClaimRequirement: a claim's connection checked against the
// reference's requirement, whatever its provider: the provider of the
// default class of the context, which another class may replace, said; of
// a class it names; or the one it names. The validator prints numbers
// grouped (5,433).
func TestResolveClaimRequirement(t *testing.T) {
	ch := maps.Clone(pkg)
	ch["config.schema.json"] = &fstest.MapFile{Data: []byte(`{"properties": {
		"db": {"type": ["string", "object"], "x-okdp-connection-ref": {"contract": "server", "provision": true, "require": {"properties": {"port": {"maximum": 5432}}}}}}}`)}
	b := map[string]Binding{"db": {Namespace: "demo", Release: "demo-pg", Contract: "server", ID: "demo.demo-app.db", Output: pgOutput}}
	for name, tc := range map[string]struct {
		claim M
		want  string
	}{
		"the default class": {M{}, `db (claim "demo.demo-app.db" of demo/demo-pg): the connection does not meet the package's requirement (x-okdp-connection-ref.require); demo/demo-pg is the provider of the default class standard of server connections (global.okdp.provisioning.server.default): name a class whose provider meets it (class), or change the default:
- at '/port': maximum: got 5,433, want 5,432`},
		"a class named": {M{"class": "standard"}, `db (claim "demo.demo-app.db" of demo/demo-pg): the connection does not meet the package's requirement (x-okdp-connection-ref.require):
- at '/port': maximum: got 5,433, want 5,432`},
		"a provider named": {M{"provider": "demo-pg"}, `db (claim "demo.demo-app.db" of demo/demo-pg): the connection does not meet the package's requirement (x-okdp-connection-ref.require):
- at '/port': maximum: got 5,433, want 5,432`},
	} {
		t.Run(name, func(t *testing.T) {
			in := inputs(M{"db": tc.claim}, M{"global": M{"okdp": M{"provisioning": M{"server": M{"default": "standard", "classes": M{"standard": M{"provider": "demo-pg"}}}}}}})
			in.Contracts, in.Bindings = outputContracts, b
			if _, err := newInstance(ch, in, defService); err == nil || err.Error() != tc.want {
				t.Errorf("got %v\nwant %s", err, tc.want)
			}
		})
	}
}

// TestNamedClaimsErrors: the named claims of the context, every problem
// at once, in name order: a name that is no DNS-1123 label, a claim that
// is no object, a key of no claim, no contract, one that is no contract
// name, a provider not of the form, a connection's name; claims not a map.
func TestNamedClaimsErrors(t *testing.T) {
	_, _, err := claimCtx(M{"claims": M{
		"Reports": M{"contract": "server"},
		"a":       "server",
		"b":       M{"contract": "server", "size": "1Gi"},
		"c":       M{"provider": "data/pg"},
		"d":       M{"contract": "a/b"},
		"e":       M{"contract": "server", "provider": "a/b/c"},
		"main-db": M{"contract": "database"},
	}, "connections": M{"main-db": M{"contract": "database", "host": "h"}}}, nil)
	want := `claims.Reports: claim: its name is a DNS-1123 label
claims.a: a claim, an object, got "server"
claims.b: claim: unknown key "size": contract, class, provider and request only
claims.c: claim: contract is required, the contract of the connection it claims
claims.d: claim: contract "a/b" is not a contract name
claims.e: claim: provider "a/b/c": <instance>, <release>, <namespace>/<instance> or <namespace>/<release>
claims.main-db: a connection of that name too (connections.main-db)`
	if err == nil || err.Error() != want {
		t.Errorf("got %v\nwant %s", err, want)
	}
	if _, _, err := claimCtx(M{"claims": []any{"a"}}, nil); err == nil || err.Error() != `claims: the named claims, by name, got []interface {}{"a"}` {
		t.Errorf("a list: got %v", err)
	}
}

// TestResolveClaimErrors: a claim no provider is bound to; one whose
// provider has not published it yet, which says so (missingOutputs); one
// bound as another contract's; a claim of a reference that takes none; a
// claim that is not one.
func TestResolveClaimErrors(t *testing.T) {
	bound := map[string]Binding{"db": {Namespace: "data", Release: "demo-pg", Contract: "server", ID: "demo.demo-app.db"}}
	for name, tc := range map[string]struct {
		values   M
		bindings map[string]Binding
		want     string
	}{
		"unbound":     {M{"db": M{}}, nil, "db: claim (server): no provider is bound to it: the claims bind when the instances compile together (CompileAll)"},
		"unpublished": {M{"db": M{}}, bound, `db: claim "demo.demo-app.db" (server): data/demo-pg publishes no output for it`},
		"another contract": {
			M{"db": M{}}, map[string]Binding{"db": {Namespace: "data", Release: "demo-pg", Contract: "database", ID: "demo.demo-app.db", Output: pgOutput}},
			"db: claim (server): bound as a database claim",
		},
		"no store": {
			M{"db": M{}}, map[string]Binding{"db": {Namespace: "data", Release: "demo-pg", Contract: "server", ID: "demo.demo-app.db", Output: pgOutput}},
			`db: claim "demo.demo-app.db" (server): of data/demo-pg, another namespace: its credentials go through the platform's secret store, global.okdp.secrets.store, which is not set`,
		},
		"no claim":   {M{"main": M{}}, nil, "main: a connection reference must be a string, got map[string]interface {}{}"},
		"a key":      {M{"db": M{"size": "1Gi"}}, nil, `db: claim: unknown key "size": class, provider and request only`},
		"a provider": {M{"db": M{"provider": "a/b/c"}}, nil, `db: claim: provider "a/b/c": <instance>, <release>, <namespace>/<instance> or <namespace>/<release>`},
		"a value":    {M{"db": M{"provider": 1}}, nil, "db: claim: provider must be a string, got 1"},
		"a request":  {M{"db": M{"request": "5Gi"}}, nil, `db: claim: request must be an object, got "5Gi"`},
		"a named claim of another contract": {
			M{"db": "reports", "claims": M{"reports": M{"contract": "database"}}}, nil,
			`db: claim "reports" has contract "database", "server" is expected`,
		},
		"a named claim, no claim taken": {
			M{"main": "reports", "claims": M{"reports": M{"contract": "database"}}}, nil,
			`main: "reports" is a claim: the reference takes none (x-okdp-connection-ref's provision)`,
		},
		"a named claim unbound": {
			M{"db": "reports", "claims": M{"reports": M{"contract": "server"}}}, nil,
			"db: claim (server): no provider is bound to it: the claims bind when the instances compile together (CompileAll)",
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := claimCtx(tc.values, tc.bindings)
			if err == nil || err.Error() != tc.want {
				t.Fatalf("got %v\nwant %s", err, tc.want)
			}
			m, waits := errors.AsType[*missingOutputs](err)
			if waits != (name == "unpublished") || waits && !reflect.DeepEqual(m.outputs, []outputKey{{"data", "server", "demo.demo-app.db", "demo-pg"}}) {
				t.Errorf("missing outputs: %v %v", waits, m)
			}
		})
	}
}

// TestParseClaim: a named claim as written, what it holds even when it
// fails.
func TestParseClaim(t *testing.T) {
	c, err := ParseClaim("vectors", M{"contract": "database", "class": "huge", "request": M{"size": "5Gi"}})
	if want := (ClaimSpec{Name: "vectors", Contract: "database", Class: "huge", Request: M{"size": "5Gi"}}); err != nil || !reflect.DeepEqual(c, want) {
		t.Errorf("got %+v, %v, want %+v", c, err, want)
	}
	b, err := yaml.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(b), "contract: database\nclass: huge\nrequest:\n    size: 5Gi\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	for name, tc := range map[string]struct {
		name string
		v    any
		want string
	}{
		"no contract": {"a", M{"class": "huge"}, "claims.a: claim: contract is required"},
		"both":        {"a", M{"contract": "database", "class": "huge", "provider": "pg"}, "claims.a: claim: class or provider, not both"},
		"not a label": {"A", M{"contract": "database"}, "claims.A: claim: its name is a DNS-1123 label"},
		"not a map":   {"a", "database", "claims.a: a claim, an object"},
		"an unknown":  {"a", M{"contract": "database", "size": 1.0}, `claims.a: claim: unknown key "size"`},
	} {
		c, err := ParseClaim(tc.name, tc.v)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want %q", name, err, tc.want)
		}
		if c.Name != tc.name {
			t.Errorf("%s: got the name %q", name, c.Name)
		}
	}
	if c, _ := ParseClaim("a", M{"class": "huge"}); c.Class != "huge" {
		t.Errorf("a claim that fails keeps what it holds: got %+v", c)
	}
}
