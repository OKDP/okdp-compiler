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
// compute (compile.Session.Eval), their errors under their file's name.

package okdp

import (
	"cmp"
	_ "embed"
	"maps"
	"strings"
	"sync"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"

	"github.com/okdp/okdp-compiler/internal/chart"
	"github.com/okdp/okdp-compiler/internal/compile"
	"github.com/okdp/okdp-compiler/internal/value"
)

// sections: the sections of a mapping, besides vars and errors: any other
// top-level key fails the compile.
var sections = []string{"output", "instance", "provides", "objects", "oidcClients"}

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

// mappingSchemaFile: the schema of what a mapping writes that the compiler
// reads: the sections, and the argument of okdpGeneratedSecret; the
// reference of package authors, its descriptions their meaning.
//
//go:embed mapping.schema.json
var mappingSchemaFile []byte

// mappingSchema: mappingSchemaFile parsed once, for every compile
// (chart.ParseSchema).
var mappingSchema = sync.OnceValues(func() (*chart.Schema, error) {
	return chart.ParseSchema("mapping.schema.json", mappingSchemaFile)
})

// runMapping: runs a mapping, read from srcs (Definition.sources: the
// name of each the first segment of its errors), on scope, what it sees
// (instance.scope), in the compile's session, its sections in order, then
// checks them against mappingSchema (schemaValues): the sections, by name,
// a null one (absent, $if) not set. output (chart -> computed values, or
// null), instance (url, usage), provides ([{contract, name?, values,
// secretRef?, secret?}]), objects (OKDP's own objects, whole manifests),
// oidcClients (checked further by oidc.go). written: the keys output
// writes at its top level (compile.Doc.Written), kept or not.
func runMapping(session *compile.Session, scope M, srcs ...compile.Source) (res M, written []string, err error) {
	doc, err := session.Eval(scope, sections, srcs...)
	if err != nil {
		return nil, nil, err
	}
	maps.DeleteFunc(doc.Sections, func(_ string, v any) bool { return v == nil })
	res, err = schemaValues(mappingSchema, "the mapping's sections don't meet the specifications of mapping.schema.json", doc.Sections)
	return res, doc.Written["output"], err
}

// functions: the host functions OKDP adds for the instance inst, those of
// the compile's session (Compile), *session once it is open: okdpFullname,
// okdpGeneratedSecret (generatedSecret, evaluated in the session),
// okdpLabels and okdpSelectorLabels (labels.go), oidcClientSecret
// (oidc.go). They read only what the documents of a compile see alike (the
// release, its namespace, the service, the OAuth client mode): the mapping
// and the compiler's templates share them.
func functions(inst *instance, session **compile.Session) []cel.EnvOption {
	release := inst.release
	opts := []cel.EnvOption{
		// okdpFullname, not fullname: mappings bind their own `fullname`.
		cel.Function("okdpFullname",
			cel.Overload("okdpFullname_void", []*cel.Type{}, cel.StringType,
				cel.FunctionBinding(func(...ref.Val) ref.Val { return types.String(fullname(release, "")) })),
			cel.Overload("okdpFullname_dyn", []*cel.Type{cel.DynType}, cel.StringType,
				compile.Unary(func(v any) (any, error) { return fullname(release, value.Str(v)), nil }))),
		cel.Function("okdpGeneratedSecret", cel.Overload("okdpGeneratedSecret_map", []*cel.Type{cel.DynType}, cel.DynType,
			compile.Unary(func(v any) (any, error) {
				args, err := schemaValues(mappingSchema, "okdpGeneratedSecret: the arguments don't meet the specifications of mapping.schema.json", M{"okdpGeneratedSecret": v})
				if err != nil {
					return nil, err
				}
				return generatedSecret(*session, inst, value.AsMap(args["okdpGeneratedSecret"]))
			}))),
	}
	opts = append(opts, labelFunctions(inst.svc.Name, release)...)
	return append(opts, oidcFunctions(inst)...)
}

// secretTemplate: the objects of okdpGeneratedSecret (the mapping
// language).
//
//go:embed generated-secret.cel.yaml
var secretTemplate []byte

// generatedSecret: okdpGeneratedSecret, the objects of a generated Secret
// for the instance inst (secretTemplate), args checked against
// mapping.schema.json (okdpGeneratedSecret), in the compile's session.
func generatedSecret(session *compile.Session, inst *instance, args M) ([]any, error) {
	scope := M{"args": args, "release": M{"name": inst.release, "namespace": inst.namespace}, "labels": labels(inst.svc.Name, inst.release, "", false)}
	v, err := evalTemplate(session, "generated-secret.cel.yaml", secretTemplate, scope)
	return value.AsList(v["objects"]), err
}

// evalTemplate: b, the template of the compiler in the file name (the
// mapping language: vars, errors, then its one section, values), evaluated
// on scope in the compile's session: its values; its errors start with
// name.
func evalTemplate(session *compile.Session, name string, b []byte, scope M) (M, error) {
	doc, err := session.Eval(scope, []string{"values"}, compile.Source{Name: name, Data: b})
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
