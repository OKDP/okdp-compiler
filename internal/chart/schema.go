// Schema: values checked against a chart's values.schema.json, as Helm 4
// checks them before it renders a chart.
package chart

import (
	"errors"
	"fmt"
	"math/big"
	"slices"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"golang.org/x/text/language"
	"golang.org/x/text/message"

	"github.com/okdp/okdp-compiler/internal/value"
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
// The value of a sensitive key (value.SensitivePath) is not printed:
// <redacted> in its place (redactCauses).
func Validate(name string, sch *jsonschema.Schema, v any) (string, error) {
	msg, _, err := validate(name, sch, v)
	return msg, err
}

// validate: Validate, and the violations of its report one by one, its
// leaves (violations).
func validate(name string, sch *jsonschema.Schema, v any) (string, []Violation, error) {
	verr := sch.Validate(v)
	if verr == nil {
		return "", nil, nil
	}
	var ve *jsonschema.ValidationError
	if !errors.As(verr, &ve) {
		return "", nil, fmt.Errorf("%s: %w", name, verr)
	}
	sortCauses(ve)
	redactCauses(ve)
	// The first line names the schema: "jsonschema validation failed with
	// '<url>#<pointer>'".
	msg := ve.Error()
	if _, lines, ok := strings.Cut(msg, "\n"); ok {
		msg = lines
	}
	return msg, violations(ve), nil
}

// Violation: a violation of a values schema, a leaf of Validate's
// report: where it is in the values (Location, the keys and the indexes
// from the root, the tokens of a JSON pointer, none for the whole
// values), and its line of the report, "at '<pointer>': <message>", as
// redacted.
type Violation struct {
	Location []string
	Message  string
}

// printer: the printer of the validator's messages, English.
var printer = message.NewPrinter(language.English)

// violations: the leaves of e, sorted and redacted, in order: each a
// violation, the causes of a combinator (allOf, anyOf, a $ref) its
// leaves'.
func violations(e *jsonschema.ValidationError) []Violation {
	if len(e.Causes) == 0 {
		if _, root := e.ErrorKind.(*kind.Schema); root {
			return nil
		}
		return []Violation{{Location: slices.Clone(e.InstanceLocation), Message: fmt.Sprintf("at %s: %s", quote(value.Pointer(e.InstanceLocation)), e.ErrorKind.LocalizedString(printer))}}
	}
	var out []Violation
	for _, c := range e.Causes {
		out = append(out, violations(c)...)
	}
	return out
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

// redactCauses: at every level of e, a violation of a sensitive value
// (value.SensitivePath of its location) whose message prints the value (a
// pattern, a format, a numeric bound) printing value.Redacted instead
// (redacted).
func redactCauses(e *jsonschema.ValidationError) {
	if value.SensitivePath(e.InstanceLocation) {
		switch e.ErrorKind.(type) {
		case *kind.Pattern, *kind.Format, *kind.Minimum, *kind.Maximum, *kind.ExclusiveMinimum, *kind.ExclusiveMaximum, *kind.MultipleOf:
			e.ErrorKind = redacted{e.ErrorKind}
		}
	}
	for _, c := range e.Causes {
		redactCauses(c)
	}
}

// redacted: a violation whose value is not printed, its message the
// kind's with value.Redacted for the value it got; for a format, without
// the format's error either, which may quote the value.
type redacted struct{ jsonschema.ErrorKind }

// LocalizedString: the message of the kind, value.Redacted in place of
// the value.
func (r redacted) LocalizedString(p *message.Printer) string {
	bound := func(keyword string, want *big.Rat) string {
		f, _ := want.Float64()
		return p.Sprintf("%s: got %s, want %v", keyword, value.Redacted, f)
	}
	switch k := r.ErrorKind.(type) {
	case *kind.Pattern:
		return p.Sprintf("%s does not match pattern %s", value.Redacted, quote(k.Want))
	case *kind.Format:
		return p.Sprintf("%s is not valid %s", value.Redacted, k.Want)
	case *kind.Minimum:
		return bound("minimum", k.Want)
	case *kind.Maximum:
		return bound("maximum", k.Want)
	case *kind.ExclusiveMinimum:
		return bound("exclusiveMinimum", k.Want)
	case *kind.ExclusiveMaximum:
		return bound("exclusiveMaximum", k.Want)
	case *kind.MultipleOf:
		return bound("multipleOf", k.Want)
	}
	return r.ErrorKind.LocalizedString(p)
}

// quote: s as the validator quotes it in a message, '...'.
func quote(s string) string {
	s = fmt.Sprintf("%q", s)
	s = strings.ReplaceAll(s, `\"`, `"`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	return "'" + s[1:len(s)-1] + "'"
}

// ValuesError: values that do not meet their schema (Parameters): the
// error as Helm reports it, and each violation apart (Violations), for a
// caller that maps them to the fields of a form.
type ValuesError struct {
	msg        string
	Violations []Violation
}

func (e *ValuesError) Error() string { return e.msg }

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
