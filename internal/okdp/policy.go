// Policy: the platform's policy, global.okdp.policy: which namespaces no
// project may have (reserved), which projects are the platform's own
// (privileged), and what the others' instances may not
// do: their objects stay in their namespace, no cluster-scoped kind
// (checkObjects); their upstream values set no key of a pod's privileges,
// identity, containers or objects, and their images come from the
// platform's registries (upstreamCheck). And for every project, no Secret
// of credentials in clear unless the caller allows it (checkPlainSecrets).

package okdp

import (
	"fmt"
	ruleset "github.com/okdp/okdp-compiler/internal/rules"
	"slices"
	"strings"

	"github.com/okdp/okdp-compiler/chart"
	"github.com/okdp/okdp-compiler/value"
)

// privileged: whether namespace is one of global.okdp.policy.privileged,
// the platform's own projects.
func privileged(okdp M, namespace string) bool {
	return slices.ContainsFunc(value.AsList(value.Dig(okdp, "policy.privileged")), func(v any) bool { return value.Str(v) == namespace })
}

// EngineNamespaces: the namespaces of the deployer engines and of the
// objects the targets write in them, which no project may have: a
// project's releases are installed with every verb of its namespace, and
// there they would rewrite the engine's sources, its Applications or
// HelmReleases, the other projects' identities: flux-system, Flux's and
// its default source's (flux.DefaultSource), okdp-releases, the flux
// target's HelmReleases and ServiceAccounts (flux.ReleasesNamespace),
// argocd, Argo CD's and the argocd target's Applications and AppProjects
// (argocd.Namespace). A target that writes in another (Flux.Source's)
// refuses its projects too.
func EngineNamespaces() []string { return defaultRules().List(ruleset.TableEngineNamespaces) }

// reservedNamespace: why namespace is no project's, those the compiler
// reserves whatever the platform (the rule set's namespace check); those of
// global.okdp.policy.reserved are the platform's (Compile). "" when it may
// be one. A check that cannot be evaluated reserves it: it says so.
func reservedNamespace(rules *RuleSet, namespace string) string {
	why, err := rules.Problem(ruleset.CheckNamespaceRule, M{"ns": namespace})
	if err != nil {
		return err.Error()
	}
	return why
}

// The rule of a project's name, its namespace, in two parts, as the
// compile applies them (Compile): the compiler's own, then the
// platform's; CheckNamespace both, for a program that creates projects.
// field names the name in the errors: Inputs.Namespace for Compile.

// checkNamespace: nil when namespace may be a project's whatever the
// platform: a DNS label (chart.IsDNSLabel), none the compiler reserves
// (reservedNamespace).
func checkNamespace(rules *RuleSet, field, namespace string) error {
	switch {
	case !chart.IsDNSLabel(namespace):
		return fmt.Errorf("%s %q: a namespace name, lowercase letters, digits and \"-\", starting and ending with a letter or a digit, 63 characters at most", field, namespace)
	case reservedNamespace(rules, namespace) != "":
		return fmt.Errorf("%s %q: the namespace is reserved: %s", field, namespace, reservedNamespace(rules, namespace))
	}
	return nil
}

// checkPlatformNamespace: nil when namespace is none of the platform's
// reserved namespaces, global.okdp.policy.reserved of okdp, the
// platform's settings: a reservation wins over a privilege.
func checkPlatformNamespace(rules *RuleSet, field, namespace string, okdp M) error {
	return rules.Refuse(ruleset.CheckPlatformNSRule, M{"field": field, "ns": namespace, "settings": okdp})
}

// CheckNamespace: nil when namespace may be a project's, okdp the
// platform's settings (global.okdp): checkNamespace, then
// checkPlatformNamespace. A namespace a target writes in by its options
// (the flux target's Source) is the target's to refuse
// (target.OwnNamespaces).
func CheckNamespace(field, namespace string, okdp M) error {
	if err := checkNamespace(defaultRules(), field, namespace); err != nil {
		return err
	}
	return checkPlatformNamespace(defaultRules(), field, namespace, okdp)
}

