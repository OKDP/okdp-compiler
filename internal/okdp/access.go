// Access: who a project's people are and what they may do, decided by the
// platform and the project without a control plane (docs/specs/
// 01_inputs_outputs.md, access): project.yaml's access, the roles of the
// identity provider's groups within the project, the default group
// okdp-project-<namespace> an editor; global.okdp.access, the platform's
// administrators and the prefix the Kubernetes API server gives the
// groups; global.okdp.controlPlane, the identity of the optional control
// plane. The targets write them into each project's manifests: Argo CD
// roles on its AppProject, RoleBindings in its namespace.

package okdp

import (
	"errors"
	"fmt"
	ruleset "github.com/okdp/okdp-compiler/internal/rules"
	"maps"
	"regexp"
	"slices"

	"github.com/okdp/okdp-compiler/value"
)

// Role: the role of a group within a project (project.yaml access).
type Role string

// The roles, each what the one before it may do, and more.
const (
	// RoleViewer: sees the project: its Argo CD Applications, its
	// namespace's objects but its Secrets (Kubernetes' view).
	RoleViewer Role = ruleset.RoleViewer
	// RoleEditor: operates the project: syncs its Applications, runs their
	// actions, reads their logs; edits its namespace's objects
	// (Kubernetes' edit).
	RoleEditor Role = ruleset.RoleEditor
	// RoleAdmin: an editor who also deletes and overrides its
	// Applications, and administers its namespace, its RoleBindings among
	// it (Kubernetes' admin).
	RoleAdmin Role = ruleset.RoleAdmin
)

// Roles: the roles, least first.
func Roles(rules *RuleSet) []Role {
	var out []Role
	for _, r := range rulesOr(rules).List(ruleset.TableProjectRoles) {
		out = append(out, Role(r))
	}
	return out
}

// AccessKey: the key of a project's file, the context at level 1, that
// gives the roles of its groups (Access.Groups): no instance receives it.
const AccessKey = "access"

// DefaultGroup: okdp-project-<namespace>, the group of a project's people
// when its file gives no access, an editor.
func DefaultGroup(namespace string) string { return "okdp-project-" + namespace }

// groupName: a group's name as the identity provider's tokens give it in
// their groups claim: a letter or a digit, then letters, digits, ".", "_",
// "@", ":" and "-", 255 characters at most. No "/": a Keycloak group by
// its name, its full path left out of the claim (the groups mapper's
// "Full group path" off), so that a name means one group; no space, no
// comma, nothing an Argo CD policy line or a YAML scalar would read
// otherwise.
var groupName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._@:-]{0,254}$`)

// IsGroup: name may name a group (groupName).
func IsGroup(name string) bool { return groupName.MatchString(name) }

// ParseAccess: the roles of the groups of a project of namespace, v the
// value of its file's access, as read: nil (absent or null) gives the
// default, DefaultGroup an editor; else a map of group names (IsGroup) to
// a role, viewer, editor or admin; {} gives no group a role. Every
// problem at once, by group in name order.
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

// roleList: the roles, for messages.
func roleList() string { return fmt.Sprintf("%s, %s or %s", RoleViewer, RoleEditor, RoleAdmin) }

// projectAccess: the roles of the groups of the project of namespace,
// the access of its context's layers at level 1, its file's, merged in
// order (ParseAccess); contextLayers refuses it at another level.
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

// admins: global.okdp.access.admins of okdp, the platform's settings: the
// groups of the platform's administrators, in their order.
func admins(okdp M) []string {
	var out []string
	for _, g := range value.AsList(value.Dig(okdp, "access.admins")) {
		out = append(out, value.Str(g))
	}
	return out
}

// NamespaceError: the namespace of a project is none a project may have
// today: the compiler reserves it, or the platform does
// (global.okdp.policy.reserved). A project that holds it is dropped from
// an output (ProjectOf, output.Files), not kept.
type NamespaceError struct{ Err error }

func (e *NamespaceError) Error() string { return e.Err.Error() }
func (e *NamespaceError) Unwrap() error { return e.Err }

// CheckProject: nil when the namespace of in may be a project's today, as
// the compile of any of its instances decides it: a DNS label, none the
// compiler reserves, none the platform does (global.okdp.policy.reserved
// of the settings in.Context's level 0 gives, checked as a compile
// checks them); a *NamespaceError otherwise, any other error the
// settings'. The one view of the platform's namespaces for Compile and
// ProjectOf.
func CheckProject(in Inputs) error {
	_, err := platformOf(in)
	return err
}

// platformOf: the platform's settings of the context of in, global.okdp,
// when the namespace of in may be a project's (CheckProject).
func platformOf(in Inputs) (M, error) {
	if err := checkNamespace(in.rules(), "Inputs.Namespace", in.Namespace); err != nil {
		return nil, &NamespaceError{err}
	}
	_, platform, err := contextLayers(in.rules(), in.Context)
	if err != nil {
		return nil, err
	}
	settings, err := platformSettings(in.rules(), platform)
	if err != nil {
		return nil, err
	}
	if err := checkPlatformNamespace(in.rules(), "Inputs.Namespace", in.Namespace, settings); err != nil {
		return nil, &NamespaceError{err}
	}
	return settings, nil
}

// PlatformAdmins: the groups of the platform's administrators,
// global.okdp.access.admins of platform, the platform's values (level
// 0), checked against the platform's schema as a compile checks them;
// none when not set. The compiler writes nothing of them: the bootstrap
// binds them once, Argo CD's role:admin and the cluster's, for a program
// that mirrors the platform's authority (a control plane).
func PlatformAdmins(rules *RuleSet, platform map[string]any) ([]string, error) {
	rules = rulesOr(rules)
	_, settings, err := contextLayers(rules, []ContextLayer{{Values: platform}})
	if err != nil {
		return nil, err
	}
	okdp, err := platformSettings(rules, settings)
	if err != nil {
		return nil, err
	}
	return admins(okdp), nil
}

// PlatformOf: the platform's settings of the context of in, global.okdp,
// when the namespace of in may be a project's, as a compile checks them.
func PlatformOf(in Inputs) (M, error) { return platformOf(in) }

// ProjectGroups: the roles of the groups of the project of in, its file's
// (the context at level 1).
func ProjectGroups(in Inputs) (map[string]Role, error) {
	return projectAccess(in.rules(), in.Context, in.Namespace)
}

// SecretStoreOf: the platform's secret store in settings, nil without one.
func SecretStoreOf(in Inputs, settings M) (*SecretStore, error) {
	return secretStore(in.rules(), settings)
}

// Privileged: whether namespace is one of the platform's own projects.
func Privileged(settings M, namespace string) bool { return privileged(settings, namespace) }
