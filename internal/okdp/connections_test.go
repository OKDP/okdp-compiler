package okdp

import (
	"context"
	"maps"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/okdp/okdp-compiler/internal/value"
)

// dbContract: a database contract with defaults, a derived field, a
// conditional default, a secret field (required: it is in the Secret, never
// set), a required boolean, and a list.
var dbContract = fstest.MapFS{"database.schema.json": {Data: []byte(`{
	"x-okdp-contract": "database",
	"x-okdp-internal": {"host": "${ref}-db.${namespace}.svc", "url": "https://${ref}.${suffix}"},
	"properties": {
		"host": {"type": "string"},
		"url": {"type": "string"},
		"engine": {"type": "string", "enum": ["postgresql", "mysql"], "default": "postgresql"},
		"tags": {"type": "array", "items": {"type": "string"}},
		"driver": {"type": "string", "x-okdp-derived": {"from": "engine", "map": {"postgresql": "pg", "mysql": "my"}}},
		"ssl": {"type": "string", "default": "require", "x-ui-condition": {"field": "engine", "value": "postgresql"}},
		"tls": {"type": "boolean", "default": false},
		"password": {"type": "string", "x-okdp-secret": true}
	},
	"required": ["host", "tls", "password"]}`)}}

// resolveSchema: parameters with references in a nested object, in a list
// of references, and one of any type.
const resolveSchema = `{"properties": {
	"main": {"properties": {"db": {"type": "string", "x-okdp-connection-ref": {"contract": "database"}}}},
	"replicas": {"type": "array", "items": {"type": "string", "x-okdp-connection-ref": {"contract": "database"}}},
	"untyped": {"x-okdp-connection-ref": {"contract": "database"}},
	"other": {"type": "string"}}}`

// resolveCtx: the context of the in-memory package with resolveSchema, the
// instance's values over these context layers.
func resolveCtx(values M, platform ...ContextLayer) (M, []Dependency, error) {
	ch := maps.Clone(pkg)
	ch["config.schema.json"] = &fstest.MapFile{Data: []byte(resolveSchema)}
	in := inputs(values)
	in.Context = append(in.Context, platform...)
	in.Contracts = dbContract
	inst, err := newInstance(ch, in, defService)
	if err != nil {
		return nil, nil, err
	}
	return value.AsMap(inst.scope["values"]), inst.deps, nil
}

