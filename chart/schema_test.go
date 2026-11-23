package chart

import (
	"testing"
)

// TestValidateRedacts: a violation of a sensitive value (a password, a
// token in a list, an API key, a client secret) prints <redacted> in place
// of the value, for a pattern, a format and a numeric bound; the value of
// another key is printed, as Helm prints it.
func TestValidateRedacts(t *testing.T) {
	s, err := ParseSchema("values.schema.json", []byte(`{"$schema": "http://json-schema.org/draft-07/schema#", "properties": {
		"realm": {"type": "string", "pattern": "^[a-z]+$"},
		"password": {"type": "string", "pattern": "^[a-z]+$"},
		"tokens": {"type": "array", "items": {"type": "string", "pattern": "^t-"}},
		"apiKey": {"type": "number", "minimum": 10, "maximum": 20, "multipleOf": 7},
		"auth": {"properties": {"clientSecret": {"type": "string", "format": "ipv4"}}},
		"dbPassword": {"type": "number", "exclusiveMinimum": 0, "exclusiveMaximum": 0}
	}}`))
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Validate(map[string]any{
		"realm":      "Hunter2",
		"password":   "Hunter2",
		"tokens":     []any{"t-ok", "Hunter2"},
		"apiKey":     float64(23),
		"auth":       map[string]any{"clientSecret": "Hunter2"},
		"dbPassword": float64(5),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `- at '/apiKey': validation failed
  - at '/apiKey': maximum: got <redacted>, want 20
  - at '/apiKey': multipleOf: got <redacted>, want 7
- at '/auth/clientSecret': <redacted> is not valid ipv4
- at '/dbPassword': exclusiveMaximum: got <redacted>, want 0
- at '/password': <redacted> does not match pattern '^[a-z]+$'
- at '/realm': 'Hunter2' does not match pattern '^[a-z]+$'
- at '/tokens/1': <redacted> does not match pattern '^t-'`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// A refused property name is reported at its object, whatever the
// validator reads after it: it gives that location without copying it
// (relocate), and the keys of a map are read in any order.
func TestPropertyNamesLocation(t *testing.T) {
	names := `{"type":"object","propertyNames":{"pattern":"^[a-z]+$"}}`
	s, err := ParseSchema("values.schema.json", []byte(`{"type":"object","properties":{"global":{"type":"object","properties":{"okdp":{"type":"object","properties":{
		"access":{"type":"object","properties":{"groups":`+names+`,"owners":`+names+`}},
		"secrets":{"type":"object","properties":{"refreshInterval":{"type":"string"},"store":{"type":"string"}}},
		"proxy":{"type":"object","properties":{"noProxy":{"type":"string"}}}}}}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	rest := map[string]any{"refreshInterval": "1h", "store": "s"}
	for name, c := range map[string]struct {
		access map[string]any
		want   string
	}{
		"one object": {
			map[string]any{"groups": map[string]any{"a/b": "x"}},
			"- at '/global/okdp/access/groups': invalid propertyName 'a/b'\n  - at '': 'a/b' does not match pattern '^[a-z]+$'",
		},
		"the same name in two objects": {
			map[string]any{"groups": map[string]any{"a/b": "x"}, "owners": map[string]any{"a/b": "x"}},
			"- at '/global/okdp/access/groups': invalid propertyName 'a/b'\n  - at '': 'a/b' does not match pattern '^[a-z]+$'\n" +
				"- at '/global/okdp/access/owners': invalid propertyName 'a/b'\n  - at '': 'a/b' does not match pattern '^[a-z]+$'",
		},
	} {
		// Enough runs for the map order to move the validator's location.
		for range 50 {
			got, err := s.Validate(map[string]any{"global": map[string]any{"okdp": map[string]any{
				"access": c.access, "secrets": rest, "proxy": map[string]any{"noProxy": "n"},
			}}})
			if err != nil || got != c.want {
				t.Fatalf("%s: got %v:\n%s\nwant:\n%s", name, err, got, c.want)
			}
		}
	}
}
