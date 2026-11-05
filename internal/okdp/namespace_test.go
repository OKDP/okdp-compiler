package okdp

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/okdp/okdp-compiler/internal/chart"
	"github.com/okdp/okdp-compiler/internal/value"
)

// nodeContracts: server, whose outputs are named, its claims requesting a
// size and a tier (a by default), and other, of the same fields, a claim
// requesting nothing.
var nodeContracts = fstest.MapFS{
	"server.schema.json": {Data: []byte(`{"x-okdp-contract": "server", "properties": {"host": {"type": "string"}, "request": {"type": "string"}}, "required": ["host"],
		"x-okdp-claim": {"properties": {"size": {"type": "string", "pattern": "^[0-9]+Gi$"}, "tier": {"enum": ["a", "b"], "default": "a"}}, "additionalProperties": false}}`)},
	"other.schema.json": {Data: []byte(`{"x-okdp-contract": "other", "properties": {"host": {"type": "string"}, "request": {"type": "string"}}, "required": ["host"]}`)},
}

// nodeDef: def, whose instances reference server outputs (uses), publish
// server outputs (publishes),
// each with its name after the host of the first it uses, and fail on
// demand (fail).
var nodeDef = func() *Definition {
	files := maps.Clone(pkg)
	files["config.schema.json"] = &fstest.MapFile{Data: []byte(`{"properties": {
		"replicas": {"type": "integer", "default": 1},
		"uses": {"type": "array", "default": [], "items": {"type": "string", "x-okdp-connection-ref": {"contract": "server"}}},
		"publishes": {"type": "array", "default": [], "items": {"type": "string"}},
		"fail": {"type": "boolean", "default": false}}}`)}
	d := withFiles(files)
	d.Okdp = []byte(`
errors:
  - {$if: '${{ values.fail }}', $value: it fails}
provides:
  - $for: n in values.publishes
    $each: {contract: server, name: '${{ n }}', values: {host: '${{ (values.uses.size() > 0 ? values.uses[0].host + "/" : "") + n }}'}}
`)
	return d
}()

// node: an instance of nodeDef, release demo-<name> of namespace demo,
// these values.
func node(name string, values M) Instance {
	in := inputs(values)
	in.Contracts, in.Release = nodeContracts, "demo-"+name
	return Instance{Definition: nodeDef, Inputs: in}
}

// outcomes: what CompileAll gives, by release: the hosts the instance
// publishes, or its error.
func outcomes(instances []Instance) map[string]string {
	got := map[string]string{}
	for i, o := range CompileAll(context.Background(), instances) {
		if o.Err != nil {
			got[instances[i].Inputs.Release] = "error: " + o.Err.Error()
			continue
		}
		var hosts []string
		for _, out := range o.Result.Info.Provides {
			hosts = append(hosts, fmt.Sprint(out.Values["host"]))
		}
		got[o.Result.Release] = strings.Join(hosts, " ")
	}
	return got
}

// permutations: every order of instances.
func permutations(instances []Instance) [][]Instance {
	if len(instances) <= 1 {
		return [][]Instance{instances}
	}
	var all [][]Instance
	for i := range instances {
		rest := slices.Concat(instances[:i], instances[i+1:])
		for _, p := range permutations(rest) {
			all = append(all, slices.Concat([]Instance{instances[i]}, p))
		}
	}
	return all
}

// TestCompileAll: instances compiled together, in every order, give the
// same outcomes: a chain, consumers first; an output nobody publishes; a provider that fails, a
// cycle of outputs, whose consumers say what does not compile; an output
// two instances publish, and what resolved through it.
func TestCompileAll(t *testing.T) {
	missing := func(path, ref string) string {
		return fmt.Sprintf(`error: %s: %q is not a connection, nor an output of an instance of the namespace`, path, ref)
	}
	for name, tc := range map[string]struct {
		instances []Instance
		want      map[string]string
	}{
		"a chain": {
			[]Instance{node("c", M{"uses": []any{"b"}, "publishes": []any{"c"}}), node("b", M{"uses": []any{"a"}, "publishes": []any{"b"}}), node("a", M{"publishes": []any{"a"}})},
			map[string]string{"demo-a": "a", "demo-b": "a/b", "demo-c": "a/b/c"},
		},
		"nobody publishes it": {
			[]Instance{node("a", M{"uses": []any{"x"}}), node("b", M{"publishes": []any{"b"}})},
			map[string]string{"demo-a": missing("uses[0]", "x"), "demo-b": "b"},
		},
		"a provider that fails": {
			[]Instance{node("a", M{"uses": []any{"b"}}), node("b", M{"publishes": []any{"b"}, "fail": true})},
			map[string]string{"demo-a": missing("uses[0]", "b") + " (demo-b does not compile either: what it publishes is unknown)", "demo-b": "error: it fails"},
		},
		"a cycle": {
			[]Instance{node("a", M{"uses": []any{"b"}, "publishes": []any{"a"}}), node("b", M{"uses": []any{"a"}, "publishes": []any{"b"}}), node("c", M{"fail": true})},
			map[string]string{
				"demo-a": missing("uses[0]", "b") + " (demo-b, demo-c do not compile either: what they publish is unknown)",
				"demo-b": missing("uses[0]", "a") + " (demo-a, demo-c do not compile either: what they publish is unknown)",
				"demo-c": "error: it fails",
			},
		},
		"a shared output": {
			[]Instance{
				node("p", M{"publishes": []any{"x"}}), node("q", M{"publishes": []any{"x", "q"}}),
				node("c", M{"uses": []any{"x"}, "publishes": []any{"y"}}), node("d", M{"uses": []any{"y"}}),
			},
			map[string]string{
				"demo-p": `error: provides "x" (server): demo-q publishes an output of that contract and name too, a reference to it would not know which`,
				"demo-q": `error: provides "x" (server): demo-p publishes an output of that contract and name too, a reference to it would not know which`,
				"demo-c": `error: uses[0]: "x" names server outputs of several instances of the namespace: demo-p, demo-q`,
				"demo-d": missing("uses[0]", "y") + " (demo-c, demo-p, demo-q do not compile either: what they publish is unknown)",
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			for _, order := range permutations(tc.instances) {
				if got := outcomes(order); !reflect.DeepEqual(got, tc.want) {
					var releases []string
					for _, inst := range order {
						releases = append(releases, inst.Inputs.Release)
					}
					t.Fatalf("in the order %v\ngot  %v\nwant %v", releases, got, tc.want)
				}
			}
		})
	}
}

// TestCompileAllSingle: an instance compiled with the others gives what a
// compile of it alone gives, Published what they publish.
func TestCompileAllSingle(t *testing.T) {
	a, b := node("a", M{"publishes": []any{"a"}}), node("b", M{"uses": []any{"a"}, "publishes": []any{"b"}})
	got := CompileAll(context.Background(), []Instance{b, a})
	alone := b.Inputs
	alone.Published = map[string]Info{"demo-a": got[1].Result.Info}
	want, err := Compile(context.Background(), nodeDef, alone)
	if err != nil || got[0].Err != nil {
		t.Fatal(err, got[0].Err)
	}
	if !reflect.DeepEqual(got[0].Result, want) {
		t.Errorf("got %#v\nwant %#v", got[0].Result, want)
	}
}

