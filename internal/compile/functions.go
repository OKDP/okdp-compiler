// Functions: the host functions every document may call besides CEL's own,
// each deterministic and hermetic, and each a binding of the Helm template
// function it reproduces, whose Go code is in helmfn (testdata/helm/functions
// holds what `helm template` prints for the same inputs). A caller adds its
// own (NewSession).
package compile

import (
	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"

	"github.com/okdp/okdp-compiler/internal/helmfn"
	"github.com/okdp/okdp-compiler/internal/value"
)

// Text: a CEL value as text, as Helm's sprig functions take it: value.Str
// of its data (Native), bytes as they are.
func Text(v ref.Val) string {
	return value.Str(Native(v))
}

// encoder: a CEL function of one value returning the text f computes; f's
// error fails the expression (Helm would print "").
func encoder(name string, f func(any) (string, error)) cel.EnvOption {
	return cel.Function(name, cel.Overload(name+"_dyn", []*cel.Type{cel.DynType}, cel.StringType,
		cel.UnaryBinding(func(v ref.Val) ref.Val {
			s, err := f(Native(v))
			if err != nil {
				return types.NewErr("%s: %s", name, err.Error())
			}
			return types.String(s)
		})))
}

// hostFunctions: sha256 (Helm's sha256sum of str(v), null being ""),
// toJson, toPrettyJson, toYaml and quote (a null gives "", as in sprig).
func hostFunctions() []cel.EnvOption {
	return []cel.EnvOption{
		cel.Function("sha256", cel.Overload("sha256_dyn", []*cel.Type{cel.DynType}, cel.StringType,
			cel.UnaryBinding(func(v ref.Val) ref.Val { return types.String(helmfn.Sha256Sum(Text(v))) }))),
		encoder("toJson", helmfn.ToJSON),
		encoder("toPrettyJson", helmfn.ToPrettyJSON),
		encoder("toYaml", helmfn.ToYAML),
		cel.Function("quote", cel.Overload("quote_dyn", []*cel.Type{cel.DynType}, cel.StringType,
			cel.UnaryBinding(func(v ref.Val) ref.Val {
				if _, ok := v.(types.Null); ok {
					return types.String("")
				}
				return types.String(helmfn.Quote(Text(v)))
			}))),
	}
}
