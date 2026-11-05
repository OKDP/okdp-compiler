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

	"github.com/okdp/okdp-compiler/internal/chart"
	"github.com/okdp/okdp-compiler/internal/value"
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
func EngineNamespaces() []string { return []string{"argocd", "flux-system", "okdp-releases"} }

// reservedNamespace: why namespace is no project's, those the compiler
// reserves whatever the platform: platform, the segment of the platform's
// keys of the store; kube-*, Kubernetes' own (kube-system, kube-public,
// kube-node-lease); EngineNamespaces. Not default: a project may be
// named so (the sandbox's confined demo). "" when it may be one; those of
// global.okdp.policy.reserved are the platform's (Compile).
func reservedNamespace(namespace string) string {
	switch {
	case namespace == platformKeys:
		return fmt.Sprintf("%s/ holds the platform's keys of the secret store, which no project writes", platformKeys)
	case strings.HasPrefix(namespace, "kube-"):
		return "kube-* is Kubernetes' own (kube-system, kube-public, kube-node-lease)"
	case slices.Contains(EngineNamespaces(), namespace):
		return fmt.Sprintf("a deployer engine's, or the namespace its target writes in (%s): a project's releases there would drive the engine", strings.Join(EngineNamespaces(), ", "))
	}
	return ""
}

// reservedByPlatform: whether namespace is one of
// global.okdp.policy.reserved, the namespaces the platform keeps from
// every project.
func reservedByPlatform(okdp M, namespace string) bool {
	return slices.ContainsFunc(value.AsList(value.Dig(okdp, "policy.reserved")), func(v any) bool { return value.Str(v) == namespace })
}

// The rule of a project's name, its namespace, in two parts, as the
// compile applies them (Compile): the compiler's own, then the
// platform's; CheckNamespace both, for a program that creates projects.
// field names the name in the errors: Inputs.Namespace for Compile.

// checkNamespace: nil when namespace may be a project's whatever the
// platform: a DNS label (chart.IsDNSLabel), none the compiler reserves
// (reservedNamespace).
func checkNamespace(field, namespace string) error {
	switch {
	case !chart.IsDNSLabel(namespace):
		return fmt.Errorf("%s %q: a namespace name, lowercase letters, digits and \"-\", starting and ending with a letter or a digit, 63 characters at most", field, namespace)
	case reservedNamespace(namespace) != "":
		return fmt.Errorf("%s %q: the namespace is reserved: %s", field, namespace, reservedNamespace(namespace))
	}
	return nil
}

// checkPlatformNamespace: nil when namespace is none of the platform's
// reserved namespaces, global.okdp.policy.reserved of okdp, the
// platform's settings: a reservation wins over a privilege.
func checkPlatformNamespace(field, namespace string, okdp M) error {
	if !reservedByPlatform(okdp, namespace) {
		return nil
	}
	also := ""
	if privileged(okdp, namespace) {
		also = ", privileged or not: global.okdp.policy.privileged lists it too, and the reservation wins"
	}
	return fmt.Errorf("%s %q: the namespace is reserved by the platform (global.okdp.policy.reserved): no project has it%s", field, namespace, also)
}

// CheckNamespace: nil when namespace may be a project's, okdp the
// platform's settings (global.okdp): checkNamespace, then
// checkPlatformNamespace. A namespace a target writes in by its options
// (the flux target's Source) is the target's to refuse
// (target.OwnNamespaces).
func CheckNamespace(field, namespace string, okdp M) error {
	if err := checkNamespace(field, namespace); err != nil {
		return err
	}
	return checkPlatformNamespace(field, namespace, okdp)
}

