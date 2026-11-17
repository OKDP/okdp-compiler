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
	root     *jsonschema.Schema // validates the values (CompileSchema)
	defaults M                  // the values' defaults, laid out as in a values file
}

// ReadParameterSchema reads the parameter schema of pkg. A package
// without the file gets the empty schema ({}): no defaults, any values
// accepted.
//
// global.okdp holds the platform's settings, which come from the context
// alone (Inputs.Context). A schema that declares it, or gives it a
// default, is refused: nothing would read that default.
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
	sdoc, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	doc, err := value.Normalize(sdoc)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	root, err := CompileSchema(schemaURL, sdoc)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return &Schema{Doc: value.AsMap(doc), name: name, root: root, defaults: schemaDefaults(doc)}, nil
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
		return nil, &ValuesError{msg: SchemaError("values", map[string]string{name: v}).Error(), Violations: leaves}
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

// Validate checks v against the schema, as the function Validate does.
func (s *Schema) Validate(name string, v any) (string, error) { return Validate(name, s.root, v) }
