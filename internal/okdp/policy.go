// Policy: the hooks of the rule sets, which the compiler calls for every
// project, on its namespace and release (CheckNamespace, checkRelease),
// on each object of an instance (CheckObjects) and on the final values of
// each chart (CheckFinalValues). What they refuse is the sets' to say:
// the compiler knows no privileged project, and the mandatory set alone
// refuses no object. What the instances give upstream charts is for a
// host to check besides (Inputs.Finalize).

package okdp

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/okdp/okdp-compiler/chart"
	ruleset "github.com/okdp/okdp-compiler/internal/rules"
	"github.com/okdp/okdp-compiler/value"
)

// checkNamespaceName returns nil when namespace may name a namespace: a
// DNS label. field names it in the error.
func checkNamespaceName(field, namespace string) error {
	if !chart.IsDNSLabel(namespace) {
		return fmt.Errorf("%s %q: a namespace name, lowercase letters, digits and \"-\", starting and ending with a letter or a digit, 63 characters at most", field, namespace)
	}
	return nil
}

// CheckNamespace returns nil when namespace may be a project's: a name
// (checkNamespaceName) that the rule sets' namespace hook passes on with
// settings, the platform's (global.okdp). A compile checks the namespace
// of its instance the same way, once it has read the settings. A
// namespace that a target writes in because of its options is for the
// target to refuse.
func CheckNamespace(ctx context.Context, rules *RuleSet, field, namespace string, settings M) error {
	if err := checkNamespaceName(field, namespace); err != nil {
		return err
	}
	return rulesOr(rules).RunHook(ctx, ruleset.HookNamespace, M{"field": field, "ns": namespace, "settings": settingsOr(settings)})
}

// NamespaceError says that a namespace cannot be a project's: it is no
// name, or the rule sets' namespace hook refuses it. An output drops a
// project that holds such a namespace.
type NamespaceError struct{ Err error }

func (e *NamespaceError) Error() string { return e.Err.Error() }
func (e *NamespaceError) Unwrap() error { return e.Err }

// SettingsOf returns the settings (global.okdp) of in, those of its
// values layers with its host's (Inputs.Settings), validated and with
// their defaults applied, as a compile reads them. It returns a
// *NamespaceError when the namespace of in is refused, and any other
// error when the settings are invalid.
func SettingsOf(ctx context.Context, in Inputs) (M, error) {
	if err := checkNamespaceName("Inputs.Namespace", in.Namespace); err != nil {
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
	if err := CheckNamespace(ctx, in.rules(), "Inputs.Namespace", in.Namespace, settings); err != nil {
		return nil, &NamespaceError{err}
	}
	return settings, nil
}

// apiGroup returns the group of an apiVersion, "" for the core group.
func apiGroup(apiVersion string) string {
	group, _, ok := strings.Cut(apiVersion, "/")
	if !ok {
		return ""
	}
	return group
}

// CheckObjects runs the rule sets' objects hook on each object of an
// instance of svc in namespace, of any project. objects is
// Result.Objects, from every source; the items of a List are checked too,
// because Helm installs them one by one. settings are the platform's,
// allowPlainSecrets Inputs.AllowPlainSecrets: the hook's documents read
// both. The first message refuses, as the document writes it.
func CheckObjects(ctx context.Context, rules *RuleSet, svc, namespace string, settings M, allowPlainSecrets bool, objects []M) error {
	if !rules.Loaded() {
		return fmt.Errorf("the rule set: %w", ruleset.ErrNotLoaded)
	}
	settings = settingsOr(settings)
	return eachObject(objects, func(o M) error {
		return rules.RunHook(ctx, ruleset.HookObjects, M{"svc": svc, "ns": namespace, "object": objectInput(o), "settings": settings, "allowPlainSecrets": allowPlainSecrets})
	})
}

// CheckFinalValues runs the rule sets' finalValues hook on the final
// values of each chart key of an instance of svc in namespace, of any
// project, keys in order. settings are the platform's. The first message
// refuses, as the hook's document writes it.
func CheckFinalValues(ctx context.Context, rules *RuleSet, svc, namespace string, settings M, values map[string]map[string]any) error {
	if !rules.Loaded() {
		return fmt.Errorf("the rule set: %w", ruleset.ErrNotLoaded)
	}
	for _, key := range slices.Sorted(maps.Keys(values)) {
		v := values[key]
		if v == nil {
			v = M{}
		}
		if err := rules.RunHook(ctx, ruleset.HookFinalValues, M{"svc": svc, "ns": namespace, "key": key, "value": v, "settings": settingsOr(settings)}); err != nil {
			return err
		}
	}
	return nil
}

// settingsOr returns settings, or an empty map for none: a hook reads the
// settings of any platform.
func settingsOr(settings M) M {
	if settings == nil {
		return M{}
	}
	return settings
}

// checkDuplicateObjects checks that no two objects of an instance have
// the same API group, kind, namespace and name, in any project. The
// second would replace the first in the cluster. That happens when a
// provider derives the same name for the objects of two claims.
//
// The versions of a group count as one object. An object without a name
// (generateName) is not compared. A Secret is left to
// refuseSecretTargets, which compares the Secrets of all the instances
// of the namespace together.
func checkDuplicateObjects(svc, namespace string, objects []M) error {
	seen := map[string]int{}
	n := 0
	return eachObject(objects, func(o M) error {
		n++
		meta := value.AsMap(o["metadata"])
		name := value.Str(meta["name"])
		if name == "" {
			return nil
		}
		ns := namespace
		if v, set := meta["namespace"]; set && v != nil && v != "" {
			ns = value.Str(v)
		}
		kind := value.Str(o["kind"])
		if kind == "Secret" && apiGroup(value.Str(o["apiVersion"])) == "" {
			return nil
		}
		id := apiGroup(value.Str(o["apiVersion"])) + "/" + kind + "/" + ns + "/" + name
		if first, dup := seen[id]; dup {
			return fmt.Errorf("%s: objects: %s/%s: two objects of one kind (%s), namespace (%s) and name: object %d and object %d; the second would replace the first. A name derived from a claim's ID must be one per ID (okdpSqlName, okdpObjectName)", svc, kind, name, apiGroupName(value.Str(o["apiVersion"])), ns, first, n)
		}
		seen[id] = n
		return nil
	})
}

// apiGroupName is apiGroup for messages: "core" for the core group.
func apiGroupName(apiVersion string) string {
	if g := apiGroup(apiVersion); g != "" {
		return g
	}
	return "core"
}

// eachObject calls f on each object, then on the items of the list the
// object holds, at any depth, and stops at the first error. Every rule
// on an instance's objects walks them with it, so that an object wrapped
// in a List escapes none of them.
func eachObject(objects []M, f func(o M) error) error {
	for _, o := range objects {
		if err := f(o); err != nil {
			return err
		}
		var items []M
		for _, i := range value.AsList(o["items"]) {
			if m, ok := i.(M); ok {
				items = append(items, m)
			}
		}
		if err := eachObject(items, f); err != nil {
			return err
		}
	}
	return nil
}
