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
		"oidc": {"properties": {"clientSecret": {"type": "string", "format": "ipv4"}}},
		"dbPassword": {"type": "number", "exclusiveMinimum": 0, "exclusiveMaximum": 0}
	}}`))
	if err != nil {
		t.Fatal(err)
	}
	got, err := Validate("values.schema.json", s.root, map[string]any{
		"realm":      "Hunter2",
		"password":   "Hunter2",
		"tokens":     []any{"t-ok", "Hunter2"},
		"apiKey":     float64(23),
		"oidc":       map[string]any{"clientSecret": "Hunter2"},
		"dbPassword": float64(5),
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `- at '/apiKey': validation failed
  - at '/apiKey': maximum: got <redacted>, want 20
  - at '/apiKey': multipleOf: got <redacted>, want 7
- at '/dbPassword': exclusiveMaximum: got <redacted>, want 0
- at '/oidc/clientSecret': <redacted> is not valid ipv4
- at '/password': <redacted> does not match pattern '^[a-z]+$'
- at '/realm': 'Hunter2' does not match pattern '^[a-z]+$'
- at '/tokens/1': <redacted> does not match pattern '^t-'`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}
