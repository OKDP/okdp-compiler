// Functions: the host functions a mapping calls besides CEL's own, each
// deterministic and hermetic, and each the Go code of the Helm template
// function it replaces (testdata/helm/functions holds what `helm template`
// prints for the same inputs).
package compiler

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

// fullname: okdp.fullname, "<release>-<suffix>" (the release alone without
// a suffix) cut to 63 characters, without a trailing "-".
func fullname(release, suffix string) string {
	s := release
	if suffix != "" {
		s = release + "-" + suffix
	}
	return strings.TrimSuffix(trunc(s, 63), "-")
}

// text: a CEL value as text, as Helm's sprig functions take it (str; bytes
// as they are).
func text(v ref.Val) string {
	if b, ok := v.(types.Bytes); ok {
		return string(b)
	}
	return str(native(v))
}

// encoder: a CEL function of one value returning the text f computes; f's
// error fails the expression (Helm would print "").
func encoder(name string, f func(any) (string, error)) cel.EnvOption {
	return cel.Function(name, cel.Overload(name+"_dyn", []*cel.Type{cel.DynType}, cel.StringType,
		cel.UnaryBinding(func(v ref.Val) ref.Val {
			s, err := f(native(v))
			if err != nil {
				return types.NewErr("%s: %s", name, err.Error())
			}
			return types.String(s)
		})))
}

// hostFunctions: sha256, toJson, toPrettyJson, toYaml, quote and
// okdpFullname, for a mapping of release release.
func hostFunctions(release string) []cel.EnvOption {
	return []cel.EnvOption{
		cel.Function("sha256", cel.Overload("sha256_dyn", []*cel.Type{cel.DynType}, cel.StringType,
			cel.UnaryBinding(func(v ref.Val) ref.Val { return types.String(sha256Hex(text(v))) }))),
		encoder("toJson", toJSON),
		encoder("toPrettyJson", toPrettyJSON),
		encoder("toYaml", toYAML),
		cel.Function("quote", cel.Overload("quote_dyn", []*cel.Type{cel.DynType}, cel.StringType,
			cel.UnaryBinding(func(v ref.Val) ref.Val {
				if _, ok := v.(types.Null); ok {
					return types.String("")
				}
				return types.String(quote(text(v)))
			}))),
		// okdpFullname, not fullname: mappings bind their own `fullname`.
		cel.Function("okdpFullname",
			cel.Overload("okdpFullname_void", []*cel.Type{}, cel.StringType,
				cel.FunctionBinding(func(...ref.Val) ref.Val { return types.String(fullname(release, "")) })),
			cel.Overload("okdpFullname_dyn", []*cel.Type{cel.DynType}, cel.StringType,
				cel.UnaryBinding(func(v ref.Val) ref.Val { return types.String(fullname(release, text(v))) }))),
	}
}
