// Package helmfn is the Go code of the Helm, sprig and Kubernetes rules the
// compiler reproduces, each in one place: the template functions a mapping
// calls (compile's host functions) and OKDP's generated Secrets use, and
// the names Kubernetes and Helm accept (names.go). The fixtures of
// testdata/helm hold what `helm template` prints for the same inputs,
// compared by the helm_test.go of the packages that call them. It knows
// neither CEL nor OKDP.
package helmfn

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	sigsyaml "sigs.k8s.io/yaml"
)

// Sha256Sum: Helm's sha256sum, the hex SHA-256 of the text.
func Sha256Sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// ToJSON: Helm's toJson: Go's encoding/json (keys sorted, compact, <, >, &,
// U+2028 and U+2029 escaped).
func ToJSON(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ToPrettyJSON: sprig's toPrettyJson: encoding/json indented by two spaces.
func ToPrettyJSON(v any) (string, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ToYAML: Helm's toYaml: sigs.k8s.io/yaml (keys sorted, YAML 1.1 quoting),
// without the last newline.
func ToYAML(v any) (string, error) {
	b, err := sigsyaml.Marshal(v)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(string(b), "\n"), nil
}

// Quote: sprig's quote of a text: Go's %q. sprig gives "" for a null,
// nothing rather than two quotes, which the caller tests.
func Quote(text string) string {
	return fmt.Sprintf("%q", text)
}

// Trunc: sprig's trunc, s cut to n bytes.
func Trunc(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
