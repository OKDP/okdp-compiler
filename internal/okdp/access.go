// Access: the groups of the identity provider that work in the
// namespace, each with a role (global.okdp.access.groups, which a host
// gives in its settings, Inputs.Settings), which a mapping reads in
// platform.access with the platform's administrators
// (global.okdp.access.admins). The host decides who gives them.

package okdp

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"

	ruleset "github.com/okdp/okdp-compiler/internal/rules"
	"github.com/okdp/okdp-compiler/value"
)

// Role is the role of a group in a namespace
// (global.okdp.access.groups).
type Role string

// Each role may do what the one before it may, and more.
const (
	// RoleViewer sees the namespace's instances and their objects, except
	// Secrets.
	RoleViewer Role = ruleset.RoleViewer
	// RoleEditor operates the instances and edits the objects of the
	// namespace.
	RoleEditor Role = ruleset.RoleEditor
	// RoleAdmin also administers the namespace, its RoleBindings included.
	RoleAdmin Role = ruleset.RoleAdmin
)

// roles lists the roles, least first.
var roles = []Role{RoleViewer, RoleEditor, RoleAdmin}

// groupName matches a group name as the identity provider's tokens give
// it. It allows no "/": a Keycloak group goes by its name, not its full
// path, so that a name means one group. It allows no space or comma
// either, nor anything an Argo CD policy line or a YAML scalar would
// read differently.
var groupName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._@:-]{0,254}$`)

// IsGroup reports whether name may name a group.
func IsGroup(name string) bool { return groupName.MatchString(name) }

// ParseAccess reads v, a map of group names to roles, as the roles of the
// groups. field names v in the messages. A nil v gives no group. It
// reports every problem at once.
func ParseAccess(field string, v any) (map[string]Role, error) {
	if v == nil {
		return nil, nil
	}
	n, err := value.Normalize(v)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", field, err)
	}
	m, ok := n.(M)
	if !ok {
		return nil, fmt.Errorf("%s: the roles of groups, a map of group names to %s, got %s", field, roleList(), value.ShortAt(field, n))
	}
	out := map[string]Role{}
	var errs []error
	for _, group := range slices.Sorted(maps.Keys(m)) {
		if err := checkGroup(value.Key(field, group), group, m[group]); err != nil {
			errs = append(errs, err)
			continue
		}
		out[group] = Role(m[group].(string))
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return out, nil
}

// checkGroup checks one group and its role, a string.
func checkGroup(where, group string, role any) error {
	r, isString := role.(string)
	switch {
	case !IsGroup(group):
		return fmt.Errorf("%s: a group name, a letter or a digit then letters, digits, \".\", \"_\", \"@\", \":\" or \"-\", 255 characters at most (no \"/\": the group's name, not its path)", where)
	case !isString || !slices.Contains(roles, Role(r)):
		return fmt.Errorf("%s: a role, %s, got %s", where, roleList(), value.ShortAt(where, role))
	}
	return nil
}

func roleList() string { return fmt.Sprintf("%s, %s or %s", RoleViewer, RoleEditor, RoleAdmin) }

// NamespaceError says that a namespace cannot be a project's: the
// compiler or the platform (global.okdp.policy.reserved) reserves it.
// An output drops a project that holds such a namespace.
type NamespaceError struct{ Err error }

func (e *NamespaceError) Error() string { return e.Err.Error() }
func (e *NamespaceError) Unwrap() error { return e.Err }

// SettingsOf returns the settings (global.okdp) of in, validated and
// with their defaults applied, as a compile reads them. It returns a
// *NamespaceError when the namespace of in is refused, and any other
// error when the settings are invalid.
func SettingsOf(ctx context.Context, in Inputs) (M, error) {
	if err := checkNamespace(ctx, in.rules(), "Inputs.Namespace", in.Namespace); err != nil {
		return nil, &NamespaceError{err}
	}
	l, err := valuesLayers(in)
	if err != nil {
		return nil, err
	}
	settings, err := platformSettings(in.rules(), l.platform)
	if err != nil {
		return nil, err
	}
	if err := checkPlatformNamespace(ctx, in.rules(), "Inputs.Namespace", in.Namespace, settings); err != nil {
		return nil, &NamespaceError{err}
	}
	return settings, nil
}

// SecretStoreOf returns the platform's secret store in settings, or nil
// without one.
func SecretStoreOf(ctx context.Context, in Inputs, settings M) (*SecretStore, error) {
	v, err := storeOf(ctx, in.rules(), settings)
	return secretStore(v), err
}
