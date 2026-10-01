// Schema: values checked against a chart's values.schema.json, as Helm 4
// checks them before it renders a chart.
package compiler

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// schemaViolations: the values against the JSON schema file name of fsys,
// none when there is no such file (checkSchema).
func schemaViolations(fsys fs.FS, name string, values M) (string, error) {
	b, err := fs.ReadFile(fsys, name)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return checkSchema(name, b, values)
}

// checkSchema: the values against the JSON schema b, read from name (none
// when b is nil: the chart has no schema), one line per violation with its
// path, as Helm prints them:
//
//   - at '/realm': '...' does not match pattern '^[A-Za-z0-9_.-]+$'
//
// The values are what the chart's templates see: Helm's coalesced values.
// The schema is compiled as Helm 4 compiles it (santhosh-tekuri/jsonschema
// v6, the version Helm 4.3 uses): the draft of its $schema, draft 2020-12
// without one (Helm 4's default, checked with helm 4.3), formats asserted
// for drafts before 2019-09 only. Unlike Helm, nothing is fetched: a $ref to
// a file or a URL fails the compile (a urn: $ref accepts anything, as in
// Helm). The violations are sorted by path: Helm's order varies between runs.
func checkSchema(name string, b []byte, values M) (_ string, err error) {
	if b == nil {
		return "", nil
	}
	// A panic of the validator is an error, as in Helm.
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%s: unable to validate schema: %v", name, r)
		}
	}()
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.UseLoader(schemaLoader{})
	const url = "file:///values.schema.json"
	if err := c.AddResource(url, doc); err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}
	sch, err := c.Compile(url)
	if err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}
	verr := sch.Validate(any(values))
	if verr == nil {
		return "", nil
	}
	var ve *jsonschema.ValidationError
	if !errors.As(verr, &ve) {
		return "", fmt.Errorf("%s: %w", name, verr)
	}
	sortCauses(ve)
	return strings.TrimPrefix(ve.Error(), "jsonschema validation failed with '"+url+"#'\n"), nil
}

// sortCauses: the causes of e sorted by instance path then message, at
// every level, for a stable report.
func sortCauses(e *jsonschema.ValidationError) {
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

// schemaLoader: what a $ref outside the schema loads. The draft
// meta-schemas are built into the validator; urn: references accept
// anything, as Helm's default URN resolver does; files and URLs are not
// read.
type schemaLoader struct{}

// Load: a permissive schema for a urn:, an error otherwise.
func (schemaLoader) Load(url string) (any, error) {
	if strings.HasPrefix(url, "urn:") {
		return true, nil
	}
	return nil, fmt.Errorf("$ref %s: the schema may only reference itself (the compile reads no file or URL)", url)
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
