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
// set), and a required boolean.
var dbContract = fstest.MapFS{"database.schema.json": {Data: []byte(`{
	"x-okdp-contract": "database",
	"x-okdp-internal": {"host": "${ref}-db.${namespace}.svc", "url": "https://${ref}.${suffix}"},
	"properties": {
		"host": {"type": "string"},
		"url": {"type": "string"},
		"engine": {"type": "string", "default": "postgresql"},
		"driver": {"type": "string", "x-okdp-derived": {"from": "engine", "map": {"postgresql": "pg", "mysql": "my"}}},
		"ssl": {"type": "string", "default": "require", "x-ui-condition": {"field": "engine", "value": "postgresql"}},
		"tls": {"type": "boolean", "default": false},
		"password": {"type": "string", "x-okdp-secret": true}
	},
	"required": ["host", "tls", "password"]}`)}}

// resolveSchema: parameters with references in a nested object, and in a
// list of references.
const resolveSchema = `{"properties": {
	"main": {"properties": {"db": {"type": "string", "x-okdp-connection-ref": {"contract": "database"}}}},
	"replicas": {"type": "array", "items": {"type": "string", "x-okdp-connection-ref": {"contract": "database"}}},
	"other": {"type": "string"}}}`

// resolveCtx: the context of the in-memory package with resolveSchema.
func resolveCtx(values M) (M, []Dependency, error) {
	ch := maps.Clone(pkg)
	ch["values.schema.json"] = &fstest.MapFile{Data: []byte(resolveSchema)}
	in := inputs(values)
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
	if values["other"] != "demo-pg" {
		t.Error("a field without x-okdp-connection-ref is left as it is")
	}
	if want := []Dependency{{Instance: "demo-pg", Contract: "database", Field: "main.db"}}; !reflect.DeepEqual(deps, want) {
		t.Errorf("dependencies: %#v", deps)
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
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := resolveCtx(tc.values)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

// TestResolveReport: every reference that does not resolve is reported at
// once, one line each, sorted by parameter path, the fields of a connection
// checked in key order: the same report from one compile to the next (the
// walk used to follow map order and stop at the first error).
func TestResolveReport(t *testing.T) {
	values := M{
		"main":     M{"db": "bad"},
		"replicas": []any{"demo-pg", "s3", "", "bad"},
		"connections": M{
			"bad": M{"contract": "database", "host": "h", "tls": true, "zone": "a", "port": 1},
			"s3":  M{"contract": "s3"},
		},
	}
	want := `main.db: connection "bad": field "port" is not part of the database contract
replicas[1]: connection "s3" has contract "s3", "database" is expected
replicas[3]: connection "bad": field "port" is not part of the database contract`
	for range 20 {
		if _, _, err := resolveCtx(values); err == nil || err.Error() != want {
			t.Fatalf("got %v\nwant %s", err, want)
		}
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
