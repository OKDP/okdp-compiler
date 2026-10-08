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
// through properties, at their path; a default elsewhere, or in the x-*
// keywords and the values of enum or examples, is not a parameter's.
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

// TestSchemaDefaultsErrors: a default where whether it applies depends on
// the values, two defaults for one property, and one its property's schema
// rejects fail, named by their JSON pointer.
func TestSchemaDefaultsErrors(t *testing.T) {
	const where = ": a default applies only to a property reached from the root through properties"
	for name, tc := range map[string]struct{ schema, want string }{
		"not JSON":             {`{`, "config.schema.json: unexpected EOF"},
		"the root":             {`{"default": {}}`, "config.schema.json#/default" + where},
		"allOf":                {`{"allOf": [{"properties": {"a": {"default": 1}}}]}`, "config.schema.json#/allOf/0/properties/a/default" + where},
		"then":                 {`{"if": {"properties": {"a": {"const": 1}}}, "then": {"properties": {"b": {"default": 1}}}}`, "config.schema.json#/then/properties/b/default" + where},
		"items":                {`{"properties": {"l": {"items": {"properties": {"a": {"default": 1}}}}}}`, "config.schema.json#/properties/l/items/properties/a/default" + where},
		"additionalProperties": {`{"properties": {"m": {"additionalProperties": {"default": 1}}}}`, "config.schema.json#/properties/m/additionalProperties/default" + where},
		"$defs":                {`{"$defs": {"x": {"default": 1}}, "properties": {"a": {"$ref": "#/$defs/x"}}}`, "config.schema.json#/$defs/x/default" + where},
		"two defaults": {`{"properties": {"a": {"default": {}, "properties": {"b": {"default": 1}}}}}`,
			"config.schema.json#/properties/a/default: a property under it has a default too (#/properties/a/properties/b/default): keep one"},
		"not its schema": {`{"properties": {"r": {"type": "integer", "minimum": 1, "default": 0}}}`,
			"config.schema.json#/properties/r/default: the default does not meet its property's schema:\n- at '': minimum: got 0, want 1"},
		"an escaped name": {`{"properties": {"a/b c~": {"type": "string", "default": 1}}}`,
			"config.schema.json#/properties/a~1b c~0/default: the default does not meet its property's schema:\n- at '': got number, want string"},
	} {
		if _, err := readSchema(tc.schema); err == nil || err.Error() != tc.want {
			t.Errorf("%s: got %v, want %s", name, err, tc.want)
		}
	}
}

// TestParameters: the layers merged in order and coalesced over the
// schema's defaults (a null deletes one), then checked against the schema;
// a package without a schema fails.
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
}
