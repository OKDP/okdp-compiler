// Parameters: the values of an instance, read as Helm reads the values of a
// chart: the layers merged as -f files, coalesced over the package's
// values.yaml, checked against its values.schema.json.
package chart

import (
	"io/fs"

	"github.com/okdp/okdp-compiler/internal/value"
)

// Parameters: the values layers merged in order (helm -f), coalesced over
// the defaults of pkg (values.yaml), checked against its parameter schema
// (values.schema.json, none when it has no such file), as Helm checks
// values.schema.json. Every violation is reported at once, under name.
func Parameters(pkg fs.FS, name string, layers []map[string]any) (M, error) {
	defaults, err := readValues(pkg, "values.yaml")
	if err != nil {
		return nil, err
	}
	user := M{}
	for _, v := range layers {
		user = value.Merge(user, value.AsMap(value.Normalize(v)))
	}
	values := coalesceValues(defaults, user)
	v, err := schemaViolations(pkg, "values.schema.json", values)
	if err != nil {
		return nil, err
	}
	if v != "" {
		return nil, schemaError("values", map[string]string{name: v})
	}
	return values, nil
}