// apiGroup: the group of an apiVersion, "" for the core group (v1).
func apiGroup(apiVersion string) string {
	group, _, ok := strings.Cut(apiVersion, "/")
	if !ok {
		return ""
	}
	return group
}

// checkObjects: the objects of an instance of svc in namespace, one that
// is not privileged (Result.Objects, every source: the credentials
// Secrets, the objects section, the generated Secrets, the PushSecrets and
// ExternalSecrets): each in the instance's namespace, where its
// metadata.namespace is set (unset, "" or null, the release's), and none
// of a cluster-scoped kind (the rule set's clusterScopedKinds); the items of one that holds a
// list of them (items: a List, which Helm installs item by item) checked
// alike. The first refused, in order, fails, named <kind>/<name>: the
// platform's projects alone (global.okdp.policy.privileged) install
// cluster-scoped objects and write other namespaces.
func checkObjects(rules *RuleSet, svc, namespace string, objects []M) error {
	return eachObject(objects, func(o M) error {
		return rules.Refuse(ruleset.CheckObjectRule, M{"svc": svc, "ns": namespace, "object": objectInput(o)})
	})
}

// checkDuplicateObjects: the objects of an instance of svc in namespace
// (Result.Objects, every source, the items of a list alike) hold no two of
// one apiVersion group, kind, namespace (unset, "" or null, the
// instance's) and name: the second would replace the first in the cluster,
// whichever comes last in the file, as two claims' objects whose names a
// provider derived the same way do. Of every project, privileged or not;
// the versions of a group are one object. A Secret is left to
// refuseSecretTargets, which compares those of the namespace's instances
// all together. An object without a name (generateName) is none to
// compare. The second is named, <kind>/<name>,
// with the first's position.
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

// apiGroupName: the group of an apiVersion as the messages name it, "core"
// for the core group (v1).
func apiGroupName(apiVersion string) string {
	if g := apiGroup(apiVersion); g != "" {
		return g
	}
	return "core"
}

// eachObject: f on each object of objects, in order, each followed by the
// items of the list it holds, at any depth (items: a List, which Helm and
// Argo CD install item by item, an item that is not a map skipped): the
// one walk of the rules on an instance's objects (checkObjects,
// checkPlainSecrets, refuseSecretTargets, keepSynced), so that an object
// wrapped in a List escapes none. The first error of f stops it.
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

// checkPlainSecrets: the objects of an instance of svc (Result.Objects),
// unless Inputs.AllowPlainSecrets, hold no credential in clear, the items
// of one that holds a list of them (items) alike:
//
//   - no Secret (v1) holds values, a non-empty data or stringData;
//   - no ExternalSecret (external-secrets.io), nor ClusterExternalSecret
//     (spec.externalSecretSpec), has its target.template.data hold a
//     literal (credentialLiteral) at a sensitive key (value.Sensitive):
//     a template ({{ }}) reads what the store or a generator gives, a
//     literal at another key (a region, a URL) passes;
//   - no Fake generator (generators.external-secrets.io), nor
//     ClusterGenerator of kind Fake, holds values (its data).
//
// The objects file is written to the deployments repository: credentials
// are generated in the cluster (okdpGeneratedSecret, a provides secret's
// generate) or read from the platform's secret store (an
// ExternalSecret), never written in clear. A Secret with only metadata
// and type passes. The first refused, in order, fails, named
// <kind>/<name>, its field named, data before stringData, the keys of a
// template in order; the value is never printed.
func checkPlainSecrets(rules *RuleSet, svc string, objects []M) error {
	return eachObject(objects, func(o M) error {
		return rules.Refuse(ruleset.CheckPlainSecret, M{"svc": svc, "object": objectInput(o)})
	})
}
