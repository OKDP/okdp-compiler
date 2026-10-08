// Mapping: what an OKDP mapping holds besides let and errors (package
// compile), and the host functions OKDP adds to the language:
//
//	output (chart -> values),
//	instance (url, usage) and provides (the connections the instance publishes),
//	objects (OKDP's own objects, whole manifests),
//	oidcClients (the OAuth clients of the service, oidc.go).
//
// The compiler's own templates (oidc-dcr.cel.yaml) are written in the same
// language, with the same functions: let, errors, then the values they
// compute (compile.Session.EvalTemplate).
package okdp

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"

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
	clients  []any // the OAuth clients (oidcClients), checked by oidc.go
}

// sections: the sections of a mapping, besides let and errors: any other
// top-level key fails the compile.
var sections = []string{"output", "instance", "provides", "objects", "oidcClients"}

// runMapping: runs a mapping (values.cel.yaml) on scope, what it sees
// (mappingScope), in the compile's session, its sections in order, each of
// its shape (section): null (absent, $if) is empty, a value of another
// shape is refused. A null chart of output is left out, as by $if;
// instance's url and usage are strings when set.
func runMapping(session *compile.Session, b []byte, scope M) (*mapped, error) {
	d, err := session.Load(b, scope, sections...)
	if err != nil {
		return nil, err
	}
	res := &mapped{output: M{}}
	charts, err := section[M](d, "output", "a map of charts to their values")
	if err != nil {
		return nil, err
	}
	for _, key := range slices.Sorted(maps.Keys(charts)) {
		if charts[key] == nil {
			continue
		}
		if res.output[key], err = shaped[M]("output: "+key, "a map of the chart's values", charts[key]); err != nil {
			return nil, err
		}
	}
	if res.instance, err = section[M](d, "instance", "a map (url, usage)"); err != nil {
		return nil, err
	}
	// url and usage are text: another value would be printed.
	for _, key := range []string{"url", "usage"} {
		if _, err := shaped[string]("instance: "+key, "a string", res.instance[key]); err != nil {
			return nil, err
		}
	}
	if res.provides, err = section[[]any](d, "provides", "a list of outputs"); err != nil {
		return nil, err
	}
	if res.objects, err = section[[]any](d, "objects", "a list of objects"); err != nil {
		return nil, err
	}
	if res.clients, err = section[[]any](d, "oidcClients", "a list of OAuth clients"); err != nil {
		return nil, err
	}
	// Every object is a whole manifest: a kind and a name at least.
	for i, o := range res.objects {
		m, ok := o.(M)
		if !ok || value.Str(m["kind"]) == "" || value.Str(value.Dig(m, "metadata.name")) == "" {
			return nil, fmt.Errorf("objects[%d]: not an object with a kind and a metadata.name: %s", i, value.Short(o))
		}
	}
	return res, nil
}

// section: the section name of the mapping d, evaluated (compile.Doc.Section),
// as a T (shaped, want: what it must be).
func section[T any](d *compile.Doc, name, want string) (T, error) {
	v, err := d.Section(name)
	if err != nil {
		var zero T
		return zero, err
	}
	return shaped[T](name, want, v)
}

// shaped: v, the value at where, as a T: its zero value when v is null, an
// error when v is of another shape (want: what it must be).
func shaped[T any](where, want string, v any) (T, error) {
	t, ok := v.(T)
	if !ok && v != nil {
		return t, fmt.Errorf("%s: must be %s, got %s", where, want, value.Short(v))
	}
	return t, nil
}

// functions: the host functions OKDP adds for the instance inst, those of
// the compile's session (CompileContext): okdpFullname,
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
				cel.UnaryBinding(func(v ref.Val) ref.Val { return types.String(fullname(release, compile.Text(v))) }))),
		cel.Function("okdpGeneratedSecret", cel.Overload("okdpGeneratedSecret_map", []*cel.Type{cel.DynType}, cel.DynType,
			cel.UnaryBinding(func(v ref.Val) ref.Val {
				args, err := shaped[M]("okdp.generatedSecret", "a map of arguments ("+strings.Join(fieldNames(secretArgs), ", ")+")", compile.Native(v))
				var out []any
				if err == nil {
					out, err = generatedSecret(inst, args)
				}
				if err != nil {
					return types.NewErr("%s", err.Error())
				}
				return types.DefaultTypeAdapter.NativeToValue(out)
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
