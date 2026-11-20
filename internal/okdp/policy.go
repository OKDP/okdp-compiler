// Policy: the platform's policy, global.okdp.policy. It says which
// namespaces no project may have (reserved) and which projects are the
// platform's own (privileged). The instances of the other projects keep
// their objects in their namespace and install no cluster-scoped kind
// (CheckObjects). What they give upstream charts is for a host to check
// (Inputs.Finalize, Inputs.CheckGlobals). And no project, privileged or
// not, writes a Secret of credentials in clear unless the caller allows
// it (checkPlainSecrets).

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

// Privileged reports whether namespace is one of the platform's own
// projects, global.okdp.policy.privileged.
func Privileged(okdp M, namespace string) bool {
	return slices.ContainsFunc(value.AsList(value.Dig(okdp, "policy.privileged")), func(v any) bool { return value.Str(v) == namespace })
}

// EngineNamespaces lists the namespaces no project may have, because the
// deployer engines and the objects the targets write for them live
// there: flux-system, okdp-releases and argocd.
//
// A project's releases are installed with every verb of its namespace.
// In one of these namespaces they would rewrite the engine's sources,
// its Applications or HelmReleases, and the identities of the other
// projects.
func EngineNamespaces(rules *RuleSet) []string {
	return rulesOr(rules).List(ruleset.TableEngineNamespaces)
}

// The rule on a project's namespace has two parts: the compiler's own,
// then the platform's. field names the namespace in the errors.

// checkNamespace returns nil when namespace may be a project's on any
// platform: it is a DNS label that the compiler does not reserve. A
// check that cannot be evaluated reserves the namespace, and its error
// says so.
func checkNamespace(ctx context.Context, rules *RuleSet, field, namespace string) error {
	if !chart.IsDNSLabel(namespace) {
		return fmt.Errorf("%s %q: a namespace name, lowercase letters, digits and \"-\", starting and ending with a letter or a digit, 63 characters at most", field, namespace)
	}
	if err := rules.Refuse(ctx, ruleset.CheckNamespaceRule, M{"ns": namespace}); err != nil {
		return fmt.Errorf("%s %q: the namespace is reserved: %w", field, namespace, err)
	}
	return nil
}

// checkPlatformNamespace returns nil when the platform does not reserve
// namespace (global.okdp.policy.reserved). A reservation wins over a
// privilege.
func checkPlatformNamespace(ctx context.Context, rules *RuleSet, field, namespace string, okdp M) error {
	return rules.Refuse(ctx, ruleset.CheckPlatformNSRule, M{"field": field, "ns": namespace, "settings": okdp})
}

// CheckNamespace applies both parts, for a program that creates
// projects. okdp is the platform's settings (global.okdp). A namespace
// that a target writes in because of its options is for the target to
// refuse (target.OwnNamespaces).
func CheckNamespace(ctx context.Context, rules *RuleSet, field, namespace string, okdp M) error {
	rules = rulesOr(rules)
	if err := checkNamespace(ctx, rules, field, namespace); err != nil {
		return err
	}
	return checkPlatformNamespace(ctx, rules, field, namespace, okdp)
}

// apiGroup returns the group of an apiVersion, "" for the core group.
func apiGroup(apiVersion string) string {
	group, _, ok := strings.Cut(apiVersion, "/")
	if !ok {
		return ""
	}
	return group
}

// CheckObjects checks the objects of an instance of svc that is not
// privileged. objects is Result.Objects, from every source. Each must
// be in the instance's namespace when it sets metadata.namespace, and of
// no cluster-scoped kind: only the platform's projects install
// cluster-scoped objects and write in other namespaces. The items of a
// List are checked too, because Helm installs them one by one. An object
// that passes then goes through the rule set's objects hook, which reads
// settings, the platform's.
func CheckObjects(ctx context.Context, rules *RuleSet, svc, namespace string, settings M, objects []M) error {
	return eachObject(objects, func(o M) error {
		in := objectInput(o)
		if err := rules.Refuse(ctx, ruleset.CheckObjectRule, M{"svc": svc, "ns": namespace, "object": in}); err != nil {
			return err
		}
		return rules.RunHook(ctx, ruleset.HookObjects, M{"svc": svc, "ns": namespace, "object": in, "settings": settingsOr(settings)})
	})
}

// CheckFinalValues runs the rule set's finalValues hook on the final
// values of each chart key of an instance of svc that is not privileged,
// keys in order. settings are the platform's. The first message refuses,
// as the hook's document writes it.
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

// checkPlainSecrets checks that the objects of an instance hold no
// credential in clear, unless Inputs.AllowPlainSecrets is set. It
// refuses a Secret that holds values, an ExternalSecret whose template
// holds a literal at a sensitive key, and a Fake generator that holds
// values. A template ({{ }}) and a literal at another key (a region, a
// URL) pass, and so does a Secret with only metadata and type.
//
// The reason is that the objects file is written to the deployments
// repository. Credentials are generated in the cluster or read from the
// platform's secret store, never written in clear. The error never
// prints the value.
func checkPlainSecrets(ctx context.Context, rules *RuleSet, svc string, objects []M) error {
	return eachObject(objects, func(o M) error {
		return rules.Refuse(ctx, ruleset.CheckPlainSecret, M{"svc": svc, "object": objectInput(o)})
	})
}
