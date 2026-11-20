// Accessors: what a compile decides, for a caller that needs the answer
// before it compiles.

package okdp

import (
	"fmt"
	"strings"

	"github.com/okdp/okdp-compiler/value"
)

// FieldError is value.FieldError: a compile error tagged with the field
// of the instance's values it is about.
type FieldError = value.FieldError

func atField(field string, err error) error { return value.AtField(field, err) }

// NamedClaimID returns the ID of the named claim name of namespace:
// <namespace>.<name>.
func NamedClaimID(namespace, name string) string { return claimID(namespace, name) }

// IsClaimID reports whether name has the form of a claim's ID: two or
// more DNS-1123 labels joined by dots. An output that the mapping names
// after the instance is a single label, so it is not one.
func IsClaimID(name string) bool { return strings.Contains(name, ".") && claimIDForm(name) }

// CheckProvider returns nil when s may name the provider of a claim or
// of a class: an instance or a release, with or without its namespace
// (<namespace>/<instance>). The error starts with where.
func CheckProvider(where, s string) error {
	if _, _, ok := providerName(s, ""); !ok {
		return fmt.Errorf("%s: provider %q: %s", where, s, providerForms)
	}
	return nil
}
