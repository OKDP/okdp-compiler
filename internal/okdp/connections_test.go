package okdp

import (
	"maps"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/okdp/okdp-compiler/internal/chart"
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
// instance's values over these platform layers.
func resolveCtx(values M, platform ...map[string]any) (M, []Dependency, error) {
	ch := maps.Clone(pkg)
	ch["values.schema.json"] = &fstest.MapFile{Data: []byte(resolveSchema)}
	in := inputs(values)
	in.Platform = append(in.Platform, platform...)
	in.Contracts = dbContract
	ctx, _, _, deps, err := buildContext(ch, in, &def.Service)
	if err != nil {
		return nil, nil, err
	}
	return value.AsMap(ctx["values"]), deps, nil
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
		"okdp":      M{"ref": "demo-pg", "contract": "database", "source": "internal"},
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
	if want := []Dependency{{Instance: "demo-pg", Contract: "database", Field: "main.db"}}; !reflect.DeepEqual(deps, want) {
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
		"secret field":   {ext(M{"password": "p"}), `"password" is a secret field`},
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
// string, is reported at once, one line each, sorted by parameter path, the
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
// sorted by parameter path then field. The secret field, required, is not
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
	want := `main.db (connection "ext"): field "engine" violates the database contract: value must be one of 'postgresql', 'mysql'
main.db (connection "ext"): field "host" violates the database contract: got number, want string
replicas[1] (connection "ext2"): field "tags[1]" violates the database contract: got number, want string
replicas[1] (connection "ext2"): field "tls" violates the database contract: got string, want boolean`
	for range 20 {
		if _, _, err := resolveCtx(values); err == nil || err.Error() != want {
			t.Fatalf("got %v\nwant %s", err, want)
		}
	}
}

// TestResolveTemplate: "{{" in a resolved connection fails, whatever layer
// it comes from: here a connection of the platform's layers, which the
// check of the instance's layers does not cover.
func TestResolveTemplate(t *testing.T) {
	platform := map[string]any{"connections": M{"ext": M{"contract": "database", "host": "{{ .Values.host }}", "tls": true}}}
	want := `replicas[0] (connection "ext"): field "host": the value contains "{{"`
	if _, _, err := resolveCtx(M{"replicas": []any{"ext"}}, platform); err == nil || err.Error() != want {
		t.Errorf("got %v, want %q", err, want)
	}
}

// TestResolveUnknownContract: a schema that references a missing contract.
func TestResolveUnknownContract(t *testing.T) {
	ch := maps.Clone(pkg)
	ch["values.schema.json"] = &fstest.MapFile{Data: []byte(`{"properties": {"q": {"type": "string", "x-okdp-connection-ref": {"contract": "queue"}}}}`)}
	if _, _, _, _, err := buildContext(ch, inputs(M{"q": "demo-mq"}), &def.Service); err == nil || !strings.Contains(err.Error(), `q: unknown contract "queue"`) {
		t.Errorf("got %v", err)
	}
}

// TestResolveUnreadableContract: a contract that is there but cannot be
// read, parsed or compiled is reported as such, with its file and the cause.
func TestResolveUnreadableContract(t *testing.T) {
	ch := maps.Clone(pkg)
	ch["values.schema.json"] = &fstest.MapFile{Data: []byte(`{"properties": {"q": {"type": "string", "x-okdp-connection-ref": {"contract": "queue"}}}}`)}
	for name, tc := range map[string]struct {
		contracts fstest.MapFS
		want      string
	}{
		"not JSON":      {fstest.MapFS{"queue.schema.json": {Data: []byte(`{"properties": `)}}, `q: the queue contract cannot be read: queue.schema.json: unexpected end of JSON input`},
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
			if _, _, _, _, err := buildContext(ch, in, &def.Service); err == nil || !strings.HasPrefix(err.Error(), tc.want) {
				t.Errorf("got %v, want an error starting with %q", err, tc.want)
			}
		})
	}
}

// TestCompleteUntilStable: a conditional default whose condition field sorts
// after it, or is derived, applies: the defaults are applied until nothing
// changes, whatever the order of the fields.
func TestCompleteUntilStable(t *testing.T) {
	schema := M{"properties": M{
		"auth":   M{"default": "md5", "x-ui-condition": M{"field": "engine", "value": "postgresql"}},
		"batch":  M{"default": true, "x-ui-condition": M{"field": "driver", "value": "pg"}},
		"driver": M{"x-okdp-derived": M{"from": "engine", "map": M{"postgresql": "pg", "mysql": "my"}}},
		"engine": M{"default": "postgresql"},
	}}
	for _, tc := range []struct{ in, want M }{
		{M{}, M{"auth": "md5", "batch": true, "driver": "pg", "engine": "postgresql"}},
		{M{"engine": "mysql"}, M{"driver": "my", "engine": "mysql"}},
	} {
		if err := complete(schema, tc.in, "x"); err != nil || !reflect.DeepEqual(tc.in, tc.want) {
			t.Errorf("got %v %v, want %v", tc.in, err, tc.want)
		}
	}
}

// TestCompileOutputErrors: the mapping may only output charts of
// service.yaml, and their base values file must exist.
func TestCompileOutputErrors(t *testing.T) {
	d := *def
	d.Mapping = []byte("output: {db: {}}\n")
	if _, err := Compile(&d, inputs()); err == nil || !strings.Contains(err.Error(), `mapping output "db": not a chart of service.yaml`) {
		t.Errorf("unknown chart: got %v", err)
	}
	d = *def
	d.Service.Charts = chart.Specs{"app": {Source: appSource, Base: "base/missing.yaml"}}
	if _, err := Compile(&d, inputs()); err == nil || !strings.Contains(err.Error(), "app: open base/missing.yaml") {
		t.Errorf("missing base values: got %v", err)
	}
}
