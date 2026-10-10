package chart

import (
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

// readSchema: the parameter schema s of a package (ReadParameterSchema).
func readSchema(s string) (*Schema, error) {
	return ReadParameterSchema(fstest.MapFS{parameterSchema: {Data: []byte(s)}})
}

// TestSchemaDefaults: the defaults of the properties reached from the root
// through properties, at their path, the outermost on a path; a default
// elsewhere, or in the x-* keywords and the values of enum or examples, is
// not a parameter's.
func TestSchemaDefaults(t *testing.T) {
	for name, tc := range map[string]struct {
		schema string
		want   M
	}{
		"none":     {`{"properties": {"a": {"type": "string"}, "b": true}}`, M{}},
		"boolean":  {`true`, M{}},
		"a value":  {`{"properties": {"a": {"type": "integer", "default": 1}}}`, M{"a": float64(1)}},
		"an empty": {`{"properties": {"a": {"type": "string", "default": ""}, "m": {"type": "object", "default": {}}}}`, M{"a": "", "m": M{}}},
		"nested": {`{"$schema": "http://json-schema.org/draft-07/schema#", "properties": {
			"b": {"properties": {"c": {"default": "x"}, "d": {"type": "string"}}},
			"e": {"type": "object", "default": {"f": [1]}}}}`, M{"b": M{"c": "x"}, "e": M{"f": []any{float64(1)}}}},
		"a property named default": {`{"properties": {"default": {"default": true}}}`, M{"default": true}},
		"not keywords":             {`{"x-ui": {"default": 1}, "properties": {"a": {"enum": [{"default": 1}], "examples": [{"default": 2}], "x-okdp-derived": {"default": 3}}}}`, M{}},
		"elsewhere": {`{"default": {}, "allOf": [{"properties": {"a": {"default": 1}}}], "$defs": {"x": {"default": 1}},
			"properties": {"l": {"items": {"properties": {"a": {"default": 1}}}}}}`, M{}},
		"the outermost": {`{"properties": {"a": {"default": {}, "properties": {"b": {"default": 1}}}}}`, M{"a": M{}}},
	} {
		got, err := readSchema(tc.schema)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if !reflect.DeepEqual(got.defaults, tc.want) {
			t.Errorf("%s: got %#v, want %#v", name, got.defaults, tc.want)
		}
	}
}

// TestParameters: the layers merged in order and coalesced over the
// schema's defaults (a null deletes one), then checked against the schema,
// a default its property's schema rejects included; a package without a
// schema, or with one that is not JSON, fails.
func TestParameters(t *testing.T) {
	schema, err := readSchema(`{"properties": {
		"a": {"type": "integer", "default": 1},
		"b": {"properties": {"c": {"default": "x"}, "d": {"default": "y"}}}}}`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Parameters(schema, "svc", []map[string]any{{"b": M{"c": "z"}}, {"b": M{"d": nil}}})
	if err != nil {
		t.Fatal(err)
	}
	if want := (M{"a": float64(1), "b": M{"c": "z"}}); !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
	if _, err := Parameters(schema, "svc", []map[string]any{{"a": "two"}}); err == nil || !strings.Contains(err.Error(), "svc:\n- at '/a': got string, want integer") {
		t.Errorf("a violation: got %v", err)
	}
	if _, err := ReadParameterSchema(fstest.MapFS{}); err == nil || !strings.Contains(err.Error(), "open config.schema.json") {
		t.Errorf("without a schema: got %v", err)
	}
	if _, err := readSchema(`{`); err == nil || err.Error() != "config.schema.json: unexpected EOF" {
		t.Errorf("not JSON: got %v", err)
	}
	bad, err := readSchema(`{"properties": {"r": {"type": "integer", "minimum": 1, "default": 0}}}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Parameters(bad, "svc", nil); err == nil || !strings.Contains(err.Error(), "svc:\n- at '/r': minimum: got 0, want 1") {
		t.Errorf("a default its schema rejects: got %v", err)
	}
	if _, err := Parameters(bad, "svc", []map[string]any{{"r": 2}}); err != nil {
		t.Errorf("a default overridden: got %v", err)
	}
}
