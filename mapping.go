// Mapping: what an OKDP mapping holds besides let and errors (package
// compile), and the host functions OKDP adds to the language:
//
//	output (chart -> values),
//	instance (url, usage) and provides (the connections the instance publishes),
//	objects (OKDP's own objects, whole manifests),
//	oidcClients (the OAuth clients of the service, oidc.go),
//
// their shapes described by the embedded mapping.schema.json.
//
// The compiler's own templates (oidc-dcr.cel.yaml) are written in the same
// language, with the same functions: let, errors, then the values they
// compute (compile.Session.EvalTemplate).

package compiler

import (
	_ "embed"
	"sync"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"

	"github.com/okdp/okdp-compiler/internal/chart"
	"github.com/okdp/okdp-compiler/internal/compile"
	"github.com/okdp/okdp-compiler/internal/helmfn"
	"github.com/okdp/okdp-compiler/internal/value"
)

// mapped: what a mapping produces.
type mapped struct {
	output   M     // chart -> computed values
	instance M     // url, usage
	provides []any // [{contract, name?, values, secretRef?, secret?}]
	objects  []any // OKDP's own objects, whole manifests
	clients  []any // the OAuth clients (oidcClients), checked further by oidc.go
}

// sections: the sections of a mapping, besides let and errors: any other
// top-level key fails the compile.
var sections = []string{"output", "instance", "provides", "objects", "oidcClients"}

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

// runMapping: runs a mapping (values.cel.yaml) on scope, what it sees
// (mappingScope), in the compile's session, its sections in order, then
// checks them against mappingSchema (schemaValues): a null (absent, $if) is
// not set, a section not set is empty, a null chart of output is left out.
func runMapping(session *compile.Session, b []byte, scope M) (*mapped, error) {
	d, err := session.Load(b, scope, sections...)
	if err != nil {
		return nil, err
	}
	doc := M{}
	for _, s := range sections {
		if doc[s], err = d.Section(s); err != nil {
			return nil, err
		}
	}
	if doc, err = schemaValues(mappingSchema, "the mapping's sections don't meet the specifications of mapping.schema.json", doc); err != nil {
		return nil, err
	}
	return &mapped{
		output:   value.AsMap(doc["output"]),
		instance: value.AsMap(doc["instance"]),
		provides: value.AsList(doc["provides"]),
		objects:  value.AsList(doc["objects"]),
		clients:  value.AsList(doc["oidcClients"]),
	}, nil
}

// functions: the host functions OKDP adds for the instance inst, those of
// the compile's session (Compile): okdpFullname,
// okdpGeneratedSecret (secrets.go), okdpLabels and okdpSelectorLabels
// (labels.go), oidcClientSecret (oidc.go). They read only what the
// documents of a compile see alike (the release, its namespace, the
// service, the OAuth client mode): the conditions, the mapping and the
// client templates, whose scope adds client and names, share them.
func functions(inst *instance) []cel.EnvOption {
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
				return generatedSecret(inst, value.AsMap(args["okdpGeneratedSecret"]))
			}))),
	}
	opts = append(opts, labelFunctions(inst.svc, release)...)
	return append(opts, oidcFunctions(inst)...)
}

// fullname: okdpFullname, "<release>-<suffix>" (the release alone without
// a suffix) cut to 63 characters, without a trailing "-".
func fullname(release, suffix string) string {
	s := release
	if suffix != "" {
		s = release + "-" + suffix
	}
	return helmfn.TruncName(s, 63)
}
