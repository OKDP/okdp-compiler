// Mapping: the sections an OKDP mapping holds besides vars and errors
// (package compile), and the host functions OKDP adds to the language.
// The embedded mapping.schema.json describes the sections. A package's
// mapping is read from two files: okdp.yaml gives vars, errors and
// every section but output; the values file gives output. The rule
// set's own templates are written in the same language, with the same
// functions.

package okdp

import (
	"cmp"
	"context"
	"errors"
	"maps"
	"slices"
	"strings"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
	"github.com/okdp/cel2yaml"

	"github.com/okdp/okdp-compiler/internal/compile"
	ruleset "github.com/okdp/okdp-compiler/internal/rules"
	"github.com/okdp/okdp-compiler/value"
)

// sections lists the sections of a mapping the compiler reads itself,
// besides vars and errors. The sections of the rule set come after them.
// Any other top-level key fails the compile.
var sections = []string{"output", "instance", "provides", "objects"}

// The values file of a package holds the output section of the mapping.
// It is read the same way under either name.
const (
	// valuesFile may hold expressions. A Definition that names no values
	// file gets this name.
	valuesFile = "values.cel.yaml"
	// plainValuesFile holds no expression, by convention. One that holds
	// any gives a warning.
	plainValuesFile = "values.yaml"
)

// valuesName returns the name of d's values file, which starts its errors.
func (d *Definition) valuesName() string { return cmp.Or(d.ValuesFile, valuesFile) }

// sources returns the mapping of d: okdp.yaml with its package keys
// skipped, then the values file as the output section.
func (d *Definition) sources() []compile.Source {
	var srcs []compile.Source
	if d.Okdp != nil {
		srcs = append(srcs, compile.Source{Name: "okdp.yaml", Data: d.Okdp, Skip: packageKeys})
	}
	if d.Values != nil {
		srcs = append(srcs, compile.Source{Name: d.valuesName(), Data: d.Values, Section: "output"})
	}
	return srcs
}

// runMapping runs the mapping read from srcs on scope, in the compile's
// session, and checks its sections against the mapping schema.
//
// res holds the sections by name. A null section (absent, or removed by
// $if) is not set:
//
//	output       chart -> computed values, or null
//	instance     url, usage
//	provides     [{contract, name?, values, secretRef?, secret?}]
//	objects      OKDP's own objects, whole manifests
//	<section>    a section of the rule set (RuleSet.Sections)
//
// written lists the keys that output writes at its top level, whether
// they are kept or not.
func runMapping(ctx context.Context, rules *RuleSet, session *cel2yaml.Session, scope M, srcs ...compile.Source) (res M, written []string, err error) {
	doc, err := compile.Eval(ctx, session, scope, slices.Concat(sections, rules.Sections()), srcs...)
	if err != nil {
		return nil, nil, err
	}
	maps.DeleteFunc(doc.Sections, func(_ string, v any) bool { return v == nil })
	res, err = schemaValues(rules.MappingSchema(), "the mapping's sections don't meet the specifications of mapping.schema.json", doc.Sections)
	return res, doc.Written["output"], err
}

// functions returns the host functions OKDP adds for the instance inst.
// They run in the compile's session, read through *session once it is
// open. They read only what every document of a compile sees alike (the
// release, its namespace, the service), so the mapping and the compiler's
// templates share them. Each call counts in the session's cost
// (cel2yaml.Costed).
func functions(ctx context.Context, inst *instance, session **cel2yaml.Session) []cel.EnvOption {
	release := inst.release
	opts := []cel.EnvOption{
		// okdpFullname, not fullname: mappings bind their own `fullname`.
		cel2yaml.Costed(cel.Function("okdpFullname",
			cel.Overload("okdpFullname_void", []*cel.Type{}, cel.StringType,
				cel.FunctionBinding(func(...ref.Val) ref.Val { return types.String(fullname(release, "")) })),
			cel.Overload("okdpFullname_dyn", []*cel.Type{cel.DynType}, cel.StringType,
				cel2yaml.Unary(func(v any) (any, error) { return fullname(release, value.Str(v)), nil }))), "okdpFullname_void", "okdpFullname_dyn"),
		cel2yaml.Costed(cel.Function("okdpGeneratedSecret", cel.Overload("okdpGeneratedSecret_map", []*cel.Type{cel.DynType}, cel.DynType,
			cel2yaml.Unary(func(v any) (any, error) {
				if inst.rules.GeneratedSecret().Data == nil {
					return nil, errors.New("okdpGeneratedSecret: the rule set of the compile has no such template")
				}
				args, err := schemaValues(inst.rules.MappingSchema(), "okdpGeneratedSecret: the arguments don't meet the specifications of mapping.schema.json", M{"okdpGeneratedSecret": v})
				if err != nil {
					return nil, err
				}
				return generatedSecret(ctx, *session, inst, value.AsMap(args["okdpGeneratedSecret"]))
			}))), "okdpGeneratedSecret_map"),
	}
	opts = append(opts, labelFunctions(inst.rules, inst.svc.Name, release)...)
	opts = append(opts, nameFunctions()...)
	opts = append(opts, fragmentFunctions(ctx, inst, session)...)
	return append(opts, documentFunctions(ctx, inst)...)
}

// generatedSecret implements okdpGeneratedSecret, from the rule set's
// template. args is already checked against mapping.schema.json.
func generatedSecret(ctx context.Context, session *cel2yaml.Session, inst *instance, args M) ([]any, error) {
	scope := M{"args": args, "release": M{"name": inst.release, "namespace": inst.namespace}, "labels": inst.rules.Labels(inst.svc.Name, inst.release, "", false)}
	v, err := evalTemplate(ctx, session, inst.rules.GeneratedSecret(), scope)
	return value.AsList(v["objects"]), err
}

// documentFunctions returns the functions that the rule set computes
// with a document. A document sees the argument of the call, the release
// and what the mapping sees as platform.
func documentFunctions(ctx context.Context, inst *instance) []cel.EnvOption {
	var opts []cel.EnvOption
	for _, f := range inst.rules.DocumentFuncs() {
		id := f.Name + "_dyn"
		opts = append(opts, cel2yaml.Costed(cel.Function(f.Name, cel.Overload(id, []*cel.Type{cel.DynType}, cel.DynType,
			cel2yaml.Unary(func(v any) (any, error) {
				values, err := inst.rules.Values(ctx, f.Document, M{"arg": v, "release": inst.scope["release"], "platform": inst.scope["platform"]})
				if err != nil {
					return nil, err
				}
				return values[f.Result], nil
			}))), id))
	}
	return opts
}

// evalTemplate evaluates t, a template of the rule set, on scope and
// returns its values section. Its errors start with the template's name.
func evalTemplate(ctx context.Context, session *cel2yaml.Session, t ruleset.Template, scope M) (M, error) {
	doc, err := compile.Eval(ctx, session, scope, []string{"values"}, compile.Source{Name: t.Name, Data: t.Data})
	if err != nil {
		return nil, err
	}
	return value.AsMap(doc.Sections["values"]), nil
}

// fullname implements okdpFullname: "<release>-<suffix>", or the release
// alone without a suffix, cut to 63 characters, without a trailing "-".
func fullname(release, suffix string) string {
	s := release
	if suffix != "" {
		s = release + "-" + suffix
	}
	return truncName(s, 63)
}

// truncName cuts s as a chart cuts a name: `trunc n | trimSuffix "-"`.
func truncName(s string, n int) string {
	return strings.TrimSuffix(s[:min(n, len(s))], "-")
}
