// Schema: values checked against a chart's values.schema.json, as Helm 4
// checks them before it renders a chart.
package chart

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
)

// schemaURL: where a schema is compiled, for its $ref to itself.
const schemaURL = "file:///values.schema.json"

// checkSchema: the values against the JSON schema b, read from name (none
// when b is nil: the chart has no schema), one line per violation with its
// path, as Helm prints them (Validate). The values are what the chart's
// templates see: Helm's coalesced values. The schema is read as ParseSchema
// reads it, compiled as Helm 4 compiles it (CompileSchema).
func checkSchema(name string, b []byte, values M) (string, error) {
	if b == nil {
		return "", nil
	}
	s, err := ParseSchema(name, b)
	if err != nil {
		return "", err
	}
	return Validate(name, s.root, values)
}

// Validate: v against sch, a schema of the file name, one line per
// violation with its path, as Helm prints them, "" when v holds:
//
//   - at '/realm': '...' does not match pattern '^[A-Za-z0-9_.-]+$'
//
// The violations are sorted by path: Helm's order varies between runs.
func Validate(name string, sch *jsonschema.Schema, v any) (string, error) {
	verr := sch.Validate(v)
	if verr == nil {
		return "", nil
	}
	var ve *jsonschema.ValidationError
	if !errors.As(verr, &ve) {
		return "", fmt.Errorf("%s: %w", name, verr)
	}
	sortCauses(ve)
	// The first line names the schema: "jsonschema validation failed with
	// '<url>#<pointer>'".
	msg := ve.Error()
	if _, lines, ok := strings.Cut(msg, "\n"); ok {
		msg = lines
	}
	return msg, nil
}

// CompileSchema: the JSON schema doc (as jsonschema.UnmarshalJSON reads
// it), at url, compiled as Helm 4 compiles a chart's values.schema.json
// (santhosh-tekuri/jsonschema v6, the version Helm 4.3 uses): the draft of
// its $schema, draft 2020-12 without one (Helm 4's default, checked with
// helm 4.3), formats asserted for drafts before 2019-09 only, unknown
// keywords ignored. Unlike Helm, nothing is fetched: a $ref to a file, a
// URL or a urn: fails (a loader of no scheme); the draft meta-schemas are
// built into the validator.
func CompileSchema(url string, doc any) (*jsonschema.Schema, error) {
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.UseLoader(jsonschema.SchemeURLLoader{})
	if err := c.AddResource(url, doc); err != nil {
		return nil, err
	}
	return c.Compile(url)
}

// sortCauses: the causes of e sorted by instance path then message, at
// every level, and the names of additional properties in one message
// sorted (the validator lists them in map order), for a stable report.
func sortCauses(e *jsonschema.ValidationError) {
	if k, ok := e.ErrorKind.(*kind.AdditionalProperties); ok {
		slices.Sort(k.Properties)
	}
	for _, c := range e.Causes {
		sortCauses(c)
	}
	slices.SortStableFunc(e.Causes, func(a, b *jsonschema.ValidationError) int {
		if c := slices.Compare(a.InstanceLocation, b.InstanceLocation); c != 0 {
			return c
		}
		return strings.Compare(a.Error(), b.Error())
	})
}

// schemaError: the violations of the values of each chart, as Helm reports
// them; what is the values checked ("values", "compiled values").
func schemaError(what string, violations map[string]string) error {
	charts := make([]string, 0, len(violations))
	for c, v := range violations {
		if v != "" {
			charts = append(charts, c)
		}
	}
	if len(charts) == 0 {
		return nil
	}
	slices.Sort(charts)
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s don't meet the specifications of the schema(s) in the following chart(s):", what)
	for _, c := range charts {
		fmt.Fprintf(&sb, "\n%s:\n%s", c, violations[c])
	}
	return errors.New(sb.String())
}
