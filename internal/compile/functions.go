// Functions: the host functions every document may call besides CEL's own,
// each deterministic and hermetic: str and first, and each the Go code of
// the Helm template function it reproduces (testdata/helm/functions holds
// what `helm template` prints for the same inputs). A caller adds its own
// (NewSession).
package compile

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
	sigsyaml "sigs.k8s.io/yaml"

	"github.com/okdp/okdp-compiler/internal/value"
)

// Unary: the binding of a host function of one argument: f of the argument
// as data (toData), its result converted back to CEL. An argument without
// a data form, or f's error, fails the expression.
func Unary[T any](f func(any) (T, error)) cel.OverloadOpt {
	return cel.UnaryBinding(func(v ref.Val) ref.Val {
		d, err := toData(v)
		var r T
		if err == nil {
			r, err = f(d)
		}
		if err != nil {
			return types.WrapErr(err)
		}
		return types.DefaultTypeAdapter.NativeToValue(r)
	})
}

// Func: the host function name, of one argument of any type, returning a
// value of the type out: f of the argument (Unary).
func Func[T any](name string, out *cel.Type, f func(any) (T, error)) cel.EnvOption {
	return cel.Function(name, cel.Overload(name+"_dyn", []*cel.Type{cel.DynType}, out, Unary(f)))
}

// hostFunctions: str (value.Str), first (the first item of a list that is
// not empty, "" without one or for a value that is not a list), sha256
// (Helm's sha256sum of str(v), null being ""), toJson (Go's encoding/json,
// as Helm: keys sorted, <, >, & escaped), toPrettyJson (indented by two
// spaces, as sprig), toYaml (sigs.k8s.io/yaml without the last newline, as
// Helm) and quote (Go's %q; a null gives "", as in sprig).
func hostFunctions() []cel.EnvOption {
	return []cel.EnvOption{
		Func("str", cel.StringType, func(v any) (string, error) { return value.Str(v), nil }),
		Func("first", cel.DynType, func(v any) (any, error) {
			for _, x := range value.AsList(v) {
				if value.Truthy(x) {
					return x, nil
				}
			}
			return "", nil
		}),
		Func("sha256", cel.StringType, func(v any) (string, error) { return fmt.Sprintf("%x", sha256.Sum256([]byte(value.Str(v)))), nil }),
		Func("toJson", cel.StringType, func(v any) (string, error) {
			b, err := json.Marshal(v)
			return string(b), err
		}),
		Func("toPrettyJson", cel.StringType, func(v any) (string, error) {
			b, err := json.MarshalIndent(v, "", "  ")
			return string(b), err
		}),
		Func("toYaml", cel.StringType, func(v any) (string, error) {
			b, err := sigsyaml.Marshal(v)
			return string(bytes.TrimSuffix(b, []byte("\n"))), err
		}),
		Func("quote", cel.StringType, func(v any) (string, error) {
			if v == nil {
				return "", nil
			}
			return fmt.Sprintf("%q", value.Str(v)), nil
		}),
	}
}
