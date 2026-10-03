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
// compute (compile.EvalTemplate).
package okdp

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"

	"github.com/okdp/okdp-compiler/internal/compile"
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

// runMapping: runs a mapping (mapping.cel.yaml) on the context ctx, within
// budget (nil: none), its sections in order, each of its shape: null
// (absent, $if) is empty, a value of another shape is refused. A null chart
// of output is left out, as by $if.
func runMapping(budget *compile.Budget, b []byte, ctx M) (*mapped, error) {
	d, err := compile.Load(b, ctx, budget, functions(ctx)...)
	if err != nil {
		return nil, err
	}
	res := &mapped{output: M{}}
	for _, k := range []string{"output", "instance", "provides", "objects", "oidcClients"} {
		v, err := d.Section(k)
		if err != nil {
			return nil, err
		}
		switch k {
		case "output":
			var charts M
			if charts, err = shaped[M]("output", "a map of charts to their values", v); err != nil {
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
		case "instance":
			res.instance, err = shaped[M]("instance", "a map (url, usage)", v)
		case "provides":
			res.provides, err = shaped[[]any]("provides", "a list of outputs", v)
		case "objects":
			res.objects, err = shaped[[]any]("objects", "a list of objects", v)
		case "oidcClients":
			res.clients, err = shaped[[]any]("oidcClients", "a list of OAuth clients", v)
		}
		if err != nil {
			return nil, err
		}
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

// shaped: v, the value at where, as a T: its zero value when v is null, an
// error when v is of another shape (want: what it must be).
func shaped[T any](where, want string, v any) (T, error) {
	t, ok := v.(T)
	if !ok && v != nil {
		return t, fmt.Errorf("%s: must be %s, got %s", where, want, value.Short(v))
	}
	return t, nil
}

// functions: the host functions OKDP adds for a document of the context
// ctx: okdpFullname, okdpGeneratedSecret (secrets.go), okdpLabels and
// okdpSelectorLabels (labels.go), oidcClientSecret (oidc.go).
func functions(ctx M) []cel.EnvOption {
	release := value.Str(value.Dig(ctx, "release.name"))
	opts := []cel.EnvOption{
		// okdpFullname, not fullname: mappings bind their own `fullname`.
		cel.Function("okdpFullname",
			cel.Overload("okdpFullname_void", []*cel.Type{}, cel.StringType,
				cel.FunctionBinding(func(...ref.Val) ref.Val { return types.String(fullname(release, "")) })),
			cel.Overload("okdpFullname_dyn", []*cel.Type{cel.DynType}, cel.StringType,
				cel.UnaryBinding(func(v ref.Val) ref.Val { return types.String(fullname(release, compile.Text(v))) }))),
		cel.Function("okdpGeneratedSecret", cel.Overload("okdpGeneratedSecret_map", []*cel.Type{cel.DynType}, cel.DynType,
			cel.UnaryBinding(func(v ref.Val) ref.Val {
				out, err := generatedSecret(ctx, value.AsMap(compile.Native(v)))
				if err != nil {
					return types.NewErr("%s", err.Error())
				}
				return types.DefaultTypeAdapter.NativeToValue(out)
			}))),
	}
	opts = append(opts, labelFunctions(ctxService(ctx), release)...)
	return append(opts, oidcFunctions(ctx)...)
}

// fullname: okdpFullname, "<release>-<suffix>" (the release alone without
// a suffix) cut to 63 characters, without a trailing "-".
func fullname(release, suffix string) string {
	s := release
	if suffix != "" {
		s = release + "-" + suffix
	}
	return strings.TrimSuffix(trunc(s, 63), "-")
}
