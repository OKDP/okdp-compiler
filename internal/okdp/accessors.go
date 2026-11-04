// Accessors: what the compile decides, for a caller that answers before
// a compile: the errors of a compile at a field of an instance's values,
// tagged with it (FieldError), for a caller that shows each beside the
// field of a form (compiler.FieldErrors); the names of the claims.

package okdp

import (
	"fmt"
	"strings"

	"github.com/okdp/okdp-compiler/internal/chart"
)

// FieldError: an error of a compile about one field of the instance's
// values, its parameter path as the messages name it (value.Key,
// value.Index: catalogs[1].storage, claims.analytics): a connection
// reference that does not resolve, a claim that does not bind, a named
// claim of the context. Its message is the error's, the path at its
// head: the tag changes no message.
type FieldError struct {
	Field string
	Err   error
}

func (e *FieldError) Error() string { return e.Err.Error() }
func (e *FieldError) Unwrap() error { return e.Err }

// atField: err tagged with field, nil for nil.
func atField(field string, err error) error {
	if err == nil {
		return nil
	}
	return &FieldError{Field: field, Err: err}
}

// NamedClaimID: the ID of the named claim name of namespace, the name of
// the output its provider publishes for it, <namespace>.<name> (claimID).
func NamedClaimID(namespace, name string) string { return claimID(namespace, "", "", name) }

// IsClaimID: name is of the form of a claim's ID, two DNS-1123 labels or
// more joined by dots, 63 characters at most (claimID), the name of the
// output a provider publishes for a claim; an output the mapping names
// after the instance, a label, is not.
func IsClaimID(name string) bool { return strings.Contains(name, ".") && chart.IsClaimID(name) }

// CheckProvider: nil when s may name a provider, a claim's or a class's:
// <instance>, <release>, <namespace>/<instance> or <namespace>/<release>
// (providerName); else the error parseClaim gives, at where.
func CheckProvider(where, s string) error {
	if _, _, ok := providerName(s, ""); !ok {
		return fmt.Errorf("%s: provider %q: %s", where, s, providerForms)
	}
	return nil
}
