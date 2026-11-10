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
	"maps"
	"regexp"
	"slices"
	"strings"

	sigsyaml "sigs.k8s.io/yaml"

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
func EngineNamespaces() []string { return defaultRules().list(tableEngineNamespaces) }

// reservedNamespace: why namespace is no project's, those the compiler
// reserves whatever the platform (the rule set's namespace check); those of
// global.okdp.policy.reserved are the platform's (Compile). "" when it may
// be one. A check that cannot be evaluated reserves it: it says so.
func reservedNamespace(rules *RuleSet, namespace string) string {
	why, err := rules.problem(checkNamespaceRule, M{"ns": namespace})
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
	return rules.refuse(checkPlatformNSRule, M{"field": field, "ns": namespace, "settings": okdp})
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
		return rules.refuse(checkObjectRule, M{"svc": svc, "ns": namespace, "object": objectInput(o)})
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

// upstreamRules: what the upstream values of an instance of a project
// that is not privileged may not set (upstreamCheck), for the messages its
// namespace.
type upstreamRules struct {
	rules      *RuleSet
	namespace  string
	deny       map[string]bool // the platform's keys, global.okdp.policy.upstream.deny, beside the rule set's
	registries []string        // global.okdp.policy.upstream.registries; nil: the images are not checked
}

// upstreamCheck: the platform's check of the upstream values of an
// instance of namespace (chart.UpstreamCheck), of global.okdp.policy of
// okdp: none for a privileged project, which sets any upstream value; for
// the others, no key of the rule set's deniedUpstreamKeys, no
// runAsUser 0 nor runAsNonRoot false (upstreamRules.key), and, with
// policy.upstream.registries, every image from one of them
// (upstreamRules.image). The package's values are not checked: they are
// reviewed with it.
func upstreamCheck(rules *RuleSet, okdp M, namespace string, privileged bool) chart.UpstreamCheck {
	if privileged {
		return nil
	}
	r := &upstreamRules{rules: rules, namespace: namespace, deny: rules.added(tableDeniedUpstreamKeys, okdp)}
	if v := value.Dig(okdp, "policy.upstream.registries"); v != nil {
		r.registries = []string{}
		for _, x := range value.AsList(v) {
			r.registries = append(r.registries, strings.TrimSuffix(value.Str(x), "/"))
		}
	}
	return func(user, values M, at func(string) string) error {
		return r.check(user, values, "", "", at)
	}
}

// yamlDocuments: the separators of the documents of a YAML text.
var yamlDocuments = regexp.MustCompile(`(?m)^---`)

// check: v, the instance's upstream values at the path p (at names it),
// the value of the key k of its parent map ("" at the top and under a
// list), over base, the computed values there (nil under a list, or in a
// string read as YAML: the instance's replace them whole). A map's images
// first (image), then its keys in order, a list's items in order: the
// first refused, in path order, fails. A string that holds a ":" and reads
// as YAML values, a map or a list, each of its documents, is checked as
// values too: charts render some string values as YAML (tpl, Bitnami's
// tplvalues.render), the volumes or containers of a list written as text.
func (r *upstreamRules) check(v, base any, k, p string, at func(string) string) error {
	switch t := v.(type) {
	case M:
		b, _ := base.(M)
		if err := r.image(t, b, k, p, at); err != nil {
			return err
		}
		for _, key := range slices.Sorted(maps.Keys(t)) {
			kp := value.Key(p, key)
			if err := r.key(key, t[key], kp, at); err != nil {
				return err
			}
			if err := r.check(t[key], b[key], key, kp, at); err != nil {
				return err
			}
		}
	case []any:
		for i, x := range t {
			if err := r.check(x, nil, "", value.Index(p, i), at); err != nil {
				return err
			}
		}
	case string:
		if !strings.Contains(t, ":") {
			return nil
		}
		text := func(q string) string { return at(p) + ", read as YAML: " + q }
		for _, doc := range yamlDocuments.Split(t, -1) {
			var y any
			if sigsyaml.Unmarshal([]byte(doc), &y) != nil {
				continue
			}
			switch y.(type) {
			case M, []any:
				if err := r.check(y, nil, "", "", text); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// key: the error of the key k of the instance's upstream values, at the
// path p, set to v: a key of the rule set's table or of the platform's deny list, to
// any value; runAsUser 0, root; runAsNonRoot false.
func (r *upstreamRules) key(k string, v any, p string, at func(string) string) error {
	return r.rules.refuse(checkUpstreamKey, M{"key": k, "value": v, "at": at(p), "ns": r.namespace, "added": r.deny})
}

// image: the error of the images the instance sets in the map m of its
// upstream values, at the path p, the value of the key k, over base, the
// computed values there; none without registries. An image string is a
// reference; a registry or a repository string (an image map's name,
// Zero to JupyterHub's, where there is no repository), or a null, the
// instance sets makes one with the other, its own or the computed one:
// <registry>/<repository>, the repository alone without registry. A null
// leaves the chart's default, which the compile does not read: "", as an
// image null. Each reference starts with an entry of registries, then
// ends, or goes on with "/" or, after an entry with a path, ":" or "@" (a
// tag, a digest). The keys whose name says they hold an image, a registry
// or a repository of images (namesImage) are references as well.
func (r *upstreamRules) image(m, base M, k, p string, at func(string) string) error {
	if r.registries == nil {
		return nil
	}
	// A key that names an image or a registry by its name, whatever the
	// chart calls it: initImage, imageName, imageRegistry,
	// defaultImageRegistry, sidecarImageRepository; its string, when not
	// empty, is a reference as the image's.
	for _, key := range slices.Sorted(maps.Keys(m)) {
		if ref, ok := m[key].(string); ok && ref != "" && r.namesImage(key) && !r.allowed(ref) {
			return r.refused(ref, value.Key(p, key), at)
		}
	}
	// The instance's string, or its null as "", set; else the computed
	// string. Another value is no image's (Harbor's registry section).
	str := func(key string) (string, bool) {
		if v, ok := m[key]; ok && (v == nil || isString(v)) {
			return value.Str(v), true
		}
		s, _ := base[key].(string)
		return s, false
	}
	if ref, set := str("image"); set && !r.allowed(ref) {
		return r.refused(ref, value.Key(p, "image"), at)
	}
	repo := "repository"
	if _, ok := m[repo]; !ok && base[repo] == nil && k == "image" {
		repo = "name"
	}
	registry, regSet := str("registry")
	repository, repoSet := str(repo)
	if !regSet && !repoSet {
		return nil
	}
	ref := repository
	if registry != "" {
		ref = registry + "/" + repository
	}
	if r.allowed(ref) {
		return nil
	}
	if regSet {
		return r.refused(ref, value.Key(p, "registry"), at)
	}
	return r.refused(ref, value.Key(p, repo), at)
}

// namesImage: whether the key k names an image, a registry or a
// repository of images by its own name, as the charts spell them: it ends
// with one of the rule set's imageKeySuffixes, whatever its case, but image
// itself, which image reads with its sibling keys.
func (r *upstreamRules) namesImage(k string) bool {
	l := strings.ToLower(k)
	return l != "image" && slices.ContainsFunc(r.rules.list(tableImageKeySuffixes), func(suffix string) bool { return strings.HasSuffix(l, suffix) })
}

// isString: whether v is a string.
func isString(v any) bool {
	_, ok := v.(string)
	return ok
}

// allowed: whether the image reference ref starts with an entry of
// registries (image).
func (r *upstreamRules) allowed(ref string) bool {
	return slices.ContainsFunc(r.registries, func(e string) bool {
		rest, ok := strings.CutPrefix(ref, e)
		return e != "" && ok && (rest == "" || rest[0] == '/' || strings.Contains(e, "/") && (rest[0] == ':' || rest[0] == '@'))
	})
}

// refused: the error of the image reference ref, at the path p, from none
// of the registries.
func (r *upstreamRules) refused(ref, p string, at func(string) string) error {
	image, registries := "image "+ref, strings.Join(r.registries, ", ")
	if ref == "" {
		image = "image null, the chart's default,"
	}
	if registries == "" {
		registries = "none"
	}
	return fmt.Errorf("%s: %s is not from a registry of global.okdp.policy.upstream.registries (%s): the platform's projects pull from any (global.okdp.policy.privileged), %s is not one of them", at(p), image, registries, r.namespace)
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
		return rules.refuse(checkPlainSecret, M{"svc": svc, "object": objectInput(o)})
	})
}

// credentialLiteral: v is written as it is, a credential when its key is
// sensitive: a string that is not empty and holds no template ({{ }},
// Helm's or External Secrets', nor ${ }, a reference of an environment
// variable), or a number. A boolean, null, a map or a list is not one.
func credentialLiteral(v any) bool {
	switch v := v.(type) {
	case string:
		return strings.TrimSpace(v) != "" && !strings.Contains(v, "{{") && !strings.Contains(v, "${")
	case int, int64, uint64, float64:
		return true
	}
	return false
}

// credentialReference: key is sensitive (value.Sensitive) by its name
// only, and names a credential rather than holds one: an existing
// Secret (existingSecret, existingSecretPasswordKey), a reference
// (passwordSecretRef, tokenRef), or the key of a Secret that holds one
// (adminPasswordKey, tokenKey; not secretKey, accessKey, which hold one).
func credentialReference(key string) bool {
	lower := strings.ToLower(strings.TrimSuffix(key, "s"))
	if strings.HasPrefix(lower, "existing") || strings.HasSuffix(lower, "ref") {
		return true
	}
	for _, sep := range []string{"key", "_key", "-key"} {
		stem, ok := strings.CutSuffix(lower, sep)
		if !ok || lower == "secretkey" || lower == "secret_key" || lower == "secret-key" {
			continue
		}
		for _, s := range []string{"password", "passwd", "token", "secret"} {
			if strings.HasSuffix(stem, s) {
				return true
			}
		}
	}
	return false
}

// plainUpstream: a warning per literal (credentialLiteral) of the
// instance's upstream values, upstream (upstream.<key>...), at a
// sensitive key (value.SensitivePath, but credentialReference): most
// likely a credential, which the instance's values file and the output
// hold in clear. A warning, not a refusal: the key's name is a guess. In
// the order of the paths; the value is never printed.
func plainUpstream(upstream M) []string {
	var out []string
	var walk func(p, key string, v any)
	walk = func(p, key string, v any) {
		switch v := v.(type) {
		case M:
			for _, k := range slices.Sorted(maps.Keys(v)) {
				walk(value.Key(p, k), k, v[k])
			}
		case []any:
			for i, x := range v {
				walk(value.Index(p, i), key, x)
			}
		default:
			if key != "" && value.Sensitive(key) && !credentialReference(key) && credentialLiteral(v) {
				out = append(out, fmt.Sprintf("%s: a literal at a sensitive key, a credential in clear in the values and the deployments repository: generate it (okdpGeneratedSecret) or read it from the platform's secret store, and give the chart the Secret's name (its existingSecret)", p))
			}
		}
	}
	walk("upstream", "", upstream)
	return out
}