// clusterScoped: the well-known cluster-scoped kinds, <group>/<kind>, the
// group of the apiVersion ("" for the core group): Kubernetes' own, and
// those of the operators OKDP's packages install objects of or that
// commonly run beside them (External Secrets, cert-manager, trust-manager,
// Kyverno, Gatekeeper, KEDA, Cilium, Calico, OpenShift, Argo Workflows).
// A kind that is not listed is assumed namespaced: the engine's identity,
// confined to the namespace, refuses it in the cluster.
var clusterScoped = func() map[string]bool {
	out := map[string]bool{}
	for group, kinds := range map[string][]string{
		"":                               {"Namespace", "Node", "PersistentVolume"},
		"storage.k8s.io":                 {"StorageClass", "CSIDriver", "CSINode", "VolumeAttachment", "VolumeAttributesClass"},
		"rbac.authorization.k8s.io":      {"ClusterRole", "ClusterRoleBinding"},
		"apiextensions.k8s.io":           {"CustomResourceDefinition"},
		"admissionregistration.k8s.io":   {"ValidatingWebhookConfiguration", "MutatingWebhookConfiguration", "ValidatingAdmissionPolicy", "ValidatingAdmissionPolicyBinding", "MutatingAdmissionPolicy", "MutatingAdmissionPolicyBinding"},
		"scheduling.k8s.io":              {"PriorityClass"},
		"networking.k8s.io":              {"IngressClass", "IPAddress", "ServiceCIDR"},
		"node.k8s.io":                    {"RuntimeClass"},
		"apiregistration.k8s.io":         {"APIService"},
		"certificates.k8s.io":            {"CertificateSigningRequest", "ClusterTrustBundle"},
		"flowcontrol.apiserver.k8s.io":   {"FlowSchema", "PriorityLevelConfiguration"},
		"policy":                         {"PodSecurityPolicy"},
		"resource.k8s.io":                {"DeviceClass", "ResourceSlice"},
		"snapshot.storage.k8s.io":        {"VolumeSnapshotClass", "VolumeSnapshotContent"},
		"gateway.networking.k8s.io":      {"GatewayClass"},
		"external-secrets.io":            {"ClusterSecretStore", "ClusterExternalSecret", "ClusterPushSecret"},
		"generators.external-secrets.io": {"ClusterGenerator"},
		"cert-manager.io":                {"ClusterIssuer"},
		"trust.cert-manager.io":          {"Bundle"},
		"kyverno.io":                     {"ClusterPolicy", "ClusterCleanupPolicy"},
		"templates.gatekeeper.sh":        {"ConstraintTemplate"},
		"keda.sh":                        {"ClusterTriggerAuthentication"},
		"cilium.io":                      {"CiliumClusterwideNetworkPolicy"},
		"crd.projectcalico.org":          {"GlobalNetworkPolicy"},
		"projectcalico.org":              {"GlobalNetworkPolicy"},
		"security.openshift.io":          {"SecurityContextConstraints"},
		"quota.openshift.io":             {"ClusterResourceQuota"},
		"argoproj.io":                    {"ClusterWorkflowTemplate"},
	} {
		for _, k := range kinds {
			out[group+"/"+k] = true
		}
	}
	return out
}()

