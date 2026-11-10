// Mapping: what an OKDP mapping holds besides vars and errors (package
// compile), and the host functions OKDP adds to the language:
//
//	output (chart -> values), the values file,
//	instance (url, usage) and provides (the connections the instance publishes),
//	objects (OKDP's own objects, whole manifests),
//	oidcClients (the OAuth clients of the service, oidc.go),
//
// their shapes described by the embedded mapping.schema.json. A package's
// mapping is read from okdp.yaml (vars, errors and every section but
// output, its package keys skipped) and its values file (output),
// values.cel.yaml or values.yaml.
//
// The compiler's own templates (oidc-dcr.cel.yaml,
// generated-secret.cel.yaml) are written in the same
// language, with the same functions: vars, errors, then the values they
// compute (compile.Eval), their errors under their file's name.

package okdp

import (
	"cmp"
	"maps"
	"slices"
	"strings"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
	"github.com/okdp/cel2yaml"

	"github.com/okdp/okdp-compiler/internal/compile"
	"github.com/okdp/okdp-compiler/internal/value"
)

// sections: the sections of a mapping the compiler reads itself, besides
// vars and errors, those of the rule set after them: any other top-level
// key fails the compile.
var sections = []string{"output", "instance", "provides", "objects"}

// The values file of a package, the mapping's output section: one of
// these, read alike.
const (
	valuesFile      = "values.cel.yaml" // with expressions; the name of a Definition's values file when it gives none
	plainValuesFile = "values.yaml"     // without, conventionally: one that holds any gives a warning (LoadDefinition)
)

// valuesName: the name of d's values file, the first segment of its
// errors: Definition.ValuesFile, values.cel.yaml when empty (a
// Definition built in memory).
func (d *Definition) valuesName() string { return cmp.Or(d.ValuesFile, valuesFile) }

// sources: the mapping of d: okdp.yaml, its package keys skipped, then
// the values file, the output section; each when the package has it.
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

// runMapping: runs a mapping, read from srcs (Definition.sources: the
// name of each the first segment of its errors), on scope, what it sees
// (instance.scope), in the compile's session, its sections in order, then
// checks them against mappingSchema (schemaValues): the sections, by name,
// a null one (absent, $if) not set. output (chart -> computed values, or
// null), instance (url, usage), provides ([{contract, name?, values,
// secretRef?, secret?}]), objects (OKDP's own objects, whole manifests),
// oidcClients (checked further by oidc.go). written: the keys output
// writes at its top level (compile.Doc.Written), kept or not.
func runMapping(rules *RuleSet, session *cel2yaml.Session, scope M, srcs ...compile.Source) (res M, written []string, err error) {
	doc, err := compile.Eval(session, scope, slices.Concat(sections, rules.sections), srcs...)
	if err != nil {
		return nil, nil, err
	}
	maps.DeleteFunc(doc.Sections, func(_ string, v any) bool { return v == nil })
	res, err = schemaValues(rules.mappingSchema, "the mapping's sections don't meet the specifications of mapping.schema.json", doc.Sections)
	return res, doc.Written["output"], err
}

// functions: the host functions OKDP adds for the instance inst, those of
// the compile's session (Compile), *session once it is open: okdpFullname,
// okdpGeneratedSecret (generatedSecret, evaluated in the session),
// okdpLabels and okdpSelectorLabels (labels.go), oidcClientSecret
// (oidc.go). They read only what the documents of a compile see alike (the
// release, its namespace, the service, the OAuth client mode): the mapping
// and the compiler's templates share them. Each call counts in the
// session's cost as the compiler's own host functions (cel2yaml.Costed).
func functions(inst *instance, session **cel2yaml.Session) []cel.EnvOption {
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
				args, err := schemaValues(inst.rules.mappingSchema, "okdpGeneratedSecret: the arguments don't meet the specifications of mapping.schema.json", M{"okdpGeneratedSecret": v})
				if err != nil {
					return nil, err
				}
				return generatedSecret(*session, inst, value.AsMap(args["okdpGeneratedSecret"]))
			}))), "okdpGeneratedSecret_map"),
	}
	opts = append(opts, labelFunctions(inst.rules, inst.svc.Name, release)...)
	opts = append(opts, nameFunctions()...)
	opts = append(opts, fragmentFunctions(inst, session)...)
	return append(opts, documentFunctions(inst)...)
}

// generatedSecret: okdpGeneratedSecret, the objects of a generated Secret
// for the instance inst (the rule set's template), args checked against
// mapping.schema.json (okdpGeneratedSecret), in the compile's session.
func generatedSecret(session *cel2yaml.Session, inst *instance, args M) ([]any, error) {
	scope := M{"args": args, "release": M{"name": inst.release, "namespace": inst.namespace}, "labels": inst.rules.labels(inst.svc.Name, inst.release, "", false)}
	v, err := evalTemplate(session, inst.rules.generatedSecret, scope)
	return value.AsList(v["objects"]), err
}

// documentFunctions: the functions of the rule set a document computes, on
// their argument, the release and what the mapping sees as platform; each
// call counts in the session's cost as the compiler's own host functions.
func documentFunctions(inst *instance) []cel.EnvOption {
	var opts []cel.EnvOption
	for _, f := range inst.rules.documentFuncs {
		id := f.name + "_dyn"
		opts = append(opts, cel2yaml.Costed(cel.Function(f.name, cel.Overload(id, []*cel.Type{cel.DynType}, cel.DynType,
			cel2yaml.Unary(func(v any) (any, error) {
				values, err := inst.rules.values(f.document, M{"arg": v, "release": inst.scope["release"], "platform": inst.scope["platform"]})
				if err != nil {
					return nil, err
				}
				return values[f.result], nil
			}))), id))
	}
	return opts
}

// evalTemplate: t, a template of the rule set (the
// mapping language: vars, errors, then its one section, values), evaluated
// on scope in the compile's session: its values; its errors start with
// name.
func evalTemplate(session *cel2yaml.Session, t template, scope M) (M, error) {
	doc, err := compile.Eval(session, scope, []string{"values"}, compile.Source{Name: t.name, Data: t.data})
	if err != nil {
		return nil, err
	}
	return value.AsMap(doc.Sections["values"]), nil
}

// fullname: okdpFullname, "<release>-<suffix>" (the release alone without
// a suffix) cut to 63 characters, without a trailing "-".
func fullname(release, suffix string) string {
	s := release
	if suffix != "" {
		s = release + "-" + suffix
	}
	return truncName(s, 63)
}

// truncName: s cut as a chart cuts a name, `trunc n | trimSuffix "-"`: to
// n bytes, without a trailing "-".
func truncName(s string, n int) string {
	return strings.TrimSuffix(s[:min(n, len(s))], "-")
}
