// Access: who the people of a project are and what they may do, decided
// by the platform and the project without a control plane
// (docs/specs/01_inputs_outputs.md, access). access in project.yaml
// gives the role of each identity provider group; global.okdp.access
// gives the platform's administrators. The targets write the result into
// each project's manifests: Argo CD roles and RoleBindings.

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

// Role is the role of a group within a project (access in project.yaml).
type Role string

// Each role may do what the one before it may, and more.
const (
	// RoleViewer sees the project: its Argo CD Applications and the
	// objects of its namespace, except Secrets (Kubernetes' view).
	RoleViewer Role = ruleset.RoleViewer
	// RoleEditor operates the project: it syncs its Applications and edits
	// the objects of its namespace (Kubernetes' edit).
	RoleEditor Role = ruleset.RoleEditor
	// RoleAdmin also deletes and overrides the Applications and administers
	// the namespace, RoleBindings included (Kubernetes' admin).
	RoleAdmin Role = ruleset.RoleAdmin
)

// Roles lists the roles of the rule set, least first.
func Roles(rules *RuleSet) []Role {
	var out []Role
	for _, r := range rulesOr(rules).List(ruleset.TableProjectRoles) {
		out = append(out, Role(r))
	}
	return out
}

// AccessKey is the key of a project's file (the context at level 1) that
// gives the roles of its groups. No instance receives it.
const AccessKey = "access"

// DefaultGroup returns okdp-project-<namespace>, the group that is an
// editor of a project whose file gives no access.
func DefaultGroup(namespace string) string { return "okdp-project-" + namespace }

// groupName matches a group name as the identity provider's tokens give
// it. It allows no "/": a Keycloak group goes by its name, not its full
// path, so that a name means one group. It allows no space or comma
// either, nor anything an Argo CD policy line or a YAML scalar would
// read differently.
var groupName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._@:-]{0,254}$`)

// IsGroup reports whether name may name a group.
func IsGroup(name string) bool { return groupName.MatchString(name) }

// ParseAccess reads the roles of the groups of the project of namespace
// from v, the access value of its file. A nil v gives the default
// (DefaultGroup is an editor); {} gives no group a role. It reports
// every problem at once.
func ParseAccess(rules *RuleSet, v any, namespace string) (map[string]Role, error) {
	roles := Roles(rules)
	if len(roles) == 0 { // no role to give: the rule set has no access
		return nil, nil
	}
	if v == nil {
		if !slices.Contains(roles, RoleEditor) { // never a role the rule set does not hold
			return map[string]Role{}, nil
		}
		return map[string]Role{DefaultGroup(namespace): RoleEditor}, nil
	}
	m, ok := v.(M)
	if !ok {
		return nil, fmt.Errorf("%s: the roles of the project's groups, a map of group names to %s, got %s", AccessKey, roleList(), value.ShortAt(AccessKey, v))
	}
	out := map[string]Role{}
	var errs []error
	for _, group := range slices.Sorted(maps.Keys(m)) {
		where := value.Key(AccessKey, group)
		role, isString := m[group].(string)
		switch {
		case !IsGroup(group):
			errs = append(errs, fmt.Errorf("%s: a group name, a letter or a digit then letters, digits, \".\", \"_\", \"@\", \":\" or \"-\", 255 characters at most (no \"/\": the group's name, not its path)", where))
		case !isString || !slices.Contains(roles, Role(role)):
			errs = append(errs, fmt.Errorf("%s: a role, %s, got %s", where, roleList(), value.ShortAt(where, m[group])))
		default:
			out[group] = Role(role)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return out, nil
}

func roleList() string { return fmt.Sprintf("%s, %s or %s", RoleViewer, RoleEditor, RoleAdmin) }

// projectAccess reads access from the context layers at level 1, the
// project's file, merged in order. contextLayers refuses it at another
// level.
func projectAccess(rules *RuleSet, layers []ContextLayer, namespace string) (map[string]Role, error) {
	var access any
	for i, l := range layers {
		v, set := l.Values[AccessKey]
		if !set || l.Level != 1 {
			continue
		}
		n, err := value.Normalize(v)
		if err != nil {
			return nil, fmt.Errorf("context layer %d (level %d): %s: %w", i+1, l.Level, AccessKey, err)
		}
		a, aMap := access.(M)
		b, bMap := n.(M)
		if aMap && bMap {
			access = value.Merge(a, b)
		} else {
			access = value.Copy(n)
		}
	}
	groups, err := ParseAccess(rules, access, namespace)
	if err != nil {
		return nil, fmt.Errorf("the project's file (level 1): %w", err)
	}
	return groups, nil
}

// admins returns global.okdp.access.admins of okdp, the platform's
// settings.
func admins(okdp M) []string {
	var out []string
	for _, g := range value.AsList(value.Dig(okdp, "access.admins")) {
		out = append(out, value.Str(g))
	}
	return out
}

// NamespaceError says that a namespace cannot be a project's: the
// compiler or the platform (global.okdp.policy.reserved) reserves it.
// An output drops a project that holds such a namespace.
type NamespaceError struct{ Err error }

func (e *NamespaceError) Error() string { return e.Err.Error() }
func (e *NamespaceError) Unwrap() error { return e.Err }

// CheckProject returns nil when the namespace of in may be a project's,
// exactly as a compile decides it. It returns a *NamespaceError when the
// namespace is refused, and any other error when the platform's settings
// are invalid.
func CheckProject(ctx context.Context, in Inputs) error {
	_, err := platformOf(ctx, in)
	return err
}

// platformOf returns the platform's settings (global.okdp) of in, once
// its namespace passes CheckProject.
func platformOf(ctx context.Context, in Inputs) (M, error) {
	if err := checkNamespace(ctx, in.rules(), "Inputs.Namespace", in.Namespace); err != nil {
		return nil, &NamespaceError{err}
	}
	_, platform, err := contextLayers(ctx, in.rules(), in.Context)
	if err != nil {
		return nil, err
	}
	settings, err := platformSettings(in.rules(), platform)
	if err != nil {
		return nil, err
	}
	if err := checkPlatformNamespace(ctx, in.rules(), "Inputs.Namespace", in.Namespace, settings); err != nil {
		return nil, &NamespaceError{err}
	}
	return settings, nil
}

// PlatformAdmins returns global.okdp.access.admins of platform, the
// platform's values (level 0), checked as a compile checks them. The
// compiler writes nothing for these groups: the bootstrap binds them.
func PlatformAdmins(ctx context.Context, rules *RuleSet, platform map[string]any) ([]string, error) {
	rules = rulesOr(rules)
	_, settings, err := contextLayers(ctx, rules, []ContextLayer{{Values: platform}})
	if err != nil {
		return nil, err
	}
	okdp, err := platformSettings(rules, settings)
	if err != nil {
		return nil, err
	}
	return admins(okdp), nil
}

// PlatformOf returns the platform's settings (global.okdp) of in. It
// fails when the namespace of in may not be a project's.
func PlatformOf(ctx context.Context, in Inputs) (M, error) { return platformOf(ctx, in) }

// ProjectGroups returns the roles of the groups of the project of in.
func ProjectGroups(in Inputs) (map[string]Role, error) {
	return projectAccess(in.rules(), in.Context, in.Namespace)
}

// SecretStoreOf returns the platform's secret store in settings, or nil
// without one.
func SecretStoreOf(ctx context.Context, in Inputs, settings M) (*SecretStore, error) {
	return secretStore(ctx, in.rules(), settings)
}

// Privileged reports whether namespace is a project of the platform's own.
func Privileged(settings M, namespace string) bool { return privileged(settings, namespace) }
