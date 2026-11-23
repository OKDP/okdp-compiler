// This file reads the parameters: the values of an instance, merged like
// -f files, coalesced over the defaults of the package's
// config.schema.json and checked against it. Package okdp reads the
// platform values with the same machinery (Schema).

package chart

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"maps"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/okdp/okdp-compiler/value"
)

// parameterSchema is the file of a package's parameter schema and defaults.
const parameterSchema = "config.schema.json"

// Schema is a values schema, read and compiled once (ParseSchema). Values
// applies its defaults and checks values against it. A package's parameter
// schema and the schema of the platform values (package okdp) are read
// this way.
type Schema struct {
	// Doc is the schema document as values (every number a float64). It is
	// empty for a boolean schema.
	Doc      M
	name     string             // the file the schema is read from, for the errors
	root     *jsonschema.Schema // validates the values (compileSchema)
	defaults M                  // the values' defaults, laid out as in a values file
}

// ReadParameterSchema reads the parameter schema of pkg. A package
// without the file gets the empty schema ({}): no defaults, any values
// accepted.
//
// global.okdp holds the settings, which come from the values layers
// alone. A schema that declares it, or gives it a default, is refused:
// nothing would read that default.
func ReadParameterSchema(pkg fs.FS) (*Schema, error) {
	b, err := fs.ReadFile(pkg, parameterSchema)
	if errors.Is(err, fs.ErrNotExist) {
		b, err = []byte("{}"), nil
	}
	if err != nil {
		return nil, err
	}
	s, err := ParseSchema(parameterSchema, b)
	if err != nil {
		return nil, err
	}
	if value.Dig(s.Doc, "properties.global.properties.okdp") != nil {
		return nil, fmt.Errorf("%s: global.okdp is the platform's settings: a package schema declares nothing under it", parameterSchema)
	}
	if _, set := value.AsMap(s.defaults["global"])["okdp"]; set {
		return nil, fmt.Errorf("%s: global.okdp is the platform's settings: a package schema gives it no default (properties.global.default)", parameterSchema)
	}
	return s, nil
}

// ParseSchema decodes and compiles the values schema b. name is the file b
// is read from, for the errors.
//
// The defaults are laid out as in a values file. A property's default is
// read only when the property is reached from the root through properties
// alone: the default of properties.a.properties.b lands at a.b. When
// several defaults lie on one path, the outermost is read. A default that
// does not meet its schema fails the check of the values that keep it.
func ParseSchema(name string, b []byte) (*Schema, error) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err == nil {
		var s *Schema
		if s, err = NewSchema(name, schemaURL, doc); err == nil {
			return s, nil
		}
	}
	return nil, fmt.Errorf("%s: %w", name, err)
}

// NewSchema compiles the values schema doc, as jsonschema.UnmarshalJSON
// reads it, registered at url: a compile error quotes url. name is the
// file doc is read from, for the errors of Validate; NewSchema's own do
// not name it. Its defaults are read as ParseSchema reads them.
func NewSchema(name, url string, doc any) (*Schema, error) {
	values, err := value.Normalize(doc)
	if err != nil {
		return nil, err
	}
	root, err := compileSchema(url, doc)
	if err != nil {
		return nil, err
	}
	return &Schema{Doc: value.AsMap(values), name: name, root: root, defaults: schemaDefaults(values)}, nil
}

// Values coalesces user over the defaults of s, then checks the result
// against s, as Helm does with a chart's values.yaml and schema. It
// returns the coalesced values and their violations, one line each, ""
// when the values hold. user is not changed.
func (s *Schema) Values(user M) (M, string, error) {
	values, v, _, err := s.check(user)
	return values, v, err
}

// check is Values, and also returns the violations one by one (validate).
func (s *Schema) check(user M) (M, string, []Violation, error) {
	values := coalesceValues(s.defaults, user)
	v, leaves, err := validate(s.name, s.root, values)
	if err != nil {
		return nil, "", nil, err
	}
	return values, v, leaves, nil
}

// Parameters computes the parameters of an instance: its values layers,
// already normalized (value.Normalize), merged in order like helm -f,
// then coalesced and checked as Schema.Values does. All the violations
// are reported at once, under name, in a *ValuesError.
//
// connections and claims are not parameters. They are returned as merged:
// they get no default and no check, whatever the schema declares for them.
func Parameters(schema *Schema, name string, layers []map[string]any) (M, error) {
	user := M{}
	for _, v := range layers {
		user = value.Merge(user, v)
	}
	apart := M{}
	for _, k := range []string{"connections", "claims"} {
		if v, set := user[k]; set {
			apart[k] = v
		}
		delete(user, k)
	}
	values, v, leaves, err := schema.check(user)
	if err != nil {
		return nil, err
	}
	if v != "" {
		return nil, &ValuesError{msg: schemaError("values", map[string]string{name: v}).Error(), Violations: leaves}
	}
	delete(values, "connections")
	delete(values, "claims")
	maps.Copy(values, apart)
	return values, nil
}

// schemaDefaults returns the defaults of s, each at its path (ParseSchema).
func schemaDefaults(s any) M {
	out := M{}
	for k, p := range value.AsMap(value.AsMap(s)["properties"]) {
		if d, ok := value.AsMap(p)["default"]; ok {
			out[k] = d
		} else if sub := schemaDefaults(p); len(sub) > 0 {
			out[k] = sub
		}
	}
	return out
}

// Validate checks v against the schema, as Helm does. It returns one line
// per violation with its path, as Helm prints them, or "" when v holds:
//
//   - at '/realm': '...' does not match pattern '^[A-Za-z0-9_.-]+$'
//
// The violations are sorted by path, because Helm's order varies between
// runs. The value of a sensitive key is never printed (redactCauses).
func (s *Schema) Validate(v any) (string, error) {
	msg, _, err := validate(s.name, s.root, v)
	return msg, err
}