// TestCompileAllNamespaces: the namespaces are compiled apart: an output
// name in two of them is no shared output, a reference does not reach
// another namespace; a release given twice in one is refused, each time,
// as an instance name (Instance.Name, the release when it has none); a
// Published given is not read.
func TestCompileAllNamespaces(t *testing.T) {
	other := func(inst Instance) Instance {
		inst.Inputs.Namespace = "other"
		return inst
	}
	given := node("b", M{"uses": []any{"z"}})
	given.Inputs.Published = map[string]Info{"demo-z": {Provides: []Output{{Name: "z", Contract: "server", Values: M{"host": "z"}}}}}
	named := func(inst Instance, name string) Instance {
		inst.Name = name
		return inst
	}
	instances := []Instance{
		node("a", M{"publishes": []any{"x"}}), other(node("a", M{"publishes": []any{"x"}})),
		other(node("c", M{"uses": []any{"x"}})), given,
		other(node("t", M{"publishes": []any{"t"}})), other(node("t", M{})),
		named(other(node("u", M{})), "n"), named(other(node("v", M{})), "n"), named(node("w", M{}), "demo-a"),
	}
	var got []string
	for _, o := range CompileAll(context.Background(), instances) {
		if o.Err != nil {
			got = append(got, o.Err.Error())
		} else {
			got = append(got, o.Result.Namespace+"/"+o.Result.Release)
		}
	}
	want := []string{
		`instance name "demo-a" is given twice in namespace "demo"`, "other/demo-a", "other/demo-c",
		`uses[0]: "z" is not a connection, nor an output of an instance of the namespace (demo-a, demo-w do not compile either: what they publish is unknown)`,
		`release "demo-t" is given twice in namespace "other"`, `release "demo-t" is given twice in namespace "other"`,
		`instance name "n" is given twice in namespace "other"`, `instance name "n" is given twice in namespace "other"`,
		`instance name "demo-a" is given twice in namespace "demo"`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

// pgDef: a package that provisions server connections, an output per
// claim, of its contract, its host after the claim's ID and the
// provider's namespace, its request as JSON; it fails on demand (fail).
var pgDef = func() *Definition {
	files := maps.Clone(pkg)
	files["config.schema.json"] = &fstest.MapFile{Data: []byte(`{"properties": {
		"replicas": {"type": "integer", "default": 1},
		"fail": {"type": "boolean", "default": false}}}`)}
	d := withFiles(files)
	// Any namespace may claim of an instance of it (tree.consent).
	d.Package.Provisions = []Provision{{Contract: "server", Claimers: []string{"*"}}}
	d.Okdp = []byte(`
errors:
  - {$if: '${{ values.fail }}', $value: it fails}
provides:
  - $for: c in claims
    $each: {contract: '${{ c.contract }}', name: '${{ c.id }}', values: {host: '${{ c.id }}.${{ release.namespace }}', request: '${{ toJson(c.request) }}'}}
`)
	return d
}()

// pgSharedDef: pgDef, which also publishes a server output x of its own.
var pgSharedDef = func() *Definition {
	d := *pgDef
	d.Okdp = append(slices.Clone(pgDef.Okdp), "  - {contract: server, name: x, values: {host: x}}\n"...)
	return &d
}()

// otherDef: pgDef, a package that provisions other connections.
var otherDef = func() *Definition {
	d := *pgDef
	d.Package.Provisions = []Provision{{Contract: "other", Claimers: []string{"*"}}}
	return &d
}()

// appDef: a package whose reference db takes claims.
var appDef = func() *Definition {
	files := maps.Clone(pkg)
	files["config.schema.json"] = &fstest.MapFile{Data: []byte(`{"properties": {
		"replicas": {"type": "integer", "default": 1},
		"db": {"type": ["string", "object"], "x-okdp-connection-ref": {"contract": "server", "provision": true}}}}`)}
	return withFiles(files)
}()

// dbsDef: a package whose references, dbs["eu.main"], dbs.eu-main and
// dbs.us, take claims; the first two give one label.
var dbsDef = func() *Definition {
	files := maps.Clone(pkg)
	files["config.schema.json"] = &fstest.MapFile{Data: []byte(`{"properties": {
		"replicas": {"type": "integer", "default": 1},
		"dbs": {"type": "object", "properties": {
			"eu.main": {"type": ["string", "object"], "x-okdp-connection-ref": {"contract": "server", "provision": true}},
			"eu-main": {"type": ["string", "object"], "x-okdp-connection-ref": {"contract": "server", "provision": true}},
			"us": {"type": ["string", "object"], "x-okdp-connection-ref": {"contract": "server", "provision": true}}}}}}`)}
	return withFiles(files)
}()

// fixedDef: appDef, its reference db fixing what its claims request, a
// size and a tier.
var fixedDef = func() *Definition {
	files := maps.Clone(pkg)
	files["config.schema.json"] = &fstest.MapFile{Data: []byte(`{"properties": {
		"replicas": {"type": "integer", "default": 1},
		"db": {"type": ["string", "object"], "x-okdp-connection-ref": {"contract": "server", "provision": true, "request": {"size": "2Gi", "tier": "b"}}}}}`)}
	return withFiles(files)
}()

// strictDef: appDef, its reference db requiring a host of namespace data.
var strictDef = func() *Definition {
	files := maps.Clone(pkg)
	files["config.schema.json"] = &fstest.MapFile{Data: []byte(`{"properties": {
		"replicas": {"type": "integer", "default": 1},
		"db": {"type": ["string", "object"], "x-okdp-connection-ref": {"contract": "server", "provision": true, "require": {"properties": {"host": {"pattern": "\\.data$"}}}}}}}`)}
	return withFiles(files)
}()

// claimInstance: an instance of def, release name of namespace, these
// values.
func claimInstance(d *Definition, namespace, name string, values M) Instance {
	in := inputs(values)
	in.Contracts, in.Namespace, in.Release = nodeContracts, namespace, name
	return Instance{Definition: d, Inputs: in}
}

// withClasses: inst, a context layer of level holding classes, the
// classes of server connections (global.okdp.provisioning.server).
func withClasses(inst Instance, level uint, classes M) Instance {
	inst.Inputs.Context = append(slices.Clone(inst.Inputs.Context), ContextLayer{Level: level, Values: M{"global": M{"okdp": M{"provisioning": M{"server": classes}}}}})
	return inst
}

// withDefault: inst, its context's classes of server connections, at the
// platform's level: one, standard, the default, its provider provider.
func withDefault(inst Instance, provider string) Instance {
	return withClasses(inst, 0, M{"default": "standard", "classes": M{"standard": M{"provider": provider}}})
}

// withClaims: inst, its context's named claims, a layer of level (1, a
// project's).
func withClaims(inst Instance, level uint, claims M) Instance {
	inst.Inputs.Context = append(slices.Clone(inst.Inputs.Context), ContextLayer{Level: level, Values: M{"claims": claims}})
	return inst
}

// claimOutcomes: what CompileAll gives, by namespace and release: what the
// instance publishes, with what was requested, the providers it depends on
// and its claims, or its error.
func claimOutcomes(instances []Instance) map[string]string {
	got := map[string]string{}
	for i, o := range CompileAll(context.Background(), instances) {
		in := instances[i].Inputs
		key := in.Namespace + "/" + in.Release
		if o.Err != nil {
			got[key] = "error: " + o.Err.Error()
			continue
		}
		var s []string
		for _, out := range o.Result.Info.Provides {
			p := "provides " + out.Name + " " + fmt.Sprint(out.Values["host"])
			if r := fmt.Sprint(out.Values["request"]); r != "{}" {
				p += " " + r
			}
			s = append(s, p)
		}
		for _, d := range o.Result.Dependencies {
			s = append(s, "depends on "+d.Namespace+"/"+d.Instance+" "+d.Output)
		}
		for _, b := range o.Claims {
			class := ""
			if b.Class != "" {
				class = " class " + b.Class
			}
			s = append(s, fmt.Sprintf("bound %s %s %s/%s%s named=%t", b.Field, b.ID, b.Namespace, b.Release, class, b.Named))
		}
		got[key] = strings.Join(s, "; ")
	}
	return got
}

// TestCompileAllClaims: claims bound to their providers and compiled, in
// every order: a connection of a reference's own, to the provider of the
// default class; a named claim, one connection, of the provider whether a
// reference names it or not; the default class's provider among several,
// one of the claimer's namespace; a provider named over the default; a
// class named, one a project adds, one that fixes part of the request;
// refused, a claim that names neither a class nor a provider without a
// default class, which says what the classes are, a class or a default
// class that is none, a class's provider not of the form or that
// provisions nothing, a provider named that provisions nothing, a class
// and a provider, a named claim of another contract, one declared
// differently in two contexts, or by the platform, a request that sets a
// key of its class; a request, the claim schema's defaults applied,
// merged over what the package fixes, refused, every violation, or of a
// contract that takes none; a claim whose provider does not compile; a
// requirement the default class's provider does not meet, the claim not
// moved to one that does, which a claim names; a claimer whose provider,
// of another namespace, is refused for an output it shares.
func TestCompileAllClaims(t *testing.T) {
	pg, pg2 := claimInstance(pgDef, "data", "pg", M{}), claimInstance(pgDef, "data", "pg2", M{})
	app := func(name string, db any) Instance {
		if db == nil {
			return claimInstance(appDef, "demo", name, M{})
		}
		return claimInstance(appDef, "demo", name, M{"db": db})
	}
	byDefault := func(name string, db any, provider string) Instance { return withDefault(app(name, db), provider) }
	// The platform's classes: standard, the default, of data/pg, and fast,
	// of data/pg2.
	classes := func(inst Instance) Instance {
		return withClasses(inst, 0, M{"default": "standard", "classes": M{"standard": M{"provider": "data/pg"}, "fast": M{"provider": "data/pg2"}}})
	}
	named := func(name string, db any) Instance {
		return withClaims(byDefault(name, db, "data/pg"), 1, M{"s": M{"contract": "server"}})
	}
	for name, tc := range map[string]struct {
		instances []Instance
		want      map[string]string
	}{
		"its own": {
			[]Instance{byDefault("demo-app", M{}, "data/pg"), pg},
			map[string]string{
				"demo/demo-app": "depends on data/pg demo.demo-app.db; bound db demo.demo-app.db data/pg class standard named=false",
				"data/pg":       "provides demo.demo-app.db demo.demo-app.db.data {\"tier\":\"a\"}",
			},
		},
		"a named claim": {
			[]Instance{named("demo-a", "s"), named("demo-b", "s"), named("demo-c", nil), pg},
			map[string]string{
				"demo/demo-a": "depends on data/pg demo.s; bound db demo.s data/pg class standard named=false",
				"demo/demo-b": "depends on data/pg demo.s; bound db demo.s data/pg class standard named=false",
				"demo/demo-c": "",
				"data/pg":     "provides demo.s demo.s.data {\"tier\":\"a\"}",
			},
		},
		"a named claim of a provider of the namespace": {
			[]Instance{
				withClaims(byDefault("demo-a", "s", "pg"), 1, M{"s": M{"contract": "server"}}),
				withClaims(withDefault(claimInstance(pgDef, "demo", "pg", M{}), "pg"), 1, M{"s": M{"contract": "server"}}),
			},
			map[string]string{
				"demo/demo-a": "depends on /pg demo.s; bound db demo.s demo/pg class standard named=false",
				"demo/pg":     "provides demo.s demo.s.demo {\"tier\":\"a\"}",
			},
		},
		"a named claim nobody references": {
			[]Instance{withClaims(app("demo-a", nil), 1, M{"s": M{"contract": "server", "provider": "data/pg2"}}), withClaims(app("demo-b", nil), 1, M{"s": M{"contract": "server", "provider": "data/pg2"}}), pg, pg2},
			map[string]string{
				"demo/demo-a": "",
				"demo/demo-b": "",
				"data/pg":     "",
				"data/pg2":    "provides demo.s demo.s.data {\"tier\":\"a\"}",
			},
		},
		"a named claim of another contract": {
			[]Instance{withClaims(byDefault("demo-a", "s", "data/pg"), 1, M{"s": M{"contract": "other", "provider": "data/o"}}), pg, claimInstance(otherDef, "data", "o", M{})},
			map[string]string{
				"demo/demo-a": `error: db: claim "s" has contract "other", "server" is expected`,
				"data/pg":     "",
				"data/o":      "provides demo.s demo.s.data",
			},
		},
		"a named claim and a claim template of one name": {
			[]Instance{byDefault("x", M{}, "data/pg"), withClaims(byDefault("demo-b", "x-db", "data/pg"), 1, M{"x-db": M{"contract": "server"}}), pg},
			map[string]string{
				"demo/x":      "depends on data/pg demo.x.db; bound db demo.x.db data/pg class standard named=false",
				"demo/demo-b": "depends on data/pg demo.x-db; bound db demo.x-db data/pg class standard named=false",
				"data/pg":     `provides demo.x-db demo.x-db.data {"tier":"a"}; provides demo.x.db demo.x.db.data {"tier":"a"}`,
			},
		},
		"two fields of one ID": {
			[]Instance{withDefault(claimInstance(dbsDef, "demo", "demo-a", M{"dbs": M{"eu.main": M{}, "eu-main": M{}, "us": M{}}}), "data/pg"), pg},
			map[string]string{
				"demo/demo-a": `error: dbs["eu.main"]: claim: it gives the same ID (demo.demo-a.dbs-eu-main) as dbs.eu-main: rename one of the parameters`,
				"data/pg":     "",
			},
		},
		"a claim declared differently": {
			[]Instance{named("demo-a", "s"), withClaims(byDefault("demo-b", nil, "data/pg"), 1, M{"s": M{"contract": "server", "provider": "data/pg"}}), named("demo-c", nil), pg},
			map[string]string{
				"demo/demo-a": "error: claims.s: declared differently in the context of demo-a and demo-b",
				"demo/demo-b": "error: claims.s: declared differently in the context of demo-a and demo-b",
				"demo/demo-c": "error: claims.s: declared differently in the context of demo-a and demo-b",
				"data/pg":     "",
			},
		},
		"claims at level 0": {
			[]Instance{withClaims(byDefault("demo-a", "s", "data/pg"), 0, M{"s": M{"contract": "server"}}), pg},
			map[string]string{
				"demo/demo-a": "error: context layer 3 (level 0): claims: a project's, of level 1 or above",
				"data/pg":     "",
			},
		},
		"the default among several": {
			[]Instance{byDefault("demo-a", M{}, "data/pg2"), pg, pg2},
			map[string]string{
				"demo/demo-a": "depends on data/pg2 demo.demo-a.db; bound db demo.demo-a.db data/pg2 class standard named=false",
				"data/pg":     "",
				"data/pg2":    "provides demo.demo-a.db demo.demo-a.db.data {\"tier\":\"a\"}",
			},
		},
		"a default of the namespace": {
			[]Instance{byDefault("demo-a", M{}, "pg"), pg, claimInstance(pgDef, "demo", "pg", M{})},
			map[string]string{
				"demo/demo-a": "depends on /pg demo.demo-a.db; bound db demo.demo-a.db demo/pg class standard named=false",
				"data/pg":     "",
				"demo/pg":     "provides demo.demo-a.db demo.demo-a.db.demo {\"tier\":\"a\"}",
			},
		},
		"a provider named": {
			[]Instance{byDefault("demo-a", M{"provider": "data/pg2"}, "data/pg"), pg, pg2},
			map[string]string{
				"demo/demo-a": "depends on data/pg2 demo.demo-a.db; bound db demo.demo-a.db data/pg2 named=true",
				"data/pg":     "",
				"data/pg2":    "provides demo.demo-a.db demo.demo-a.db.data {\"tier\":\"a\"}",
			},
		},
		"no default": {
			[]Instance{app("demo-a", M{}), withClaims(withClasses(app("demo-b", "s"), 0, M{"classes": M{"standard": M{"provider": "data/pg"}, "fast": M{"provider": "data/pg2"}}}), 1, M{"s": M{"contract": "server"}}), pg, pg2},
			map[string]string{
				"demo/demo-a": "error: db: claim (server): it names no class, and the context sets no default one, global.okdp.provisioning.server.default (Inputs.Context): no class of server connections is defined (global.okdp.provisioning.server.classes)",
				"demo/demo-b": "error: claims.s: claim (server): it names no class, and the context sets no default one, global.okdp.provisioning.server.default (Inputs.Context): the classes are fast, standard",
				"data/pg":     "",
				"data/pg2":    "",
			},
		},
		"no provider": {
			[]Instance{app("demo-a", M{}), byDefault("demo-b", M{}, "data/pg"), app("demo-c", M{"provider": "data/pg"}), claimInstance(appDef, "data", "pg", M{})},
			map[string]string{
				"demo/demo-a": "error: db: claim (server): it names no class, and the context sets no default one, global.okdp.provisioning.server.default (Inputs.Context): no class of server connections is defined (global.okdp.provisioning.server.classes)",
				"demo/demo-b": `error: db: claim (server): the default class "standard": data/pg is no instance that provisions server connections`,
				"demo/demo-c": "error: db: claim (server): data/pg is no instance that provisions server connections",
				"data/pg":     "",
			},
		},
		"a class's provider not of the form": {
			[]Instance{byDefault("demo-a", M{}, "a/b/c"), pg},
			map[string]string{
				"demo/demo-a": `error: db: claim (server): the default class "standard" (global.okdp.provisioning.server.classes.standard): provider "a/b/c": <instance>, <release>, <namespace>/<instance> or <namespace>/<release>`,
				"data/pg":     "",
			},
		},
		"a class named": {
			[]Instance{classes(app("demo-a", M{"class": "fast"})), classes(app("demo-b", M{})), pg, pg2},
			map[string]string{
				"demo/demo-a": "depends on data/pg2 demo.demo-a.db; bound db demo.demo-a.db data/pg2 class fast named=true",
				"demo/demo-b": "depends on data/pg demo.demo-b.db; bound db demo.demo-b.db data/pg class standard named=false",
				"data/pg":     `provides demo.demo-b.db demo.demo-b.db.data {"tier":"a"}`,
				"data/pg2":    `provides demo.demo-a.db demo.demo-a.db.data {"tier":"a"}`,
			},
		},
		"a class a project adds": {
			[]Instance{withClasses(byDefault("demo-a", M{"class": "large"}, "data/pg"), 1, M{"classes": M{"large": M{"provider": "data/pg2"}}}), pg, pg2},
			map[string]string{
				"demo/demo-a": "depends on data/pg2 demo.demo-a.db; bound db demo.demo-a.db data/pg2 class large named=true",
				"data/pg":     "",
				"data/pg2":    `provides demo.demo-a.db demo.demo-a.db.data {"tier":"a"}`,
			},
		},
		"a class with a request": {
			[]Instance{
				withClasses(byDefault("demo-a", M{"class": "large"}, "data/pg"), 1, M{"classes": M{"large": M{"provider": "data/pg", "request": M{"size": "50Gi"}}}}),
				withClasses(byDefault("demo-b", M{"class": "large", "request": M{"size": "1Gi", "tier": "b"}}, "data/pg"), 1, M{"classes": M{"large": M{"provider": "data/pg", "request": M{"size": "50Gi"}}}}),
				pg,
			},
			map[string]string{
				"demo/demo-a": "depends on data/pg demo.demo-a.db; bound db demo.demo-a.db data/pg class large named=true",
				"demo/demo-b": "error: db: claim: request.size: set by the class large (global.okdp.provisioning.server.classes.large): remove it, or name another class",
				"data/pg":     `provides demo.demo-a.db demo.demo-a.db.data {"size":"50Gi","tier":"a"}`,
			},
		},
		"a class that is not one": {
			[]Instance{classes(app("demo-a", M{"class": "nope"})), withClasses(app("demo-b", M{}), 0, M{"default": "nope", "classes": M{"standard": M{"provider": "data/pg"}}}), withClasses(app("demo-c", M{"class": "nope"}), 0, M{}), pg},
			map[string]string{
				"demo/demo-a": `error: db: claim (server): class "nope" is not a class of server connections (global.okdp.provisioning.server.classes): fast, standard`,
				"demo/demo-b": `error: db: claim (server): the default class "nope" (global.okdp.provisioning.server.default) is not a class of server connections (global.okdp.provisioning.server.classes): standard`,
				"demo/demo-c": `error: db: claim (server): class "nope" is not a class of server connections (global.okdp.provisioning.server.classes): none is defined`,
				"data/pg":     "",
			},
		},
		"a class whose provider provisions nothing": {
			[]Instance{withClasses(app("demo-a", M{"class": "fast"}), 0, M{"classes": M{"fast": M{"provider": "data/x"}}}), pg},
			map[string]string{
				"demo/demo-a": `error: db: claim (server): class "fast": data/x is no instance that provisions server connections`,
				"data/pg":     "",
			},
		},
		"class and provider": {
			[]Instance{classes(app("demo-a", M{"class": "fast", "provider": "data/pg"})), pg, pg2},
			map[string]string{
				"demo/demo-a": "error: db: claim: class or provider, not both",
				"data/pg":     "",
				"data/pg2":    "",
			},
		},
		"a requirement": {
			[]Instance{
				withDefault(claimInstance(strictDef, "demo", "demo-a", M{"db": M{}}), "pg"),
				withDefault(claimInstance(strictDef, "demo", "demo-b", M{"db": M{"provider": "data/pg"}}), "pg"),
				pg, claimInstance(pgDef, "demo", "pg", M{}),
			},
			map[string]string{
				"demo/demo-a": `error: db (claim "demo.demo-a.db" of demo/pg): the connection does not meet the package's requirement (x-okdp-connection-ref.require); demo/pg is the provider of the default class standard of server connections (global.okdp.provisioning.server.default): name a class whose provider meets it (class), or change the default:
- at '/host': 'demo.demo-a.db.demo' does not match pattern '\\.data$'`,
				"demo/demo-b": "depends on data/pg demo.demo-b.db; bound db demo.demo-b.db data/pg named=true",
				"data/pg":     "provides demo.demo-b.db demo.demo-b.db.data {\"tier\":\"a\"}",
				"demo/pg":     "provides demo.demo-a.db demo.demo-a.db.demo {\"tier\":\"a\"}",
			},
		},
		"a request": {
			[]Instance{byDefault("demo-a", M{"request": M{"size": "5Gi"}}, "data/pg"), withClaims(byDefault("demo-b", "s", "data/pg"), 1, M{"s": M{"contract": "server", "request": M{"tier": "b"}}}), pg},
			map[string]string{
				"demo/demo-a": "depends on data/pg demo.demo-a.db; bound db demo.demo-a.db data/pg class standard named=false",
				"demo/demo-b": "depends on data/pg demo.s; bound db demo.s data/pg class standard named=false",
				"data/pg":     `provides demo.demo-a.db demo.demo-a.db.data {"size":"5Gi","tier":"a"}; provides demo.s demo.s.data {"tier":"b"}`,
			},
		},
		"a request refused": {
			[]Instance{byDefault("demo-a", M{"request": M{"size": "5G", "tier": "c", "zone": "eu"}}, "data/pg"), pg},
			map[string]string{
				"demo/demo-a": `error: db: claim (server): the request does not meet the contract's claim schema (x-okdp-claim):
- at '': additional properties 'zone' not allowed
- at '/size': '5G' does not match pattern '^[0-9]+Gi$'
- at '/tier': value must be one of 'a', 'b'`,
				"data/pg": "",
			},
		},
		"two claims refused": {
			[]Instance{
				withClaims(withDefault(claimInstance(dbsDef, "demo", "demo-a", M{"dbs": M{"eu.main": M{"class": "nope"}, "us": M{"request": M{"size": "5G"}}}}), "data/pg"), 1, M{"s": M{"contract": "server", "request": M{"tier": "c"}}}),
				pg,
			},
			map[string]string{
				"demo/demo-a": `error: claims.s: claim (server): the request does not meet the contract's claim schema (x-okdp-claim):
- at '/tier': value must be one of 'a', 'b'
dbs["eu.main"]: claim (server): class "nope" is not a class of server connections (global.okdp.provisioning.server.classes): standard
dbs.us: claim (server): the request does not meet the contract's claim schema (x-okdp-claim):
- at '/size': '5G' does not match pattern '^[0-9]+Gi$'`,
				"data/pg": "",
			},
		},
		"a request of a contract without claim schema": {
			[]Instance{withClaims(app("demo-a", nil), 1, M{"s": M{"contract": "other", "provider": "data/o", "request": M{"size": "1Gi"}}}), claimInstance(otherDef, "data", "o", M{})},
			map[string]string{
				"demo/demo-a": "error: claims.s: claim (other): request: the contract declares no claim schema (x-okdp-claim): a claim requests nothing of it",
				"data/o":      "",
			},
		},
		"a package's fixed request": {
			[]Instance{withDefault(claimInstance(fixedDef, "demo", "demo-a", M{"db": M{"request": M{"size": "5Gi"}}}), "data/pg"), withDefault(claimInstance(fixedDef, "demo", "demo-b", M{"db": M{}}), "data/pg"), pg},
			map[string]string{
				"demo/demo-a": "depends on data/pg demo.demo-a.db; bound db demo.demo-a.db data/pg class standard named=false",
				"demo/demo-b": "depends on data/pg demo.demo-b.db; bound db demo.demo-b.db data/pg class standard named=false",
				"data/pg":     `provides demo.demo-a.db demo.demo-a.db.data {"size":"5Gi","tier":"b"}; provides demo.demo-b.db demo.demo-b.db.data {"size":"2Gi","tier":"b"}`,
			},
		},
		"a provider refused for a shared output": {
			[]Instance{byDefault("app", M{}, "data/pg"), claimInstance(pgSharedDef, "data", "pg", M{}), claimInstance(nodeDef, "data", "x", M{"publishes": []any{"x"}})},
			map[string]string{
				"demo/app": `error: db: claim "demo.app.db" (server): data/pg publishes no output for it (data/pg does not compile either: what it publishes is unknown)`,
				"data/pg":  `error: provides "x" (server): x publishes an output of that contract and name too, a reference to it would not know which`,
				"data/x":   `error: provides "x" (server): pg publishes an output of that contract and name too, a reference to it would not know which`,
			},
		},
		"a provider that fails": {
			[]Instance{byDefault("demo-a", M{}, "data/pg"), claimInstance(pgDef, "data", "pg", M{"fail": true})},
			map[string]string{
				"demo/demo-a": `error: db: claim "demo.demo-a.db" (server): data/pg publishes no output for it (data/pg does not compile either: what it publishes is unknown)`,
				"data/pg":     "error: it fails",
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			for _, order := range permutations(tc.instances) {
				if got := claimOutcomes(order); !reflect.DeepEqual(got, tc.want) {
					t.Fatalf("got  %q\nwant %q", got, tc.want)
				}
			}
		})
	}
}

// TestCompileAllConsent: a provider serves the claims of another
// namespace through a class of the platform's, or with its consent, in
// every order: of a package that lists no claimers, the platform's
// default class and a class of the platform's named bind; a class of the
// project's, named or the default one, a provider named and a named
// claim's provider are refused, unless the provider is of the claimer's
// namespace; the claimers of the instance (Inputs.Claimers) bind the
// namespaces they list, or any ("*"), and replace those of its package,
// none when empty; a package's claimers (provisions) bind those it lists.
func TestCompileAllConsent(t *testing.T) {
	closedDef := *pgDef
	closedDef.Package.Provisions = []Provision{{Contract: "server"}}
	demoDef := *pgDef
	demoDef.Package.Provisions = []Provision{{Contract: "server", Claimers: []string{"demo"}}}
	pg := func(d *Definition, namespace, name string, claimers []string) Instance {
		inst := claimInstance(d, namespace, name, M{})
		inst.Inputs.Claimers = claimers
		return inst
	}
	closed := pg(&closedDef, "data", "pg", nil)
	app := func(name string, db any) Instance { return claimInstance(appDef, "demo", name, M{"db": db}) }
	// The platform's classes, standard the default, and a class of the
	// project's, large, each of data/pg.
	classes := func(inst Instance) Instance {
		inst = withClasses(inst, 0, M{"default": "standard", "classes": M{"standard": M{"provider": "data/pg"}}})
		return withClasses(inst, 1, M{"classes": M{"large": M{"provider": "data/pg"}}})
	}
	refused := func(field, id, by, list string) string {
		return fmt.Sprintf("error: %s: claim %q (server): of data/pg%s, another namespace: demo is not one of its claimers (claimers of instance data/pg: %s): the provider lists the namespaces that may claim of it (claimers of its instance.yaml, else of its package's provisions), or the platform routes them through a class of platform.yaml", field, id, by, list)
	}
	for name, tc := range map[string]struct {
		instances []Instance
		want      map[string]string
	}{
		"the platform's classes": {
			[]Instance{classes(app("demo-a", M{})), classes(app("demo-b", M{"class": "standard"})), closed},
			map[string]string{
				"demo/demo-a": "depends on data/pg demo.demo-a.db; bound db demo.demo-a.db data/pg class standard named=false",
				"demo/demo-b": "depends on data/pg demo.demo-b.db; bound db demo.demo-b.db data/pg class standard named=true",
				"data/pg":     `provides demo.demo-a.db demo.demo-a.db.data {"tier":"a"}; provides demo.demo-b.db demo.demo-b.db.data {"tier":"a"}`,
			},
		},
		"without consent": {
			[]Instance{
				classes(app("demo-a", M{"class": "large"})),
				withClasses(app("demo-b", M{}), 1, M{"default": "large", "classes": M{"large": M{"provider": "data/pg"}}}),
				app("demo-c", M{"provider": "data/pg"}),
				withClaims(classes(app("demo-d", "s")), 1, M{"s": M{"contract": "server", "provider": "data/pg"}}),
				withClaims(classes(claimInstance(appDef, "demo", "demo-e", M{})), 1, M{"s": M{"contract": "server", "provider": "data/pg"}}),
				closed,
			},
			map[string]string{
				"demo/demo-a": refused("db", "demo.demo-a.db", `, the provider of class "large"`, "none, its package's"),
				"demo/demo-b": refused("db", "demo.demo-b.db", `, the provider of the default class "large"`, "none, its package's"),
				"demo/demo-c": refused("db", "demo.demo-c.db", "", "none, its package's"),
				"demo/demo-d": refused("claims.s", "demo.s", "", "none, its package's"),
				"demo/demo-e": refused("claims.s", "demo.s", "", "none, its package's"),
				"data/pg":     "",
			},
		},
		"of the claimer's namespace": {
			[]Instance{app("demo-a", M{"provider": "pg"}), withClasses(app("demo-b", M{"class": "large"}), 1, M{"classes": M{"large": M{"provider": "pg"}}}), pg(&closedDef, "demo", "pg", nil)},
			map[string]string{
				"demo/demo-a": "depends on /pg demo.demo-a.db; bound db demo.demo-a.db demo/pg named=true",
				"demo/demo-b": "depends on /pg demo.demo-b.db; bound db demo.demo-b.db demo/pg class large named=true",
				"demo/pg":     `provides demo.demo-a.db demo.demo-a.db.demo {"tier":"a"}; provides demo.demo-b.db demo.demo-b.db.demo {"tier":"a"}`,
			},
		},
		"the instance's claimers": {
			[]Instance{classes(app("demo-a", M{"class": "large"})), app("demo-b", M{"provider": "data/pg"}), pg(&closedDef, "data", "pg", []string{"reports", "demo"})},
			map[string]string{
				"demo/demo-a": "depends on data/pg demo.demo-a.db; bound db demo.demo-a.db data/pg class large named=true",
				"demo/demo-b": "depends on data/pg demo.demo-b.db; bound db demo.demo-b.db data/pg named=true",
				"data/pg":     `provides demo.demo-a.db demo.demo-a.db.data {"tier":"a"}; provides demo.demo-b.db demo.demo-b.db.data {"tier":"a"}`,
			},
		},
		"any namespace": {
			[]Instance{app("demo-a", M{"provider": "data/pg"}), pg(&closedDef, "data", "pg", []string{"*"})},
			map[string]string{
				"demo/demo-a": "depends on data/pg demo.demo-a.db; bound db demo.demo-a.db data/pg named=true",
				"data/pg":     `provides demo.demo-a.db demo.demo-a.db.data {"tier":"a"}`,
			},
		},
		"other claimers": {
			[]Instance{app("demo-a", M{"provider": "data/pg"}), pg(&closedDef, "data", "pg", []string{"reports"})},
			map[string]string{
				"demo/demo-a": refused("db", "demo.demo-a.db", "", "reports"),
				"data/pg":     "",
			},
		},
		"the package's claimers": {
			[]Instance{app("demo-a", M{"provider": "data/pg"}), pg(&demoDef, "data", "pg", nil)},
			map[string]string{
				"demo/demo-a": "depends on data/pg demo.demo-a.db; bound db demo.demo-a.db data/pg named=true",
				"data/pg":     `provides demo.demo-a.db demo.demo-a.db.data {"tier":"a"}`,
			},
		},
		"the instance's over the package's": {
			[]Instance{app("demo-a", M{"provider": "data/pg"}), pg(&demoDef, "data", "pg", []string{})},
			map[string]string{
				"demo/demo-a": refused("db", "demo.demo-a.db", "", "none"),
				"data/pg":     "",
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			for _, order := range permutations(tc.instances) {
				if got := claimOutcomes(order); !reflect.DeepEqual(got, tc.want) {
					t.Fatalf("got  %q\nwant %q", got, tc.want)
				}
			}
		})
	}
}

// TestCompileAllProviderNames: a provider, a claim's, a class's or a named
// claim's, names an instance of its namespace by its name or by its
// release, in every order: by name, qualified or of the claimer's
// namespace; by release, the instance named or not; refused, a name of an
// instance that provisions nothing, and a name that is the release of an
// instance and the name of another, whatever they provision, each claim
// that names it, the instances it names compiled.
func TestCompileAllProviderNames(t *testing.T) {
	named := func(inst Instance, name string) Instance {
		inst.Name = name
		return inst
	}
	app := func(release, name string, db any) Instance {
		return named(claimInstance(appDef, "demo", release, M{"db": db}), name)
	}
	pg := named(claimInstance(pgDef, "data", "data-pg", M{}), "pg")
	for name, tc := range map[string]struct {
		instances []Instance
		want      map[string]string
	}{
		"by name": {
			[]Instance{
				app("demo-a", "a", M{"provider": "data/pg"}),
				withDefault(app("demo-b", "b", M{}), "data/pg"),
				withClaims(withDefault(app("demo-c", "c", "s"), "data/pg"), 1, M{"s": M{"contract": "server", "provider": "data/pg"}}),
				pg,
			},
			map[string]string{
				"demo/demo-a": "depends on data/data-pg demo.a.db; bound db demo.a.db data/data-pg named=true",
				"demo/demo-b": "depends on data/data-pg demo.b.db; bound db demo.b.db data/data-pg class standard named=false",
				"demo/demo-c": "depends on data/data-pg demo.s; bound db demo.s data/data-pg named=true",
				"data/data-pg": `provides demo.a.db demo.a.db.data {"tier":"a"}; provides demo.b.db demo.b.db.data {"tier":"a"}; ` +
					`provides demo.s demo.s.data {"tier":"a"}`,
			},
		},
		"by name, of the claimer's namespace": {
			[]Instance{app("demo-a", "a", M{"provider": "pg"}), withDefault(app("demo-b", "b", M{}), "pg"), named(claimInstance(pgDef, "demo", "demo-pg", M{}), "pg")},
			map[string]string{
				"demo/demo-a":  "depends on /demo-pg demo.a.db; bound db demo.a.db demo/demo-pg named=true",
				"demo/demo-b":  "depends on /demo-pg demo.b.db; bound db demo.b.db demo/demo-pg class standard named=false",
				"demo/demo-pg": `provides demo.a.db demo.a.db.demo {"tier":"a"}; provides demo.b.db demo.b.db.demo {"tier":"a"}`,
			},
		},
		"by release": {
			[]Instance{app("demo-a", "a", M{"provider": "data/data-pg"}), withDefault(app("demo-b", "b", M{}), "data/pg2"), pg, claimInstance(pgDef, "data", "pg2", M{})},
			map[string]string{
				"demo/demo-a":  "depends on data/data-pg demo.a.db; bound db demo.a.db data/data-pg named=true",
				"demo/demo-b":  "depends on data/pg2 demo.b.db; bound db demo.b.db data/pg2 class standard named=false",
				"data/data-pg": `provides demo.a.db demo.a.db.data {"tier":"a"}`,
				"data/pg2":     `provides demo.b.db demo.b.db.data {"tier":"a"}`,
			},
		},
		"an instance that provisions nothing": {
			[]Instance{app("demo-a", "a", M{"provider": "data/x"}), named(claimInstance(appDef, "data", "data-x", M{}), "x"), pg},
			map[string]string{
				"demo/demo-a":  "error: db: claim (server): data/x is no instance that provisions server connections",
				"data/data-x":  "",
				"data/data-pg": "",
			},
		},
		"ambiguous": {
			[]Instance{
				app("demo-c", "c", M{"provider": "demo-a"}),
				withDefault(app("demo-d", "d", M{}), "demo/demo-a"),
				app("demo-e", "e", M{"provider": "a"}),
				named(claimInstance(pgDef, "demo", "demo-a", M{}), "a"),
				named(claimInstance(appDef, "demo", "demo-demo-a", M{}), "demo-a"),
			},
			map[string]string{
				"demo/demo-c":      "error: db: claim (server): demo/demo-a names more than one instance, demo-a (its release) and demo-demo-a (its name): the claim would not know which",
				"demo/demo-d":      `error: db: claim (server): the default class "standard": demo/demo-a names more than one instance, demo-a (its release) and demo-demo-a (its name): the claim would not know which`,
				"demo/demo-e":      "depends on /demo-a demo.e.db; bound db demo.e.db demo/demo-a named=true",
				"demo/demo-a":      `provides demo.e.db demo.e.db.demo {"tier":"a"}`,
				"demo/demo-demo-a": "",
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			for _, order := range permutations(tc.instances) {
				if got := claimOutcomes(order); !reflect.DeepEqual(got, tc.want) {
					t.Fatalf("got  %q\nwant %q", got, tc.want)
				}
			}
		})
	}
}

// TestClaimID: a claim's ID, DNS-1123 labels joined by dots, 63
// characters at most (chart.IsClaimID): what the dashes of a join made one
// ID are two; a longer one cut, ended with a hash.
func TestClaimID(t *testing.T) {
	for _, tc := range []struct{ namespace, release, field, name, want string }{
		{"demo", "demo-app", "db", "", "demo.demo-app.db"},
		{"demo", "demo-app", "catalogs[1].database", "", "demo.demo-app.catalogs-1-database"},
		{"demo", "demo-app", `dbs["eu.main"]`, "", "demo.demo-app.dbs-eu-main"},
		{"demo", "demo-app", "db", "analytics", "demo.analytics"},
		{"demo", "a-b", "c", "", "demo.a-b.c"},
		{"demo", "a", "b-c", "", "demo.a.b-c"},
		{"demo", "app", "db", "", "demo.app.db"},
		{"demo", "", "", "app-db", "demo.app-db"},
	} {
		if got := claimID(tc.namespace, tc.release, tc.field, tc.name); got != tc.want {
			t.Errorf("got %q, want %q", got, tc.want)
		}
	}
	long := claimID("demo", strings.Repeat("a", 53), "database", "")
	if len(long) != 63 || !strings.HasPrefix(long, "demo."+strings.Repeat("a", 49)+"-") || !chart.IsClaimID(long) || long == claimID("demo", strings.Repeat("a", 53), "databases", "") {
		t.Errorf("long: %q (%d)", long, len(long))
	}
	// Cut, the ID keeps its namespace whole, whatever its length up to
	// 54: the claimers' (checkClaims).
	for n := 1; n <= 54; n++ {
		ns := strings.Repeat("n", n)
		for _, id := range []string{claimID(ns, strings.Repeat("a", 60), "db", ""), claimID(ns, "", "", strings.Repeat("b", 63))} {
			if len(id) > 63 || !strings.HasPrefix(id, ns+".") || !chart.IsClaimID(id) {
				t.Errorf("a namespace of %d: %q (%d)", n, id, len(id))
			}
		}
	}
}

// TestCompileAllCredentials: the credentials of a claim of another
// namespace go through the platform's secret store, under <provider
// namespace>/<id>: the provider pushes its Secret (PushSecret, deleted
// from the store with the claim), the claimer reads a copy of its own
// (ExternalSecret), both through the ClusterSecretStore, or the
// SecretStore of their namespace when the platform gives the store's
// provider, at the platform's refresh interval; none within a namespace;
// without the store, the provider refused, and so its claimer.
func TestCompileAllCredentials(t *testing.T) {
	contracts := fstest.MapFS{"server.schema.json": {Data: []byte(`{"properties": {"host": {"type": "string"}, "password": {"type": "string", "x-okdp-secret": true}}, "required": ["host"]}`)}}
	d := *pgDef
	d.Okdp = []byte(`
provides:
  - $for: c in claims
    $each: {contract: server, name: '${{ c.id }}', values: {host: h}, secretRef: {name: '${{ c.id }}-credentials'}}
`)
	store := M{"okdp": M{"secrets": M{"store": "okdp-secrets"}}}
	with := func(inst Instance, global M) Instance {
		inst.Inputs.Contracts = contracts
		if global != nil {
			inst.Inputs.Context = append(inst.Inputs.Context, ContextLayer{Values: M{"global": global}})
		}
		return inst
	}
	kinds := func(o Outcome) string {
		if o.Err != nil {
			return "error: " + o.Err.Error()
		}
		var s []string
		for _, obj := range o.Result.Objects {
			s = append(s, fmt.Sprintf("%s %s/%s %v", obj["kind"], value.Dig(obj, "metadata.namespace"), value.Dig(obj, "metadata.name"), obj["spec"]))
			// The claimer's copy is labelled as the provider's Secret is,
			// the ExternalSecret and the Secret it writes.
			if obj["kind"] == "ExternalSecret" {
				l, tl := value.AsMap(value.Dig(obj, "metadata.labels")), value.AsMap(value.Dig(obj, "spec.target.template.metadata.labels"))
				s[len(s)-1] += fmt.Sprintf(" labelled %v %v", l["okdp.io/credentials-server"], tl["okdp.io/credentials-server"])
			}
		}
		return strings.Join(s, "; ")
	}
	for name, tc := range map[string]struct {
		global, namespace string
		pg, app           string
	}{
		"across namespaces": {
			"store", "data",
			"PushSecret data/demo.demo-app.db map[data:[map[match:map[remoteRef:map[property:password remoteKey:data/demo.demo-app.db] secretKey:password]]] deletionPolicy:Delete refreshInterval:1h secretStoreRefs:[map[kind:ClusterSecretStore name:okdp-secrets]] selector:map[secret:map[name:demo.demo-app.db-credentials]] updatePolicy:Replace]",
			"ExternalSecret demo/demo-app-demo.demo-app.db-credentials map[data:[map[remoteRef:map[key:data/demo.demo-app.db property:password] secretKey:password]] refreshInterval:1h secretStoreRef:map[kind:ClusterSecretStore name:okdp-secrets] target:map[creationPolicy:Owner name:demo-app-demo.demo-app.db-credentials template:map[metadata:map[labels:map[app.kubernetes.io/instance:demo-app app.kubernetes.io/managed-by:Helm app.kubernetes.io/name:demo app.kubernetes.io/part-of:okdp okdp.io/credentials-server:true okdp.io/instance:demo-app okdp.io/service:demo]]]]] labelled true true",
		},
		"a SecretStore of each namespace": {
			"provider", "data",
			"PushSecret data/demo.demo-app.db map[data:[map[match:map[remoteRef:map[property:password remoteKey:data/demo.demo-app.db] secretKey:password]]] deletionPolicy:Delete refreshInterval:30m secretStoreRefs:[map[kind:SecretStore name:okdp-secrets]] selector:map[secret:map[name:demo.demo-app.db-credentials]] updatePolicy:Replace]",
			"ExternalSecret demo/demo-app-demo.demo-app.db-credentials map[data:[map[remoteRef:map[key:data/demo.demo-app.db property:password] secretKey:password]] refreshInterval:30m secretStoreRef:map[kind:SecretStore name:okdp-secrets] target:map[creationPolicy:Owner name:demo-app-demo.demo-app.db-credentials template:map[metadata:map[labels:map[app.kubernetes.io/instance:demo-app app.kubernetes.io/managed-by:Helm app.kubernetes.io/name:demo app.kubernetes.io/part-of:okdp okdp.io/credentials-server:true okdp.io/instance:demo-app okdp.io/service:demo]]]]] labelled true true",
		},
		"one namespace": {"store", "demo", "", ""},
		"no store": {
			"", "data",
			`error: claim "demo.demo-app.db" (server): claimed of namespace demo: its credentials go through the platform's secret store, global.okdp.secrets.store, which is not set`,
			`error: db: claim "demo.demo-app.db" (server): data/pg publishes no output for it (data/pg does not compile either: what it publishes is unknown)`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			var global M
			switch tc.global {
			case "store":
				global = store
			case "provider":
				global = M{"okdp": M{"secrets": M{"store": "okdp-secrets", "refreshInterval": "30m", "provider": M{"vault": M{"server": "http://openbao.openbao.svc:8200", "path": "okdp", "version": "v2"}}}}}
			}
			out := CompileAll(context.Background(), []Instance{
				with(claimInstance(&d, tc.namespace, "pg", M{}), global),
				with(withDefault(claimInstance(appDef, "demo", "demo-app", M{"db": M{}}), tc.namespace+"/pg"), global),
			})
			if got := kinds(out[0]); got != tc.pg {
				t.Errorf("pg: got  %s\nwant %s", got, tc.pg)
			}
			if got := kinds(out[1]); got != tc.app {
				t.Errorf("app: got  %s\nwant %s", got, tc.app)
			}
		})
	}
}

// writerDef: nodeDef, whose instances also write the objects of writes.
var writerDef = func() *Definition {
	files := maps.Clone(pkg)
	files["config.schema.json"] = &fstest.MapFile{Data: []byte(`{"properties": {
		"replicas": {"type": "integer", "default": 1},
		"uses": {"type": "array", "default": [], "items": {"type": "string", "x-okdp-connection-ref": {"contract": "server"}}},
		"publishes": {"type": "array", "default": [], "items": {"type": "string"}},
		"writes": {"type": "array", "default": []}}}`)}
	d := withFiles(files)
	d.Okdp = []byte(`
objects:
  - {$for: w in values.writes, $each: '${{ w }}'}
provides:
  - $for: n in values.publishes
    $each: {contract: server, name: '${{ n }}', values: {host: '${{ n }}'}}
`)
	return d
}()

// TestCompileAllSecretTargets: two objects of a namespace that write one
// Secret, of one instance or of two, refuse the instances that hold them,
// each error naming every writer, and those that reference their
// outputs: a v1 Secret, an ExternalSecret's target (its name when it has
// none, none with creationPolicy None), a PushSecret's key of the store
// and property. Another namespace's Secret of the name is another.
func TestCompileAllSecretTargets(t *testing.T) {
	writer := func(ns, name string, values M) Instance {
		inst := node(name, values)
		inst.Definition, inst.Inputs.Namespace, inst.Inputs.Release = writerDef, ns, ns+"-"+name
		return inst
	}
	es := func(name, target, policy string) M {
		spec := M{"secretStoreRef": M{"name": "okdp-secrets"}}
		if target != "" {
			spec["target"] = M{"name": target, "creationPolicy": policy}
		}
		return M{"apiVersion": "external-secrets.io/v1", "kind": "ExternalSecret", "metadata": M{"name": name}, "spec": spec}
	}
	secret := func(name string) M {
		return M{"apiVersion": "v1", "kind": "Secret", "metadata": M{"name": name}, "type": "Opaque"}
	}
	push := func(name, key, property string) M {
		return M{"apiVersion": "external-secrets.io/v1alpha1", "kind": "PushSecret", "metadata": M{"name": name},
			"spec": M{"selector": M{"secret": M{"name": "s"}}, "data": []any{M{"match": M{"secretKey": "k", "remoteRef": M{"remoteKey": key, "property": property}}}}}}
	}
	for name, tc := range map[string]struct {
		instances []Instance
		want      map[string]string
	}{
		"two instances": {
			[]Instance{
				writer("demo", "a", M{"writes": []any{es("es-a", "s", "Owner")}, "publishes": []any{"a"}}),
				writer("demo", "b", M{"writes": []any{secret("s")}}),
				writer("demo", "c", M{"writes": []any{es("es-c", "s", "None")}}),
				writer("demo", "d", M{"uses": []any{"a"}}),
				writer("other", "a", M{"writes": []any{secret("s")}}),
			},
			map[string]string{
				"demo-a":  `error: Secret "s" of namespace demo is written by ExternalSecret es-a of demo-a and Secret s of demo-b: two objects may not write one Secret`,
				"demo-b":  `error: Secret "s" of namespace demo is written by ExternalSecret es-a of demo-a and Secret s of demo-b: two objects may not write one Secret`,
				"demo-c":  "",
				"demo-d":  `error: uses[0]: "a" is not a connection, nor an output of an instance of the namespace (demo-a, demo-b do not compile either: what they publish is unknown)`,
				"other-a": "",
			},
		},
		"one instance": {
			[]Instance{writer("demo", "a", M{"writes": []any{es("x", "s", "Owner"), es("s", "", ""), es("y", "t", "Merge")}})},
			map[string]string{"demo-a": `error: Secret "s" of namespace demo is written by ExternalSecret s of demo-a and ExternalSecret x of demo-a: two objects may not write one Secret`},
		},
		"one object twice": {
			[]Instance{writer("demo", "a", M{"writes": []any{secret("s")}}), writer("demo", "b", M{"writes": []any{secret("s"), secret("s")}})},
			map[string]string{
				"demo-a": `error: Secret "s" of namespace demo is written by Secret s of demo-a and Secret s of demo-b (2 times): two objects may not write one Secret`,
				"demo-b": `error: Secret "s" of namespace demo is written by Secret s of demo-a and Secret s of demo-b (2 times): two objects may not write one Secret`,
			},
		},
		"an item of a List": {
			[]Instance{
				writer("demo", "a", M{"writes": []any{M{"apiVersion": "v1", "kind": "List", "metadata": M{"name": "l"}, "items": []any{M{"apiVersion": "v1", "kind": "List", "items": []any{es("same", "same", "Owner")}}}}}}),
				writer("demo", "b", M{"writes": []any{es("same", "same", "Owner")}}),
			},
			map[string]string{
				"demo-a": `error: Secret "same" of namespace demo is written by ExternalSecret same of demo-a and ExternalSecret same of demo-b: two objects may not write one Secret`,
				"demo-b": `error: Secret "same" of namespace demo is written by ExternalSecret same of demo-a and ExternalSecret same of demo-b: two objects may not write one Secret`,
			},
		},
		"a key of the store": {
			[]Instance{
				writer("demo", "a", M{"writes": []any{push("p", "demo/k", "password"), push("q", "demo/k", "username")}}),
				writer("demo", "b", M{"writes": []any{push("p", "demo/k", "password")}}),
			},
			map[string]string{
				"demo-a": `error: the key demo/k (property password) of the secret store of namespace demo is written by PushSecret p of demo-a and PushSecret p of demo-b: two objects may not write one Secret`,
				"demo-b": `error: the key demo/k (property password) of the secret store of namespace demo is written by PushSecret p of demo-a and PushSecret p of demo-b: two objects may not write one Secret`,
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			for _, order := range permutations(tc.instances) {
				if got := outcomes(order); !reflect.DeepEqual(got, tc.want) {
					t.Fatalf("got  %v\nwant %v", got, tc.want)
				}
			}
		})
	}
}

// TestCompileAllDeclaredConnections: every connection of an instance's
// context is checked at its declaration, referenced or not, each that
// fails at connections.<name>: the instance is refused, the others of
// another context are not.
func TestCompileAllDeclaredConnections(t *testing.T) {
	bad := node("a", nil)
	bad.Inputs.Context = append(bad.Inputs.Context, ContextLayer{Level: 1, Values: M{"connections": M{
		"Bad_Name":  M{"contract": "server", "host": "h"},
		"no-host":   M{"contract": "server"},
		"unknown":   M{"contract": "nothing"},
		"field":     M{"contract": "server", "host": "h", "port": 1.0},
		"none":      M{"host": "h"},
		"scalar":    "x",
		"other-key": M{"contract": "server", "host": "h", "secretRef": M{"remoteKey": "platform/x"}},
		"good":      M{"contract": "server", "host": "h"},
	}}})
	good := node("b", nil)
	good.Inputs.Namespace = "other"
	out := CompileAll(context.Background(), []Instance{bad, good})
	if out[1].Err != nil {
		t.Fatalf("an instance of another context: %v", out[1].Err)
	}
	want := []string{
		`connections.Bad_Name: a connection's name is a DNS-1123 label, lowercase letters, digits and "-", 63 characters at most`,
		`connections.field: connection "field": field "port" is not part of the server contract`,
		`connections.no-host (connection "no-host"): field "host" is required by the server contract`,
		`connections.none: contract is required, the contract of the connection`,
		`connections.other-key: connection "other-key": secretRef.remoteKey: its credentials are in the platform's secret store, global.okdp.secrets.store, which is not set`,
		`connections.scalar: a connection, an object of its contract's fields, got "x"`,
		`connections.unknown: the nothing contract cannot be read: nothing.schema.json: file does not exist`,
	}
	if err := out[0].Err; err == nil || err.Error() != strings.Join(want, "\n") {
		t.Fatalf("got\n%v\nwant\n%s", err, strings.Join(want, "\n"))
	}
	var fe *FieldError
	if !errors.As(out[0].Err, &fe) || fe.Field != "connections.Bad_Name" {
		t.Errorf("not a field's: %#v", out[0].Err)
	}
}