// TestResolve: internal and external references, completed against the contract.
func TestResolve(t *testing.T) {
	values, deps, err := resolveCtx(M{
		"main":     M{"db": "demo-pg"},
		"replicas": []any{"ext", ""},
		"untyped":  nil,
		"other":    "demo-pg",
		"connections": M{"ext": M{
			"contract": "database", "description": "a replica", "engine": "mysql",
			"host": "replica.example.org", "tls": false, "secretRef": M{"name": "ext-creds", "key": "dropped"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	internal := M{
		"host": "demo-pg-db.demo.svc", "url": "https://demo-pg.example.org",
		"engine": "postgresql", "driver": "pg", "ssl": "require", "tls": false,
		"secretRef": M{"name": "demo-pg-database-credentials"},
		"okdp":      M{"ref": "demo-pg", "contract": "database", "source": "internal", "instance": "demo-pg"},
	}
	external := M{
		"host": "replica.example.org", "engine": "mysql", "driver": "my", "tls": false,
		"secretRef": M{"name": "ext-creds"},
		"okdp":      M{"ref": "ext", "contract": "database", "source": "external"},
	}
	if got := value.AsMap(value.AsMap(values["main"])["db"]); !reflect.DeepEqual(got, internal) {
		t.Errorf("internal:\n got %#v\nwant %#v", got, internal)
	}
	if got := value.AsMap(values["replicas"].([]any)[0]); !reflect.DeepEqual(got, external) {
		t.Errorf("external:\n got %#v\nwant %#v", got, external)
	}
	if values["replicas"].([]any)[1] != "" {
		t.Error("an empty reference is left as it is")
	}
	if v, ok := values["untyped"]; !ok || v != nil {
		t.Error("a null reference is left as it is")
	}
	if values["other"] != "demo-pg" {
		t.Error("a field without x-okdp-connection-ref is left as it is")
	}
	if want := []Dependency{{Instance: "demo-pg", Contract: "database", Field: "main.db", Output: "demo-pg"}}; !reflect.DeepEqual(deps, want) {
		t.Errorf("dependencies: %#v", deps)
	}
}

// TestResolveGoTypes: values a Go caller builds with other types than M and
// []any resolve as the same values read from a file: a []string of
// references passes the schema (type: array), a map[string]string entry of
// connections is an external connection.
func TestResolveGoTypes(t *testing.T) {
	values, deps, err := resolveCtx(M{
		"replicas":    []string{"ext"},
		"connections": map[string]map[string]string{"ext": {"contract": "database", "host": "replica.example.org"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := M{
		"host": "replica.example.org", "engine": "postgresql", "driver": "pg", "ssl": "require", "tls": false,
		"okdp": M{"ref": "ext", "contract": "database", "source": "external"},
	}
	if got := value.AsList(values["replicas"]); len(got) != 1 || !reflect.DeepEqual(got[0], want) || len(deps) > 0 {
		t.Errorf("got %#v, dependencies %#v\nwant [%#v], none", got, deps, want)
	}
}

// TestResolveErrors: contract checks on references.
func TestResolveErrors(t *testing.T) {
	ext := func(fields M) M {
		c := M{"contract": "database", "host": "h", "tls": true}
		maps.Copy(c, fields)
		return M{"replicas": []any{"ext"}, "connections": M{"ext": c}}
	}
	for name, tc := range map[string]struct {
		values M
		want   string
	}{
		"required field": {ext(M{"host": ""}), `replicas[0] (connection "ext"): field "host" is required by the database contract`},
		"wrong contract": {ext(M{"contract": "s3"}), `replicas[0]: connection "ext" has contract "s3", "database" is expected`},
		"unknown field":  {ext(M{"port": 1}), `field "port" is not part of the database contract`},
		"secret field":   {ext(M{"password": "p"}), `replicas[0]: connection "ext": "password" is a secret field, put it in the credentials Secret`},
		"not a string":   {M{"replicas": []any{}, "untyped": 5}, `untyped: a connection reference must be a string, got 5`},
		"not a release":  {M{"replicas": []any{strings.Repeat("a", 54)}}, `replicas[0]: "` + strings.Repeat("a", 54) + `" is not a connection, nor a release name`},
		"a template":     {M{"replicas": []any{`{{ lookup "v1" "Secret" "demo" "" }}`}}, `values layer 1: replicas[0]: the value contains "{{"`},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := resolveCtx(tc.values)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

// TestResolveReport: every reference that does not resolve, or is not a
// string, is reported at once, one line each, in path order, the
// fields of a connection checked in key order: the same report from one
// compile to the next (the walk used to follow map order and stop at the
// first error).
func TestResolveReport(t *testing.T) {
	values := M{
		"main":     M{"db": "bad"},
		"replicas": []any{"demo-pg", "s3", "", "bad", "Demo_PG"},
		"untyped":  M{"host": "h"},
		"connections": M{
			"bad": M{"contract": "database", "host": "h", "tls": true, "zone": "a", "port": 1},
			"s3":  M{"contract": "s3"},
		},
	}
	want := `main.db: connection "bad": field "port" is not part of the database contract
replicas[1]: connection "s3" has contract "s3", "database" is expected
replicas[3]: connection "bad": field "port" is not part of the database contract
replicas[4]: "Demo_PG" is not a connection, nor a release name: lowercase letters, digits and "-", a letter or digit at each end, 53 characters at most
untyped: a connection reference must be a string, got map[string]interface {}{"host":"h"}`
	for range 20 {
		if _, _, err := resolveCtx(values); err == nil || err.Error() != want {
			t.Fatalf("got %v\nwant %s", err, want)
		}
	}
}

// TestResolveTypes: the fields of an external connection, completed,
// against the contract's types: every violation at once, one line each,
// in parameter path order then by field path. The secret field, required, is not
// in the values: not reported. An internal reference is not checked.
func TestResolveTypes(t *testing.T) {
	values := M{
		"main":     M{"db": "ext"},
		"replicas": []any{"demo-pg", "ext2"},
		"connections": M{
			"ext":  M{"contract": "database", "host": 1, "engine": "oracle"},
			"ext2": M{"contract": "database", "host": "h", "tls": "yes", "tags": []any{"a", 2}},
		},
	}
	want := `main.db (connection "ext"): the fields violate the database contract:
- at '/engine': value must be one of 'postgresql', 'mysql'
- at '/host': got number, want string
replicas[1] (connection "ext2"): the fields violate the database contract:
- at '/tags/1': got number, want string
- at '/tls': got string, want boolean`
	for range 20 {
		if _, _, err := resolveCtx(values); err == nil || err.Error() != want {
			t.Fatalf("got %v\nwant %s", err, want)
		}
	}
}

// TestResolveTemplate: "{{" in a resolved connection fails, whatever layer
// it comes from: here a connection of the platform's level, which the
// check of the instance's layers does not cover.
func TestResolveTemplate(t *testing.T) {
	platform := ContextLayer{Values: map[string]any{"connections": M{"ext": M{"contract": "database", "host": "{{ .Values.host }}", "tls": true}}}}
	want := `replicas[0] (connection "ext"): field "host": the value contains "{{"`
	if _, _, err := resolveCtx(M{"replicas": []any{"ext"}}, platform); err == nil || err.Error() != want {
		t.Errorf("got %v, want %q", err, want)
	}
}

// TestResolveUnreadableContract: a contract that is missing, cannot be
// read, parsed or compiled is reported as such, with its file and the cause.
func TestResolveUnreadableContract(t *testing.T) {
	ch := maps.Clone(pkg)
	ch["config.schema.json"] = &fstest.MapFile{Data: []byte(`{"properties": {"q": {"type": "string", "x-okdp-connection-ref": {"contract": "queue"}}}}`)}
	for name, tc := range map[string]struct {
		contracts fstest.MapFS
		want      string
	}{
		"missing":       {fstest.MapFS{}, `q: the queue contract cannot be read: queue.schema.json: file does not exist`},
		"not JSON":      {fstest.MapFS{"queue.schema.json": {Data: []byte(`{"properties": `)}}, `q: the queue contract cannot be read: queue.schema.json: unexpected EOF`},
		"not an object": {fstest.MapFS{"queue.schema.json": {Data: []byte(`[]`)}}, `q: the queue contract cannot be read: queue.schema.json: not a JSON object`},
		"not a schema": {
			fstest.MapFS{"queue.schema.json": {Data: []byte(`{"properties": {"port": {"type": 5}}}`)}},
			`q: the queue contract cannot be read: queue.schema.json: "file:///queue.schema.json#" is not valid against metaschema`,
		},
		"a directory": {fstest.MapFS{"queue.schema.json/x": {}}, `q: the queue contract cannot be read: queue.schema.json: invalid argument`},
	} {
		t.Run(name, func(t *testing.T) {
			in := inputs(M{"q": "demo-mq"})
			in.Contracts = tc.contracts
			if _, err := newInstance(ch, in, defService); err == nil || !strings.HasPrefix(err.Error(), tc.want) {
				t.Errorf("got %v, want an error starting with %q", err, tc.want)
			}
		})
	}
}

// outputContracts: dbContract's database, and server, a contract without a
// naming convention, whose outputs are named (cnpg-postgresql's
// database-server).
var outputContracts = fstest.MapFS{
	"database.schema.json": dbContract["database.schema.json"],
	"server.schema.json": {Data: []byte(`{
	"x-okdp-contract": "server",
	"properties": {
		"host": {"type": "string"},
		"port": {"type": "integer", "default": 5432},
		"user": {"type": "string"},
		"password": {"type": "string", "x-okdp-secret": true}
	},
	"required": ["host", "password"]}`)}}

// namespaceOutputs: what the instances of the namespace publish
// (Inputs.Published), as okdp.instance.yaml decodes, a number an int:
// demo-pg a server output with its secretRef, and its database output,
// named after it; demo-app, the compiling release, a server output.
var namespaceOutputs = map[string]Instance{
	"demo-pg": {Provides: []Output{
		{Name: "demo-pg-hive", Contract: "server", Values: M{"host": "demo-pg-rw.demo.svc", "port": 5433, "user": "hive"}, SecretRef: &SecretRef{Name: "demo-pg-hive-app"}},
		{Name: "demo-pg", Contract: "database", Values: M{"host": "demo-pg-db.demo.svc", "url": "https://demo-pg.example.org", "tls": true}},
	}},
	"demo-app": {Provides: []Output{{Name: "self", Contract: "server", Values: M{"host": "demo-app.demo.svc"}}}},
}

// outputCtx: the context of the in-memory package with a server reference,
// db, and a database one, main, these instances published.
func outputCtx(values M, published map[string]Instance) (M, []Dependency, error) {
	ch := maps.Clone(pkg)
	ch["config.schema.json"] = &fstest.MapFile{Data: []byte(`{"properties": {
		"db": {"type": "string", "x-okdp-connection-ref": {"contract": "server"}},
		"main": {"type": "string", "x-okdp-connection-ref": {"contract": "database"}}}}`)}
	in := inputs(values)
	in.Contracts, in.Published = outputContracts, published
	inst, err := newInstance(ch, in, defService)
	if err != nil {
		return nil, nil, err
	}
	return value.AsMap(inst.scope["values"]), inst.deps, nil
}

// TestResolveOutput: a reference to an output of an instance of the
// namespace resolves to its values, a copy, completed and read as values
// (an int a float64), and its secretRef; the instance is a dependency, the
// output named.
func TestResolveOutput(t *testing.T) {
	values, deps, err := outputCtx(M{"db": "demo-pg-hive"}, namespaceOutputs)
	if err != nil {
		t.Fatal(err)
	}
	want := M{
		"host": "demo-pg-rw.demo.svc", "port": float64(5433), "user": "hive",
		"secretRef": M{"name": "demo-pg-hive-app"},
		"okdp":      M{"ref": "demo-pg-hive", "contract": "server", "source": "output", "instance": "demo-pg"},
	}
	if !reflect.DeepEqual(values["db"], want) {
		t.Errorf("got %#v\nwant %#v", values["db"], want)
	}
	if want := []Dependency{{Instance: "demo-pg", Contract: "server", Field: "db", Output: "demo-pg-hive"}}; !reflect.DeepEqual(deps, want) {
		t.Errorf("dependencies: %#v", deps)
	}
	if v := namespaceOutputs["demo-pg"].Provides[0].Values["port"]; v != 5433 {
		t.Errorf("Inputs.Published changed: port %#v", v)
	}
}

// TestResolveOrder: a connection first, then, of a contract without a
// naming convention, an output; of a contract with one, the convention,
// never an output, with or without Published.
func TestResolveOrder(t *testing.T) {
	conn := M{"demo-pg-hive": M{"contract": "server", "host": "h"}}
	for name, tc := range map[string]struct {
		values    M
		published map[string]Instance
		field     string
		want      M // okdp
		deps      []Dependency
	}{
		"a connection first": {
			M{"db": "demo-pg-hive", "connections": conn}, namespaceOutputs, "db",
			M{"ref": "demo-pg-hive", "contract": "server", "source": "external"}, nil,
		},
		"the convention, not the output": {
			M{"main": "demo-pg"}, namespaceOutputs, "main",
			M{"ref": "demo-pg", "contract": "database", "source": "internal", "instance": "demo-pg"},
			[]Dependency{{Instance: "demo-pg", Contract: "database", Field: "main", Output: "demo-pg"}},
		},
		"the convention, no Published": {
			M{"main": "demo-pg"}, nil, "main",
			M{"ref": "demo-pg", "contract": "database", "source": "internal", "instance": "demo-pg"},
			[]Dependency{{Instance: "demo-pg", Contract: "database", Field: "main", Output: "demo-pg"}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			values, deps, err := outputCtx(tc.values, tc.published)
			if err != nil {
				t.Fatal(err)
			}
			if got := value.AsMap(values[tc.field])["okdp"]; !reflect.DeepEqual(got, tc.want) || !reflect.DeepEqual(deps, tc.deps) {
				t.Errorf("got %#v, dependencies %#v\nwant %#v, %#v", got, deps, tc.want, tc.deps)
			}
		})
	}
}

// TestResolveOutputErrors: an output several instances publish; a name
// that resolves to nothing, Published given or not, the compiling release
// left out, an output of another contract said; an output the contract
// refuses; the errors of a contract with a naming convention, Published
// unread.
func TestResolveOutputErrors(t *testing.T) {
	server := func(release string, values M) map[string]Instance {
		return map[string]Instance{release: {Provides: []Output{{Name: "shared", Contract: "server", Values: values}}}}
	}
	twice := server("demo-b", M{"host": "b"})
	maps.Copy(twice, server("demo-a", M{"host": "a"}))
	for name, tc := range map[string]struct {
		values    M
		published map[string]Instance
		want      string
	}{
		"two instances": {M{"db": "shared"}, twice, `db: "shared" names server outputs of several instances of the namespace: demo-a, demo-b`},
		"the compiling release": {
			M{"db": "self"}, namespaceOutputs,
			`db: "self" is not a connection, nor an output of an instance of the namespace, and the server contract has no internal naming convention`,
		},
		"another contract": {
			M{"db": "demo-pg"}, namespaceOutputs,
			`db: "demo-pg" is not a connection, nor an output of an instance of the namespace (demo-pg publishes it with contract "database", "server" is expected), and the server contract has no internal naming convention`,
		},
		"not a release name": {
			M{"main": "Demo_PG"}, namespaceOutputs,
			`main: "Demo_PG" is not a connection, nor a release name: lowercase letters, digits and "-", a letter or digit at each end, 53 characters at most`,
		},
		"no Published":  {M{"db": "demo-pg-hive"}, nil, `db: "demo-pg-hive" is not a connection, and the server contract has no internal naming convention`},
		"unknown field": {M{"db": "shared"}, server("demo-a", M{"host": "a", "zone": "z"}), `db: output "shared" of demo-a: field "zone" is not part of the server contract`},
		"types": {
			M{"db": "shared"}, server("demo-a", M{"host": "a", "port": "5432"}),
			"db (output \"shared\" of demo-a): the fields violate the server contract:\n- at '/port': got string, want integer",
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := outputCtx(tc.values, tc.published); err == nil || err.Error() != tc.want {
				t.Errorf("got %v\nwant %s", err, tc.want)
			}
		})
	}
}

// TestCompleteUntilStable: a conditional default whose condition field sorts
// after it, or is derived, applies: the defaults are applied until nothing
// changes, whatever the order of the fields.
func TestCompleteUntilStable(t *testing.T) {
	c := testContract(t, `{"properties": {
		"auth":   {"default": "md5", "x-ui-condition": {"field": "engine", "value": "postgresql"}},
		"batch":  {"default": true, "x-ui-condition": {"field": "driver", "value": "pg"}},
		"driver": {"x-okdp-derived": {"from": "engine", "map": {"postgresql": "pg", "mysql": "my"}}},
		"engine": {"default": "postgresql"}}}`)
	for _, tc := range []struct{ in, want M }{
		{M{}, M{"auth": "md5", "batch": true, "driver": "pg", "engine": "postgresql"}},
		{M{"engine": "mysql"}, M{"driver": "my", "engine": "mysql"}},
	} {
		if err := c.complete(tc.in, "x"); err != nil || !reflect.DeepEqual(tc.in, tc.want) {
			t.Errorf("got %v %v, want %v", tc.in, err, tc.want)
		}
	}
}

// TestCompleteRequired: every required field not set is reported at once,
// one line each, in alphabetical order, not in the contract's; false is a
// value, a secret field is never set.
func TestCompleteRequired(t *testing.T) {
	c := testContract(t, `{"x-okdp-contract": "database", "required": ["user", "tls", "host", "password"], "properties": {
		"host": {}, "tls": {}, "user": {}, "password": {"x-okdp-secret": true}}}`)
	want := `x: field "host" is required by the database contract
x: field "user" is required by the database contract`
	if err := c.complete(M{"tls": false, "user": ""}, "x"); err == nil || err.Error() != want {
		t.Errorf("got %v\nwant %s", err, want)
	}
}

// testContract: the contract of this schema, read as a compile reads it
// (readContract).
func testContract(t *testing.T, schema string) *contract {
	t.Helper()
	c, err := readContract(fstest.MapFS{"database.schema.json": {Data: []byte(schema)}}, "database")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// TestCompileOutputErrors: values.cel.yaml may only give the values of
// dependencies of Chart.yaml, and their defaults file must exist.
func TestCompileOutputErrors(t *testing.T) {
	d := *def
	d.Values = []byte("db: {}\n")
	if _, err := Compile(context.Background(), &d, inputs()); err == nil || !strings.Contains(err.Error(), "values.cel.yaml: db: not a dependency of Chart.yaml") {
		t.Errorf("unknown chart: got %v", err)
	}
	d = *def
	d.Package.Defaults = map[string]string{"app": "missing.defaults.yaml"}
	if _, err := Compile(context.Background(), &d, inputs()); err == nil || !strings.Contains(err.Error(), "app: open missing.defaults.yaml") {
		t.Errorf("missing defaults file: got %v", err)
	}
}
