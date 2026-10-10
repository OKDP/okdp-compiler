// Functions: the host functions every document may call besides CEL's own,
// each deterministic and hermetic, and each a binding of the Helm template
// function it reproduces, whose Go code is in helmfn (testdata/helm/functions
// holds what `helm template` prints for the same inputs). A caller adds its
// own (NewSession).
package compile

import (
	"fmt"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"

	"github.com/okdp/okdp-compiler/internal/helmfn"
	"github.com/okdp/okdp-compiler/internal/value"
)

// Unary: the binding of a host function of one argument: f of the argument
// as data (toData), its result converted back to CEL. An argument without
// a data form, or f's error, fails the expression.
func Unary(f func(any) (any, error)) cel.OverloadOpt {
	return cel.UnaryBinding(func(v ref.Val) ref.Val {
		d, err := toData(v)
		if err == nil {
			d, err = f(d)
		}
		if err != nil {
			return types.WrapErr(err)
		}
		return types.DefaultTypeAdapter.NativeToValue(d)
	})
}

// encoder: a CEL function of one value returning the text f computes; f's
// error fails the expression (Helm would print "").
func encoder(name string, f func(any) (string, error)) cel.EnvOption {
	return cel.Function(name, cel.Overload(name+"_dyn", []*cel.Type{cel.DynType}, cel.StringType,
		Unary(func(v any) (any, error) {
			s, err := f(v)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", name, err)
			}
			return s, nil
		})))
}

// hostFunctions: sha256 (Helm's sha256sum of str(v), null being ""),
// toJson, toPrettyJson, toYaml and quote (a null gives "", as in sprig).
func hostFunctions() []cel.EnvOption {
	return []cel.EnvOption{
		cel.Function("sha256", cel.Overload("sha256_dyn", []*cel.Type{cel.DynType}, cel.StringType,
			Unary(func(v any) (any, error) { return helmfn.Sha256Sum(value.Str(v)), nil }))),
		encoder("toJson", helmfn.ToJSON),
		encoder("toPrettyJson", helmfn.ToPrettyJSON),
		encoder("toYaml", helmfn.ToYAML),
		cel.Function("quote", cel.Overload("quote_dyn", []*cel.Type{cel.DynType}, cel.StringType,
			Unary(func(v any) (any, error) {
				if v == nil {
					return "", nil
				}
				return helmfn.Quote(value.Str(v)), nil
			}))),
	}
}
