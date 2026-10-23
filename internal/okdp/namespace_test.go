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
)

// nodeContracts: server, a contract without a naming convention, its
// outputs named, and database, one with.
var nodeContracts = fstest.MapFS{
	"server.schema.json":   {Data: []byte(`{"x-okdp-contract": "server", "properties": {"host": {"type": "string"}}, "required": ["host"]}`)},
	"database.schema.json": {Data: []byte(`{"x-okdp-contract": "database", "x-okdp-internal": {"host": "${ref}-db.${namespace}.svc"}, "properties": {"host": {"type": "string"}}, "required": ["host"]}`)},
}

// nodeDef: def, whose instances reference server outputs (uses) and a
// database by its convention (db), publish server outputs (publishes),
// each with its name after the host of the first it uses, and fail on
// demand (fail).
var nodeDef = func() *Definition {
	files := maps.Clone(pkg)
	files["config.schema.json"] = &fstest.MapFile{Data: []byte(`{"properties": {
		"replicas": {"type": "integer", "default": 1},
		"uses": {"type": "array", "default": [], "items": {"type": "string", "x-okdp-connection-ref": {"contract": "server"}}},
		"db": {"type": "string", "x-okdp-connection-ref": {"contract": "database"}},
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
// same outcomes: a chain, consumers first; references by convention,
// never waited for; an output nobody publishes; a provider that fails, a
// cycle of outputs, whose consumers say what does not compile; an output
// two instances publish, and what resolved through it.
func TestCompileAll(t *testing.T) {
	missing := func(path, ref string) string {
		return fmt.Sprintf(`error: %s: %q is not a connection, nor an output of an instance of the namespace, and the server contract has no internal naming convention`, path, ref)
	}
	for name, tc := range map[string]struct {
		instances []Instance
		want      map[string]string
	}{
		"a chain": {
			[]Instance{node("c", M{"uses": []any{"b"}, "publishes": []any{"c"}}), node("b", M{"uses": []any{"a"}, "publishes": []any{"b"}}), node("a", M{"publishes": []any{"a"}})},
			map[string]string{"demo-a": "a", "demo-b": "a/b", "demo-c": "a/b/c"},
		},
		"by convention": {
			[]Instance{node("a", M{"db": "demo-b"}), node("b", M{"db": "demo-a"})},
			map[string]string{"demo-a": "", "demo-b": ""},
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
		`uses[0]: "z" is not a connection, nor an output of an instance of the namespace, and the server contract has no internal naming convention`,
		`release "demo-t" is given twice in namespace "other"`, `release "demo-t" is given twice in namespace "other"`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}