// engineGroups: the API groups of the deployer engines' custom resources,
// which a project that is not privileged may not install: a HelmRelease, a
// GitRepository, a Kustomization or an Application in the project's
// namespace lets it drive the engine with the engine's own identity. The
// platform's lockdown of the engines (--no-cross-namespace-refs,
// --default-service-account, Argo CD's application.namespaces) is the
// second line.
var engineGroups = map[string]bool{
	"helm.toolkit.fluxcd.io":         true,
	"kustomize.toolkit.fluxcd.io":    true,
	"source.toolkit.fluxcd.io":       true,
	"notification.toolkit.fluxcd.io": true,
	"image.toolkit.fluxcd.io":        true,
	"argoproj.io":                    true,
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
// of a cluster-scoped kind (clusterScoped); the items of one that holds a
// list of them (items: a List, which Helm installs item by item) checked
// alike. The first refused, in order, fails, named <kind>/<name>: the
// platform's projects alone (global.okdp.policy.privileged) install
// cluster-scoped objects and write other namespaces.
func checkObjects(svc, namespace string, objects []M) error {
	return eachObject(objects, func(o M) error {
		meta := value.AsMap(o["metadata"])
		id := value.Str(o["kind"]) + "/" + value.Str(meta["name"])
		version := value.Str(o["apiVersion"])
		if clusterScoped[apiGroup(version)+"/"+value.Str(o["kind"])] {
			return fmt.Errorf("%s: objects: %s: a cluster-scoped kind (%s): the platform's projects install cluster-scoped objects (global.okdp.policy.privileged), %s is not one of them", svc, id, version, namespace)
		}
		if group := apiGroup(version); engineGroups[group] {
			return fmt.Errorf("%s: objects: %s: an object of the deployer's kinds (%s) lets a project drive the platform's engine: the platform's projects set it (global.okdp.policy.privileged), %s is not one of them", svc, id, version, namespace)
		}
		if ns, set := meta["namespace"]; set && ns != nil && ns != "" && ns != namespace {
			return fmt.Errorf("%s: objects: %s: namespace %s, not the instance's (%s): the platform's projects write other namespaces (global.okdp.policy.privileged), %s is not one of them", svc, id, value.Str(ns), namespace, namespace)
		}
		return nil
	})
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

// deniedKeys: the keys the upstream values of an instance of a project
// that is not privileged set nowhere, at any depth, to any value: those of
// a pod's privileges on its node (privileged, the host's namespaces, ports
// and paths, capabilities, sysctls), its security context whole, its
// identity (its ServiceAccount, its token), and the containers and objects
// a chart adds from its values (sidecars, init containers, extra objects).
// runAsUser 0 and runAsNonRoot false too (upstreamRules.key). The
// scheduling keys (nodeSelector, tolerations, affinity) stay free.
var deniedKeys = func() map[string]bool {
	out := map[string]bool{}
	for _, k := range []string{
		"privileged", "hostPath", "hostNetwork", "hostPID", "hostIPC", "hostPort",
		"allowPrivilegeEscalation", "capabilities", "seccompProfile", "sysctls",
		"securityContext", "podSecurityContext", "containerSecurityContext",
		"serviceAccountName", "serviceAccount", "automountServiceAccountToken",
		"extraContainers", "sidecars", "initContainers", "extraInitContainers",
		"extraObjects", "extraDeploy", "extraManifests",
	} {
		out[k] = true
	}
	return out
}()

// upstreamRules: what the upstream values of an instance of a project
// that is not privileged may not set (upstreamCheck), for the messages its
// namespace.
type upstreamRules struct {
	namespace  string
	deny       map[string]bool // the platform's keys, global.okdp.policy.upstream.deny, beside deniedKeys
	registries []string        // global.okdp.policy.upstream.registries; nil: the images are not checked
}

// upstreamCheck: the platform's check of the upstream values of an
// instance of namespace (chart.UpstreamCheck), of global.okdp.policy of
// okdp: none for a privileged project, which sets any upstream value; for
// the others, no key of deniedKeys nor of policy.upstream.deny, no
// runAsUser 0 nor runAsNonRoot false (upstreamRules.key), and, with
// policy.upstream.registries, every image from one of them
// (upstreamRules.image). The package's values are not checked: they are
// reviewed with it.
func upstreamCheck(okdp M, namespace string, privileged bool) chart.UpstreamCheck {
	if privileged {
		return nil
	}
	r := &upstreamRules{namespace: namespace, deny: map[string]bool{}}
	for _, k := range value.AsList(value.Dig(okdp, "policy.upstream.deny")) {
		r.deny[value.Str(k)] = true
	}
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
// path p, set to v: a key of deniedKeys or of the platform's deny list, to
// any value; runAsUser 0, root; runAsNonRoot false.
func (r *upstreamRules) key(k string, v any, p string, at func(string) string) error {
	var why string
	switch {
	case deniedKeys[k]:
		why = k + " is denied to the instance's upstream values (a pod's privileges, identity, containers or objects)"
	case r.deny[k]:
		why = k + " is denied to the instance's upstream values (global.okdp.policy.upstream.deny)"
	case k == "runAsUser" && v == 0.0:
		why = "runAsUser 0, root, is denied to the instance's upstream values"
	case k == "runAsNonRoot" && v == false:
		why = "runAsNonRoot false is denied to the instance's upstream values"
	default:
		return nil
	}
	return fmt.Errorf("%s: %s: the platform's projects set it (global.okdp.policy.privileged), %s is not one of them", at(p), why, r.namespace)
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
// tag, a digest).
func (r *upstreamRules) image(m, base M, k, p string, at func(string) string) error {
	if r.registries == nil {
		return nil
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
// unless Inputs.AllowPlainSecrets: none a Secret (v1) holding values, a
// non-empty data or stringData, the items of one that holds a list of
// them (items) alike. The objects file is written to the deployments
// repository: credentials are generated in the cluster
// (okdpGeneratedSecret, a provides secret's generate) or read from the
// platform's secret store (an ExternalSecret), never written in clear. A
// Secret with only metadata and type passes. The first refused, in order,
// fails, named Secret/<name>, its key of values named, data before
// stringData.
func checkPlainSecrets(svc string, objects []M) error {
	return eachObject(objects, func(o M) error {
		if value.Str(o["kind"]) == "Secret" && apiGroup(value.Str(o["apiVersion"])) == "" {
			for _, k := range []string{"data", "stringData"} {
				if len(value.AsMap(o[k])) > 0 {
					return fmt.Errorf("%s: objects: Secret/%s: a plain value (%s): credentials are generated (okdpGeneratedSecret) or read from the platform's secret store (an ExternalSecret); Inputs.AllowPlainSecrets allows it", svc, value.Str(value.Dig(o, "metadata.name")), k)
				}
			}
		}
		return nil
	})
}
