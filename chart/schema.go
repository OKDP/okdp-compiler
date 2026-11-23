// This file checks values against a schema, as Helm 4 does before it renders.

package chart

import (
	"errors"
	"fmt"
	"maps"
	"math/big"
	"slices"
	"strconv"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"golang.org/x/text/language"
	"golang.org/x/text/message"

	"github.com/okdp/okdp-compiler/value"
)

// schemaURL is the URL schemas compile at, so that a $ref to itself resolves.
const schemaURL = "file:///values.schema.json"

// validate checks v against sch, a schema read from the file name. It
// returns one line per violation with its path, as Helm prints them, or ""
// when v holds, and the violations one by one:
//
//   - at '/realm': '...' does not match pattern '^[A-Za-z0-9_.-]+$'
//
// The violations are sorted by path, because Helm's order varies between
// runs. The value of a sensitive key is never printed (redactCauses).
func validate(name string, sch *jsonschema.Schema, v any) (string, []Violation, error) {
	verr := sch.Validate(v)
	if verr == nil {
		return "", nil, nil
	}
	var ve *jsonschema.ValidationError
	if !errors.As(verr, &ve) {
		return "", nil, fmt.Errorf("%s: %w", name, verr)
	}
	relocate(ve, v, nil, map[string]bool{})
	sortCauses(ve)
	redactCauses(ve)
	// Drop the first line, which only names the schema.
	msg := ve.Error()
	if _, lines, ok := strings.Cut(msg, "\n"); ok {
		msg = lines
	}
	return msg, violations(ve), nil
}

// Violation is one violation of a values schema: a leaf of the report of
// Schema.Validate.
type Violation struct {
	Location []string // the keys and indexes from the root; none for the values as a whole
	Message  string   // its line of the report, already redacted: "at '<pointer>': <message>"
}

var printer = message.NewPrinter(language.English)

// violations returns the leaves of e, already sorted and redacted. A
// combinator (allOf, anyOf, a $ref) contributes the leaves of its causes.
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

// relocate restores the instance location of the violations whose location
// the validator gives without copying it (jsonschema v6.0.3: propertyNames
// and contentSchema). Such a slice shares the validator's own, which the
// rest of the walk writes over: its length holds, its keys may be those of
// a value read later. The location of every other violation is a copy.
//
// The value is found again in v, the values validated: under prefix, the
// location of the nearest violation above e that is a copy, at the depth
// of the slice, an object that holds the refused name. When several
// values fit, the one the slice still names is taken, else the first, in
// path order, that no violation of the same name took (taken); two
// objects of one depth that hold the same refused name, only one of them
// under propertyNames, may thus be named one for the other.
func relocate(e *jsonschema.ValidationError, v any, prefix []string, taken map[string]bool) {
	var fits func(any) bool
	name := ""
	switch k := e.ErrorKind.(type) {
	case *kind.PropertyNames:
		name = k.Property
		fits = func(x any) bool {
			m, ok := x.(map[string]any)
			_, has := m[name]
			return ok && has
		}
	case *kind.ContentSchema:
		fits = func(x any) bool { _, ok := x.(string); return ok }
	default:
		for _, c := range e.Causes {
			relocate(c, v, e.InstanceLocation, taken)
		}
		return
	}
	// The causes of such a violation are about the name or the decoded
	// content, a value of its own: their locations are not of v.
	var found [][]string
	if at, ok := valueAt(v, prefix); ok {
		pathsAt(at, slices.Clone(prefix), len(e.InstanceLocation)-len(prefix), fits, &found)
	}
	key := func(loc []string) string { return name + "\x00" + strings.Join(loc, "\x00") }
	pick := slices.IndexFunc(found, func(loc []string) bool { return slices.Equal(loc, e.InstanceLocation) && !taken[key(loc)] })
	if pick < 0 {
		pick = slices.IndexFunc(found, func(loc []string) bool { return !taken[key(loc)] })
	}
	if pick < 0 {
		e.InstanceLocation = slices.Clone(e.InstanceLocation)
		return
	}
	taken[key(found[pick])] = true
	e.InstanceLocation = found[pick]
}

// valueAt returns the value of v at the location loc, keys of maps and
// indexes of lists.
func valueAt(v any, loc []string) (any, bool) {
	for _, k := range loc {
		switch t := v.(type) {
		case map[string]any:
			x, ok := t[k]
			if !ok {
				return nil, false
			}
			v = x
		case []any:
			i, err := strconv.Atoi(k)
			if err != nil || i < 0 || i >= len(t) {
				return nil, false
			}
			v = t[i]
		default:
			return nil, false
		}
	}
	return v, true
}

// pathsAt appends to out the locations, depth levels under v, of the
// values that fit, in path order: at is the location of v.
func pathsAt(v any, at []string, depth int, fits func(any) bool, out *[][]string) {
	if depth <= 0 {
		if depth == 0 && fits(v) {
			*out = append(*out, at)
		}
		return
	}
	switch t := v.(type) {
	case map[string]any:
		for _, k := range slices.Sorted(maps.Keys(t)) {
			pathsAt(t[k], append(slices.Clip(at), k), depth-1, fits, out)
		}
	case []any:
		for i, x := range t {
			pathsAt(x, append(slices.Clip(at), strconv.Itoa(i)), depth-1, fits, out)
		}
	}
}

// compileSchema compiles the JSON schema doc (as jsonschema.UnmarshalJSON
// reads it), registered at url, as Helm 4.3 compiles a chart's
// values.schema.json: the draft is the one $schema names, or 2020-12
// without one, formats are asserted for drafts before 2019-09 only, and
// unknown keywords are ignored.
//
// Unlike Helm, it fetches nothing: a $ref to a file, a URL or a urn:
// fails, because the loader knows no scheme.
func compileSchema(url string, doc any) (*jsonschema.Schema, error) {
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.UseLoader(jsonschema.SchemeURLLoader{})
	if err := c.AddResource(url, doc); err != nil {
		return nil, err
	}
	return c.Compile(url)
}

// sortCauses makes the report stable: it sorts the causes of e by instance
// path, then by message, at every level, and the names of the additional
// properties of one message, which the validator gives in map order.
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

// redactCauses keeps sensitive values out of the report. A violation at a
// sensitive location (value.SensitivePath) whose message would print the
// value (a pattern, a format, a numeric bound) prints value.Redacted
// instead.
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

// redacted is a violation whose value must not be printed. For a format,
// it also drops the format's own error, which may quote the value.
type redacted struct{ jsonschema.ErrorKind }

// LocalizedString is the kind's message with value.Redacted for the value.
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

// quote quotes s as the validator does in a message: '...'.
func quote(s string) string {
	s = fmt.Sprintf("%q", s)
	s = strings.ReplaceAll(s, `\"`, `"`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	return "'" + s[1:len(s)-1] + "'"
}

// ValuesError reports values that do not meet their schema (Parameters).
// It carries the error as Helm reports it, and each violation separately
// (Violations), for a caller that maps them to the fields of a form.
type ValuesError struct {
	msg        string
	Violations []Violation
}

func (e *ValuesError) Error() string { return e.msg }

// schemaError builds the error Helm reports when values violate the
// schemas of their charts, nil when none does. violations maps each chart
// to its violations ("" when it has none). what names the values checked
// ("values").
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
