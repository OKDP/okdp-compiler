package okdp

import (
	"context"
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
// another namespace; a release given twice in one is refused, each time;
// a Published given is not read.
func TestCompileAllNamespaces(t *testing.T) {
	other := func(inst Instance) Instance {
		inst.Inputs.Namespace = "other"
		return inst
	}
	given := node("b", M{"uses": []any{"z"}})
	given.Inputs.Published = map[string]Info{"demo-z": {Provides: []Output{{Name: "z", Contract: "server", Values: M{"host": "z"}}}}}
	instances := []Instance{
		node("a", M{"publishes": []any{"x"}}), other(node("a", M{"publishes": []any{"x"}})),
		other(node("c", M{"uses": []any{"x"}})), given,
		other(node("t", M{"publishes": []any{"t"}})), other(node("t", M{})),
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
		"demo/demo-a", "other/demo-a", "other/demo-c",
		`uses[0]: "z" is not a connection, nor an output of an instance of the namespace`,
		`release "demo-t" is given twice in namespace "other"`, `release "demo-t" is given twice in namespace "other"`,
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
	d.Package.Provisions = []string{"server"}
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
	d.Package.Provisions = []string{"other"}
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
				"demo/demo-a": `error: db: claim (server): the default class "standard" (global.okdp.provisioning.server.classes.standard): provider "a/b/c": <release> or <namespace>/<release>`,
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
}

// TestCompileAllCredentials: the credentials of a claim of another
// namespace go through the platform's secret store: the provider pushes
// its Secret (PushSecret), the claimer reads a copy of its own
// (ExternalSecret); none within a namespace; without the store, the
// provider refused, and so its claimer.
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
			"PushSecret data/demo.demo-app.db map[data:[map[match:map[remoteRef:map[property:password remoteKey:okdp.demo.demo-app.db] secretKey:password]]] deletionPolicy:None refreshInterval:1h secretStoreRefs:[map[kind:ClusterSecretStore name:okdp-secrets]] selector:map[secret:map[name:demo.demo-app.db-credentials]] updatePolicy:Replace]",
			"ExternalSecret demo/demo-app-demo.demo-app.db-credentials map[data:[map[remoteRef:map[key:okdp.demo.demo-app.db property:password] secretKey:password]] refreshInterval:1h secretStoreRef:map[kind:ClusterSecretStore name:okdp-secrets] target:map[creationPolicy:Owner name:demo-app-demo.demo-app.db-credentials template:map[metadata:map[labels:map[app.kubernetes.io/instance:demo-app app.kubernetes.io/managed-by:Helm app.kubernetes.io/name:demo app.kubernetes.io/part-of:okdp okdp.io/credentials-server:true okdp.io/instance:demo-app okdp.io/service:demo]]]]] labelled true true",
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
			if tc.global != "" {
				global = store
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
