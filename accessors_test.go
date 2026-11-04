package compiler_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	compiler "github.com/okdp/okdp-compiler"
)

// The field errors of a compile: a violation of the parameter schema at
// its place, a reference at its path, a tree that does not load a
// problem each, of the whole.
func TestFieldErrors(t *testing.T) {
	def, in := example()
	in.Values = append(in.Values, map[string]any{"replicas": "two"})
	_, err := compiler.Compile(context.Background(), def, in)
	fields := compiler.FieldErrors(fmt.Errorf("wrapped: %w", err))
	if len(fields) != 1 || fields[0].Pointer != "/replicas" || fields[0].Path != "replicas" || fields[0].Message != "at '/replicas': got string, want integer" {
		t.Fatalf("a schema violation: %+v (%v)", fields, err)
	}
	load := &compiler.LoadError{Problems: []string{"a", "b"}}
	if fields := compiler.FieldErrors(load); len(fields) != 2 || fields[1].Message != "b" || fields[1].Path != "" {
		t.Errorf("a LoadError: %+v", fields)
	}
	if fields := compiler.FieldErrors(errors.New("x")); len(fields) != 1 || fields[0].Message != "x" {
		t.Errorf("an error of no field: %+v", fields)
	}
	if compiler.FieldErrors(nil) != nil {
		t.Error("nil")
	}
}

// The connection references of a schema are those the compile reads,
// through properties and items; one out of its reach is refused.
func TestConnectionRefs(t *testing.T) {
	schema := map[string]any{
		"type":     "object",
		"required": []any{"db"},
		"properties": map[string]any{
			"db": map[string]any{"type": "string", "x-okdp-connection-ref": map[string]any{"contract": "database", "provision": true, "request": map[string]any{"size": "1Gi"}}},
			"catalogs": map[string]any{"type": "array", "items": map[string]any{"properties": map[string]any{
				"storage": map[string]any{"description": "the store", "x-okdp-connection-ref": map[string]any{"contract": "s3"}},
			}}},
		},
	}
	refs, err := compiler.ConnectionRefs(schema)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 || refs[0].Path != "catalogs[].storage" || refs[0].Contract != "s3" || refs[0].Schema != "properties.catalogs.items.properties.storage" || refs[0].Property["description"] != "the store" {
		t.Fatalf("refs = %+v", refs)
	}
	if r := refs[1]; r.Path != "db" || !r.Provision || !r.Required || r.Request["size"] != "1Gi" {
		t.Fatalf("db = %+v", r)
	}
	schema["allOf"] = []any{map[string]any{"properties": map[string]any{"x": map[string]any{"x-okdp-connection-ref": map[string]any{"contract": "s3"}}}}}
	if _, err := compiler.ConnectionRefs(schema); err == nil || !strings.Contains(err.Error(), "allOf[0].properties.x: x-okdp-connection-ref out of the compile's reach") {
		t.Fatalf("a reference under allOf: %v", err)
	}
}

// A contract as the compile reads it: its fields in the order of the
// file, the secret ones apart.
func TestReadContract(t *testing.T) {
	contracts := os.DirFS("examples/02_advanced/input/db/contracts")
	d, err := compiler.ReadContract(contracts, "database")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range d.Fields {
		names = append(names, f.Name)
	}
	if strings.Join(names, ",") != "engine,driver,host,port,dbName,size,password" || d.Label != "database" || strings.Join(d.Required, ",") != "dbName,host" {
		t.Fatalf("descriptor = %+v", d)
	}
	if f := d.Field("password"); f == nil || !f.Secret || f.Required {
		t.Errorf("password = %+v", f)
	}
	if f := d.Field("driver"); f == nil || f.Derived["from"] != "engine" {
		t.Errorf("driver = %+v", f)
	}
	if d.Claim == nil || d.Field("nothing") != nil {
		t.Error("claim schema, unknown field")
	}
	all, err := compiler.ReadContracts(contracts)
	if err != nil || len(all) != 2 || all[1].Name != "http" {
		t.Fatalf("contracts: %d %v", len(all), err)
	}
	if _, err := compiler.ReadContract(contracts, "nothing"); err == nil || !strings.Contains(err.Error(), "the nothing contract cannot be read") {
		t.Fatalf("a missing contract: %v", err)
	}
}

// The values layering and the names are the compile's.
func TestValuesAndNames(t *testing.T) {
	a := map[string]any{"x": map[string]any{"a": 1.0, "b": 2.0}, "l": []any{1.0}}
	merged := compiler.MergeValues(a, map[string]any{"x": map[string]any{"b": nil}, "l": []any{2.0}})
	if x := merged["x"].(map[string]any); x["a"] != 1.0 || x["b"] != nil || len(merged["l"].([]any)) != 1 || a["x"].(map[string]any)["b"] != 2.0 {
		t.Errorf("merged = %v (a %v)", merged, a)
	}
	co := compiler.CoalesceValues(map[string]any{"x": map[string]any{"b": nil}}, map[string]any{"x": map[string]any{"a": 1.0, "b": 2.0}, "n": nil})
	if x := co["x"].(map[string]any); x["a"] != 1.0 || len(x) != 1 || len(co) != 1 {
		t.Errorf("coalesced = %v", co)
	}
	if compiler.NamedClaimID("demo", "analytics") != "demo.analytics" || !compiler.IsClaimID("demo.app.db") || compiler.IsClaimID("web") || compiler.IsClaimID("Demo.x") {
		t.Error("claim IDs")
	}
	if compiler.CheckProvider("p", "data/postgresql") != nil || compiler.CheckProvider("p", "a/b/c") == nil {
		t.Error("providers")
	}
}

// A connection is checked at its declaration as the compile of a tree
// checks it, whatever references it.
func TestCheckConnection(t *testing.T) {
	contracts := os.DirFS("examples/02_advanced/input/db/contracts")
	c := compiler.Connection{Name: "pg", Contract: "database", Values: map[string]any{"host": "db", "dbName": "app"}, SecretRef: "pg"}
	if err := compiler.CheckConnection(contracts, "demo", c, nil); err != nil {
		t.Fatal(err)
	}
	c.Values = map[string]any{"host": "db", "password": "x"}
	err := compiler.CheckConnection(contracts, "demo", c, nil)
	if fields := compiler.FieldErrors(err); len(fields) != 1 || fields[0].Path != "connections.pg" || !strings.Contains(fields[0].Message, `"password" is a secret field`) {
		t.Fatalf("a secret field: %v", err)
	}
	c.Values, c.SecretRef, c.RemoteKey = map[string]any{"host": "db", "dbName": "app"}, "", "platform/pg"
	if err := compiler.CheckConnection(contracts, "demo", c, map[string]any{"global": map[string]any{"okdp": map[string]any{"secrets": map[string]any{"store": "s"}}}}); err == nil || !strings.Contains(err.Error(), `names a key under "demo/"`) {
		t.Fatalf("a project's key of the platform's: %v", err)
	}
	if err := compiler.CheckConnection(contracts, "", c, map[string]any{"global": map[string]any{"okdp": map[string]any{"secrets": map[string]any{"store": "s"}}}}); err != nil {
		t.Fatalf("the platform's: %v", err)
	}
}
