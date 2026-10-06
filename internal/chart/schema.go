// Schema: values checked against a chart's values.schema.json, as Helm 4
// checks them before it renders a chart.
package chart

import (
	"bytes"
	"errors"
	"fmt"
	gourl "net/url"
	"slices"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// schemaURL: where a schema is compiled, for its $ref to itself.
const schemaURL = "file:///values.schema.json"

// checkSchema: the values against the JSON schema b, read from name (none
// when b is nil: the chart has no schema), one line per violation with its
// path, as Helm prints them (validate). The values are what the chart's
// templates see: Helm's coalesced values. The schema is compiled as Helm 4
// compiles it (CompileSchema).
func checkSchema(name string, b []byte, values M) (string, error) {
	if b == nil {
		return "", nil
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}
	sch, err := CompileSchema(schemaURL, doc)
	if err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}
	return validate(name, sch, values)
}

// validate: v against sch, a schema of the file name, one line per
// violation with its path, as Helm prints them, "" when v holds:
//
//   - at '/realm': '...' does not match pattern '^[A-Za-z0-9_.-]+$'
//
// The violations are sorted by path: Helm's order varies between runs. A
// panic of the validator is an error, as in Helm.
func validate(name string, sch *jsonschema.Schema, v any) (_ string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%s: unable to validate schema: %v", name, r)
		}
	}()
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
// keywords ignored. Unlike Helm, nothing is fetched: a $ref to a file or a
// URL fails (a urn: $ref accepts anything, as in Helm). A panic of the
// compiler is an error, as in Helm.
func CompileSchema(url string, doc any) (*jsonschema.Schema, error) {
	s, err := compileSchemas(url, doc)
	if err != nil {
		return nil, err
	}
	return s[0], nil
}

// compileSchemas: the schema doc at url compiled as CompileSchema compiles
// it, then its subschemas at each JSON pointer of ptrs (/properties/a),
// as they apply within it.
func compileSchemas(url string, doc any, ptrs ...string) (_ []*jsonschema.Schema, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("unable to validate schema: %v", r)
		}
	}()
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.UseLoader(schemaLoader{})
	if err := c.AddResource(url, doc); err != nil {
		return nil, err
	}
	out := make([]*jsonschema.Schema, 0, 1+len(ptrs))
	for _, loc := range append([]string{url}, ptrs...) {
		if len(out) > 0 {
			loc = url + "#" + fragment(loc)
		}
		s, err := c.Compile(loc)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

// fragment: the JSON pointer ptr as a URL fragment, each of its tokens
// percent-encoded.
func fragment(ptr string) string {
	tokens := strings.Split(ptr, "/")
	for i, t := range tokens {
		tokens[i] = gourl.PathEscape(t)
	}
	return strings.Join(tokens, "/")
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
