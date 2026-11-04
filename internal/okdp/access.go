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
	"maps"
	"regexp"
	"slices"

	"github.com/okdp/okdp-compiler/internal/chart"
	"github.com/okdp/okdp-compiler/internal/value"
)

// Role: the role of a group within a project (project.yaml access).
type Role string

// The roles, each what the one before it may do, and more.
const (
	// RoleViewer: sees the project: its Argo CD Applications, its
	// namespace's objects but its Secrets (Kubernetes' view).
	RoleViewer Role = "viewer"
	// RoleEditor: operates the project: syncs its Applications, runs their
	// actions, reads their logs; edits its namespace's objects
	// (Kubernetes' edit).
	RoleEditor Role = "editor"
	// RoleAdmin: an editor who also deletes and overrides its
	// Applications, and administers its namespace, its RoleBindings among
	// it (Kubernetes' admin).
	RoleAdmin Role = "admin"
)

// Roles: the roles, least first.
func Roles() []Role { return []Role{RoleViewer, RoleEditor, RoleAdmin} }

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

// Access: the roles of a project's groups, by group name, and the prefix
// the Kubernetes API server gives the groups of its users (its
// --oidc-groups-prefix, global.okdp.access.groupPrefix): a RoleBinding's
// subject is the prefix and the name, an Argo CD role's group the name.
type Access struct {
	Groups      map[string]Role
	GroupPrefix string
}

// ParseAccess: the roles of the groups of a project of namespace, v the
// value of its file's access, as read: nil (absent or null) gives the
// default, DefaultGroup an editor; else a map of group names (IsGroup) to
// a role, viewer, editor or admin; {} gives no group a role. Every
// problem at once, by group in name order.
func ParseAccess(v any, namespace string) (map[string]Role, error) {
	if v == nil {
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
		case !isString || !slices.Contains(Roles(), Role(role)):
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
func projectAccess(layers []ContextLayer, namespace string) (map[string]Role, error) {
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
	groups, err := ParseAccess(access, namespace)
	if err != nil {
		return nil, fmt.Errorf("the project's file (level 1): %w", err)
	}
	return groups, nil
}

// ServiceAccount: a ServiceAccount, by namespace and name.
type ServiceAccount struct {
	Namespace, Name string
}

// controlPlane: global.okdp.controlPlane.serviceAccount of okdp, the
// platform's settings, the identity of the control plane; nil when not
// set: the platform has none, and the targets bind none.
func controlPlane(okdp M) *ServiceAccount {
	sa := value.AsMap(value.Dig(okdp, "controlPlane.serviceAccount"))
	if len(sa) == 0 {
		return nil
	}
	return &ServiceAccount{Namespace: value.Str(sa["namespace"]), Name: value.Str(sa["name"])}
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

// Project: what the targets write a project's manifests from, the
// platform's and the project's decisions, not its instances': its
// namespace; whether it is one of the platform's own
// (global.okdp.policy.privileged); the platform's secret store in it
// (global.okdp.secrets with a provider); the roles of its groups
// (Access); the control plane's identity (global.okdp.controlPlane).
type Project struct {
	Namespace    string
	Privileged   bool
	SecretStore  *SecretStore
	Access       Access
	ControlPlane *ServiceAccount
}

// AccessError: a project's access the compile refuses (ParseAccess):
// ProjectOf gives the rest of the project all the same, no group a role.
type AccessError struct{ Err error }

func (e *AccessError) Error() string { return e.Err.Error() }
func (e *AccessError) Unwrap() error { return e.Err }

// ProjectOf: the Project of the namespace of in, of its context alone,
// as the compile of any of its instances gives it (Result): today's
// settings of the platform and of the project, whether its instances
// compile or not, for the targets to write the manifests of a project
// whose instances are kept (output.Files). The context's layers checked
// as a compile checks them (contextLayers, platformSettings), their
// error the error. An access that is refused is an *AccessError, the
// Project given all the same, no group a role in it.
func ProjectOf(in Inputs) (Project, error) {
	if err := checkNamespace("Inputs.Namespace", in.Namespace); err != nil {
		return Project{}, err
	}
	_, platform, err := contextLayers(in.Context)
	if err != nil {
		return Project{}, err
	}
	settings, err := platformSettings(platform)
	if err != nil {
		return Project{}, err
	}
	p := Project{Namespace: in.Namespace, Privileged: privileged(settings, in.Namespace), SecretStore: secretStore(settings), Access: Access{GroupPrefix: groupPrefix(settings)}, ControlPlane: controlPlane(settings)}
	if p.Access.Groups, err = projectAccess(in.Context, in.Namespace); err != nil {
		p.Access.Groups = map[string]Role{}
		return p, &AccessError{err}
	}
	return p, nil
}

// groupPrefix: global.okdp.access.groupPrefix of okdp, "" when unset.
func groupPrefix(okdp M) string { return value.Str(value.Dig(okdp, "access.groupPrefix")) }

// PlatformAdmins: the groups of the platform's administrators,
// global.okdp.access.admins of platform, the platform's values (level
// 0), checked against the platform's schema as a compile checks them;
// none when not set. The compiler writes nothing of them: the bootstrap
// binds them once, Argo CD's role:admin and the cluster's, for a program
// that mirrors the platform's authority (a control plane).
func PlatformAdmins(platform map[string]any) ([]string, error) {
	_, settings, err := contextLayers([]ContextLayer{{Values: platform}})
	if err != nil {
		return nil, err
	}
	okdp, err := platformSettings(settings)
	if err != nil {
		return nil, err
	}
	return admins(okdp), nil
}

// ProjectAccess: the roles of the groups of the project of namespace,
// file its project.yaml as written (nil: none): its access (ParseAccess),
// the default when it gives none, for a program that mirrors the
// project's authority (a control plane).
func ProjectAccess(file []byte, namespace string) (map[string]Role, error) {
	if err := checkNamespace("project", namespace); err != nil {
		return nil, err
	}
	values, err := chart.ParseValues(file)
	if err != nil {
		return nil, err
	}
	return ParseAccess(values[AccessKey], namespace)
}
