// Parameters: the values of an instance, read as Helm reads the values of a
// chart: the layers merged as -f files, coalesced over the defaults of the
// package's config.schema.json, checked against it. The same machinery
// serves any values schema (Schema): package okdp reads the platform
// values with its own.
package chart

import (
	"bytes"
	"fmt"
	"io/fs"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/okdp/okdp-compiler/internal/value"
)

// parameterSchema: the parameter schema of a package, the parameters'
// defaults included.
const parameterSchema = "config.schema.json"

// Schema: a values schema, read, decoded and compiled once (ParseSchema)
// for every stage that reads it: its values' defaults and check (Values),
// and its keywords (Doc). A package's parameter schema
// (ReadParameterSchema, once per compile): the parameters (Parameters), and
// in package okdp the keys of global an instance may set and the connection
// references; the schema of the platform values (package okdp): the
// settings of global.okdp the compiler reads.
type Schema struct {
	// Doc: the document as values (value.Normalize: every number a
	// float64, as a values file reads it); empty for a boolean schema.
	Doc      M
	name     string             // the file it is read from, for the errors
	root     *jsonschema.Schema // the values' validator, compiled as a chart's values.schema.json (CompileSchema)
	defaults M                  // the values' defaults, as a values file holds them
}

// ReadParameterSchema: the parameter schema of pkg (config.schema.json),
// read as ParseSchema reads a schema. A missing schema fails with the error
// of the file system.
func ReadParameterSchema(pkg fs.FS) (*Schema, error) {
	b, err := fs.ReadFile(pkg, parameterSchema)
	if err != nil {
		return nil, err
	}
	return ParseSchema(parameterSchema, b)
}

// ParseSchema: the values schema b, read from the file name, decoded once
// (jsonschema.UnmarshalJSON: the validator keeps the numbers as written,
// Doc reads them as float64) and compiled once (CompileSchema). The
// defaults are as a values file holds them: the default of each property
// reached from the root through properties alone, at its path (that of
// properties.a.properties.b at a.b), the outermost on a path. A default
// anywhere else is not read; one that does not meet its schema fails the
// check of the values that keep it (Values).
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

// Values: user coalesced over the defaults of s, as Helm coalesces values
// over a chart's values.yaml, then checked against s, as Helm checks a
// chart's values.schema.json: the values, and their violations, one line
// each with its path (Validate), "" when they hold. user is not changed.
func (s *Schema) Values(user M) (M, string, error) {
	values := coalesceValues(s.defaults, user)
	v, err := Validate(s.name, s.root, values)
	if err != nil {
		return nil, "", err
	}
	return values, v, nil
}

// Parameters: the values layers, normalized (value.Normalize), merged in
// order (helm -f), coalesced over the defaults of the parameter schema and
// checked against it (Schema.Values). Every violation is reported at once,
// under name, as Helm reports those of a chart's values.
func Parameters(schema *Schema, name string, layers []map[string]any) (M, error) {
	user := M{}
	for _, v := range layers {
		user = value.Merge(user, v)
	}
	values, v, err := schema.Values(user)
	if err != nil {
		return nil, err
	}
	if v != "" {
		return nil, schemaError("values", map[string]string{name: v})
	}
	return values, nil
}

// schemaDefaults: the defaults of the properties of the schema s, at
// their path (ParseSchema).
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
