// The schemas of a rule set: the first set that gives one gives the
// schema, and the sets read after it add to it. The document they give
// together is parsed once, when every set is read.

package rules

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"reflect"
	"slices"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/okdp/okdp-compiler/chart"
	"github.com/okdp/okdp-compiler/value"
)

// The schemas a rule set may give, under schemas in rules.yaml.
const (
	schemaPlatform = "platform"
	schemaMapping  = "mapping"
)

// schemaFile is a schema of the rule set as the sets give it: the file of
// the first set that gives it, and its document with what the sets read
// after it add.
type schemaFile struct {
	name  string         // the base name of the first file: the schema's name in its errors
	data  []byte         // the first file, parsed as it is when no set adds to it
	doc   map[string]any // the document, decoded as chart.ParseSchema decodes it
	added bool           // a set read after the first adds to doc
}

// loadSchema reads the schema key of a set. The first set that gives it
// gives the schema; a set read after it adds to it (mergeSchema).
func (r *RuleSet) loadSchema(fsys fs.FS, schemas M, key string) error {
	if _, ok := schemas[key]; !ok {
		return nil
	}
	file := path.Clean(value.Str(schemas[key]))
	b, err := fs.ReadFile(fsys, file)
	if err != nil {
		return fmt.Errorf("%s: %w", RulesFile, err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("%s: %s: %w", RulesFile, path.Base(file), err)
	}
	f, ok := r.schemaFiles[key]
	if !ok {
		m, _ := doc.(map[string]any) // nil for a boolean schema: chart.ParseSchema reads it, and no set adds to it
		r.schemaFiles[key] = &schemaFile{name: path.Base(file), data: b, doc: m}
		return nil
	}
	add, isMap := doc.(map[string]any)
	if f.doc == nil || !isMap {
		return taken("schemas", key+": #")
	}
	if at := mergeSchema(f.doc, add, "#"); at != "" {
		return taken("schemas", key+": "+at)
	}
	f.added = true
	return nil
}

// mergeSchema adds the schema node add to base, the node at the JSON
// pointer at of the schema. A key of properties or $defs that base does
// not hold is added; one that both hold is merged when both are schema
// objects and base's has properties or $defs, a leaf otherwise, refused.
// Any other keyword of add must be base's, with the same value:
// an added schema changes none of base's required, additionalProperties,
// type or default. It returns the pointer of the first key that both give
// otherwise, "" when add is merged.
func mergeSchema(base, add map[string]any, at string) string {
	for _, k := range slices.Sorted(maps.Keys(add)) {
		where := at + "/" + pointerToken(k)
		if k != "properties" && k != "$defs" {
			if bv, ok := base[k]; !ok || !reflect.DeepEqual(bv, add[k]) {
				return where
			}
			continue
		}
		am, aMap := add[k].(map[string]any)
		if _, ok := base[k]; !ok {
			base[k] = add[k]
			continue
		}
		bm, bMap := base[k].(map[string]any)
		if !aMap || !bMap {
			return where
		}
		for _, name := range slices.Sorted(maps.Keys(am)) {
			if _, ok := bm[name]; !ok {
				bm[name] = am[name]
				continue
			}
			bs, bObj := bm[name].(map[string]any)
			as, aObj := am[name].(map[string]any)
			if !bObj || !aObj || bs["properties"] == nil && bs["$defs"] == nil {
				return where + "/" + pointerToken(name)
			}
			if at := mergeSchema(bs, as, where+"/"+pointerToken(name)); at != "" {
				return at
			}
		}
	}
	return ""
}

// pointerToken escapes a key as a token of a JSON pointer.
func pointerToken(k string) string {
	return strings.NewReplacer("~", "~0", "/", "~1").Replace(k)
}

// parseSchemas parses each schema of the rule set once, as the sets give
// it together, with chart.ParseSchema. A schema no set adds to is its file,
// parsed as it is.
func (r *RuleSet) parseSchemas() error {
	for _, schema := range []struct {
		key string
		to  **chart.Schema
	}{{schemaPlatform, &r.platformSchema}, {schemaMapping, &r.mappingSchema}} {
		key, to := schema.key, schema.to
		f, ok := r.schemaFiles[key]
		if !ok {
			continue
		}
		if !f.added {
			s, err := chart.ParseSchema(f.name, f.data)
			if err != nil {
				return fmt.Errorf("%s: %w", RulesFile, err)
			}
			*to = s
			continue
		}
		b, err := json.Marshal(f.doc)
		if err != nil {
			return fmt.Errorf("%s: schemas: %s: %w", RulesFile, key, err)
		}
		s, err := chart.ParseSchema(f.name, b)
		if err != nil {
			return fmt.Errorf("%s: schemas: %s, with what the added rule sets give: %w", RulesFile, key, err)
		}
		*to = s
	}
	return nil
}
