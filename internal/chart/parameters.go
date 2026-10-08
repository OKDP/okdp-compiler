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
	"maps"
	"slices"
	"strconv"
	"strings"

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
// Doc reads them as float64) and compiled once, its root with the schema of
// the property of each default (compileSchemas). The defaults are as a
// values file holds them: the default of each property reached from the
// root through properties alone, at its path (that of
// properties.a.properties.b at a.b). A default anywhere else fails, named
// by its JSON pointer: at the root or under another keyword, where whether
// it applies would depend on the values; under a property that has a
// default, where the two would compete; or one that does not meet its
// property's schema.
func ParseSchema(name string, b []byte) (*Schema, error) {
	sdoc, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	doc, err := value.Normalize(sdoc)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	w := defaultsWalk{name: name}
	defaults, err := w.schema(doc, "", false, true)
	if err != nil {
		return nil, err
	}
	props := make([]string, len(w.ptrs))
	for i, p := range w.ptrs {
		props[i] = strings.TrimSuffix(p, "/default")
	}
	schemas, err := compileSchemas(schemaURL, sdoc, props...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	// Each default against the schema of its property.
	for i, p := range w.ptrs {
		v, err := validate(name, schemas[i+1], w.values[i])
		if err != nil {
			return nil, err
		}
		if v != "" {
			return nil, fmt.Errorf("%s#%s: the default does not meet its property's schema:\n%s", name, p, v)
		}
	}
	return &Schema{Doc: value.AsMap(doc), name: name, root: schemas[0], defaults: defaults}, nil
}

// Values: user coalesced over the defaults of s, as Helm coalesces values
// over a chart's values.yaml, then checked against s, as Helm checks a
// chart's values.schema.json: the values, and their violations, one line
// each with its path (validate), "" when they hold. user is not changed.
func (s *Schema) Values(user M) (M, string, error) {
	values := coalesceValues(s.defaults, user)
	v, err := validate(s.name, s.root, values)
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

// The keywords whose value is a schema or a list of schemas, and those
// whose value maps names to schemas (drafts 7 to 2020-12), properties
// apart: what applies under them depends on the values, so no default
// does.
var (
	subschemaKeywords = []string{"additionalItems", "additionalProperties", "allOf", "anyOf", "contains", "contentSchema", "else", "if", "items", "not", "oneOf", "prefixItems", "propertyNames", "then", "unevaluatedItems", "unevaluatedProperties"}
	schemaMapKeywords = []string{"$defs", "definitions", "dependencies", "dependentSchemas", "patternProperties"}
)

// defaultsWalk: the defaults of a schema file (ParseSchema), and
// the JSON pointer of each, in the order found.
type defaultsWalk struct {
	name   string
	ptrs   []string
	values []any
}

// schema: the defaults under the schema s, at the JSON pointer ptr: those
// of its properties when reach (s is the root, or a property reached from
// it through properties). own: s is such a property, and its parent reads
// its default.
func (w *defaultsWalk) schema(s any, ptr string, own, reach bool) (M, error) {
	m, ok := s.(M)
	if !ok {
		return M{}, nil // a boolean schema
	}
	if _, ok := m["default"]; ok && !own {
		return nil, fmt.Errorf("%s#%s/default: a default applies only to a property reached from the root through properties", w.name, ptr)
	}
	out := M{}
	for _, k := range slices.Sorted(maps.Keys(m)) {
		at := ptr + "/" + escapeToken(k)
		switch v := m[k]; {
		case k == "properties":
			props := value.AsMap(v)
			for _, p := range slices.Sorted(maps.Keys(props)) {
				pat := at + "/" + escapeToken(p)
				n := len(w.ptrs)
				sub, err := w.schema(props[p], pat, reach, reach)
				if err != nil {
					return nil, err
				}
				d, has := value.AsMap(props[p])["default"]
				switch {
				case has && len(w.ptrs) > n:
					return nil, fmt.Errorf("%s#%s/default: a property under it has a default too (#%s): keep one", w.name, pat, w.ptrs[n])
				case has:
					w.ptrs = append(w.ptrs, pat+"/default")
					w.values = append(w.values, d)
					out[p] = d
				case len(sub) > 0:
					out[p] = sub
				}
			}
		case slices.Contains(subschemaKeywords, k):
			l, isList := v.([]any)
			if !isList {
				l = []any{v}
			}
			for i, e := range l {
				eat := at
				if isList {
					eat += "/" + strconv.Itoa(i)
				}
				if _, err := w.schema(e, eat, false, false); err != nil {
					return nil, err
				}
			}
		case slices.Contains(schemaMapKeywords, k):
			named := value.AsMap(v)
			for _, n := range slices.Sorted(maps.Keys(named)) {
				if _, err := w.schema(named[n], at+"/"+escapeToken(n), false, false); err != nil {
					return nil, err
				}
			}
		}
	}
	return out, nil
}

// escapeToken: k as a token of a JSON pointer (~ as ~0, / as ~1).
func escapeToken(k string) string {
	return strings.NewReplacer("~", "~0", "/", "~1").Replace(k)
}
