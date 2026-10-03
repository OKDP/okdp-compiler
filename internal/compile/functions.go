// Functions: the host functions every document may call besides CEL's own,
// each deterministic and hermetic, and each the Go code of the Helm template
// function it reproduces (testdata/helm/functions holds what `helm template`
// prints for the same inputs). A caller adds its own (Load).
package compile

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	sigsyaml "sigs.k8s.io/yaml"

	"github.com/okdp/okdp-compiler/internal/value"
)

// sha256Hex: Helm's sha256sum of str(v): the hex SHA-256 of the text (null
// is "").
func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// toJSON: Helm's toJson: Go's encoding/json (keys sorted, compact, <, >, &,
// U+2028 and U+2029 escaped).
func toJSON(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// toPrettyJSON: sprig's toPrettyJson: encoding/json indented by two spaces.
func toPrettyJSON(v any) (string, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// toYAML: Helm's toYaml: sigs.k8s.io/yaml (keys sorted, YAML 1.1 quoting),
// without the last newline.
func toYAML(v any) (string, error) {
	b, err := sigsyaml.Marshal(v)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(string(b), "\n"), nil
}

// quote: sprig's quote: Go's %q of the text (sprig gives "" for a null,
// nothing rather than two quotes).
func quote(text string) string {
	return fmt.Sprintf("%q", text)
}

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

// hostFunctions: sha256, toJson, toPrettyJson, toYaml and quote.
func hostFunctions() []cel.EnvOption {
	return []cel.EnvOption{
		cel.Function("sha256", cel.Overload("sha256_dyn", []*cel.Type{cel.DynType}, cel.StringType,
			cel.UnaryBinding(func(v ref.Val) ref.Val { return types.String(sha256Hex(Text(v))) }))),
		encoder("toJson", toJSON),
		encoder("toPrettyJson", toPrettyJSON),
		encoder("toYaml", toYAML),
		cel.Function("quote", cel.Overload("quote_dyn", []*cel.Type{cel.DynType}, cel.StringType,
			cel.UnaryBinding(func(v ref.Val) ref.Val {
				if _, ok := v.(types.Null); ok {
					return types.String("")
				}
				return types.String(quote(Text(v)))
			}))),
	}
}
