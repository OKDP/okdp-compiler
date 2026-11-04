// Package flux: the flux target: the umbrella chart of each instance, with
// the Flux HelmRelease that installs it, waiting for the releases its
// instance depends on (dependsOn), and the Kustomization of its values
// ConfigMap; each project's own manifests, its Namespace, the platform's
// secret store in it, the RoleBindings of its groups and the identity its
// releases are installed with; a
// Kustomization per project, and the root's, of the projects: all a Flux
// Kustomization of the path projects/ syncs. The format is okdp-gitops',
// which commits the files as they are.
package flux

import (
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/okdp/okdp-compiler/internal/chart"
	"github.com/okdp/okdp-compiler/internal/graph"
	"github.com/okdp/okdp-compiler/internal/okdp"
	"github.com/okdp/okdp-compiler/internal/target"
	"github.com/okdp/okdp-compiler/internal/target/helm"
)

// The files of the target besides the umbrella charts.
const (
	// HelmReleaseFile: an instance's HelmRelease, in its directory.
	HelmReleaseFile = "helmrelease.yaml"
	// KustomizationFile: an instance's Kustomization, its HelmRelease and
	// its values ConfigMap, in its directory; a project's manifests', in
	// its target.ProjectManifestsDir; a project's, its instances'
	// directories and its manifests', in the project's; the root's, the
	// projects' directories, in target.ProjectsDir.
	KustomizationFile = "kustomization.yaml"
	// ServiceAccountFile: the identity of a project's releases, in its
	// target.ProjectManifestsDir (ServiceAccount); not written for one of
	// the platform's own projects, whose releases keep helm-controller's.
	ServiceAccountFile = "service-account.yaml"
)

// ReleasesNamespace: the namespace of the HelmReleases and of their values
// ConfigMaps, okdp-gitops': their names, the releases', must be unique.
const ReleasesNamespace = "okdp-releases"

// ReleaseObjects: the objects of the target named after the releases,
// which share ReleasesNamespace: what a release in two namespaces makes
// clash (target.UniqueReleases).
const ReleaseObjects = "the HelmReleases of " + ReleasesNamespace

// RoleName: okdp-project, the Role of a project's namespace its releases
// are installed with, and its RoleBinding.
const RoleName = "okdp-project"

// ServiceAccountName: project-<namespace>, the ServiceAccount of
// ReleasesNamespace helm-controller impersonates for the releases of a
// project, bound to RoleName of its namespace.
func ServiceAccountName(namespace string) string { return "project-" + namespace }

// DefaultSource: the GitRepository of the tree, okdp-gitops'.
const DefaultSource = "flux-system/okdp-gitops"

// Target: the flux target.
type Target struct {
	Source     string   // the GitRepository the HelmReleases read the charts from, <namespace>/<name>; DefaultSource when empty
	PathPrefix string   // the path of the tree in that repository, "" its root
	Keep       []string // namespaces the root Kustomization lists though no result is given: projects whose directories the output keeps as they are, not written (okdp-compile --partial)
}

// Files: the helm target's files, then, in each instance's
// target.InstanceDir, its HelmReleaseFile and KustomizationFile
// (Instance); in each project's target.ProjectManifests, its manifests
// (ProjectManifests) and their KustomizationFile; in each project's
// target.ProjectDir, its KustomizationFile (Project), and in
// target.ProjectsDir the root's (Projects), the namespaces of Keep among
// them, even without results, so that a Kustomization of the path syncs an
// empty tree; each marked generated
// (target.Generated). The options are checked first; two releases of one
// name in two namespaces are refused: their HelmReleases share
// ReleasesNamespace; and a project of ReleasesNamespace or of the Source's
// namespace (target.OwnNamespaces). The platform's own objects in these
// namespaces take a name a release cannot (a dot: okdp.projects), the
// HelmReleases being named after the releases, their values ConfigMaps
// values-<release>, the projects' ServiceAccounts project-<namespace>.
func (t Target) Files(results []*okdp.Result, g *graph.Graph) (map[string][]byte, error) {
	if err := t.check(); err != nil {
		return nil, err
	}
	if err := target.UniqueReleases(results, ReleaseObjects); err != nil {
		return nil, err
	}
	source, _, _ := strings.Cut(t.source(), "/")
	if err := target.OwnNamespaces(results, map[string]string{ReleasesNamespace: ReleaseObjects + " and the identities of the projects", source: "the GitRepository of the tree (Flux.Source)"}); err != nil {
		return nil, err
	}
	files, err := helm.Target{}.Files(results, g)
	if err != nil {
		return nil, err
	}
	instances := map[string][]string{}
	for _, res := range results {
		dir := target.InstanceDir(res.Namespace, res.Release)
		var dependsOn []string
		for _, n := range g.DependsOn(graph.Node{Namespace: res.Namespace, Release: res.Release}) {
			dependsOn = append(dependsOn, n.Release)
		}
		for name, data := range t.Instance(res.Namespace, res.Release, path.Join(t.PathPrefix, dir), dependsOn, res.Privileged) {
			files[path.Join(dir, name)] = append([]byte(target.Generated), data...)
		}
		instances[res.Namespace] = append(instances[res.Namespace], path.Join("instances", res.Release))
	}
	var namespaces []string
	for _, p := range target.Projects(results) {
		manifests, err := ProjectManifests(p)
		if err != nil {
			return nil, err
		}
		for name, data := range manifests {
			files[path.Join(target.ProjectManifests(p.Namespace), name)] = append([]byte(target.Generated), data...)
		}
		dirs := slices.Concat(instances[p.Namespace], []string{target.ProjectManifestsDir})
		files[path.Join(target.ProjectDir(p.Namespace), KustomizationFile)] = append([]byte(target.Generated), Project(dirs)...)
		namespaces = append(namespaces, p.Namespace)
	}
	for _, ns := range t.Keep {
		if !slices.Contains(namespaces, ns) {
			namespaces = append(namespaces, ns)
		}
	}
	files[path.Join(target.ProjectsDir, KustomizationFile)] = append([]byte(target.Generated), Projects(namespaces)...)
	return files, nil
}

// Kept: the files of a project kept (target.Keeper), of p, each marked
// generated: its manifests (ProjectManifests) in its
// target.ProjectManifests; and, for each instance whose HelmReleaseFile
// previous holds, its HelmReleaseFile and KustomizationFile as Instance
// writes them today, its identity of p (the project's ServiceAccountName
// unless privileged), its dependsOn those of the previous HelmRelease:
// the rest of the instance's files, its chart and values, kept as they
// are. A previous HelmRelease that does not read fails it. The options
// are checked first.
func (t Target) Kept(p target.Project, previous map[string][]byte) (map[string][]byte, error) {
	if err := t.check(); err != nil {
		return nil, err
	}
	manifests, err := ProjectManifests(p)
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{}
	for name, data := range manifests {
		files[path.Join(target.ProjectManifests(p.Namespace), name)] = append([]byte(target.Generated), data...)
	}
	prefix := path.Join(target.ProjectDir(p.Namespace), "instances") + "/"
	for _, file := range slices.Sorted(maps.Keys(previous)) {
		rest, ok := strings.CutPrefix(file, prefix)
		release, name, _ := strings.Cut(rest, "/")
		if !ok || name != HelmReleaseFile {
			continue
		}
		if !chart.IsRelease(release) {
			return nil, fmt.Errorf("%s: %q is no release", file, release)
		}
		var hr struct {
			Spec struct {
				DependsOn []struct {
					Name string `yaml:"name"`
				} `yaml:"dependsOn"`
			} `yaml:"spec"`
		}
		if err := yaml.Unmarshal(previous[file], &hr); err != nil {
			return nil, fmt.Errorf("%s: %w", file, err)
		}
		var dependsOn []string
		for _, d := range hr.Spec.DependsOn {
			dependsOn = append(dependsOn, d.Name)
		}
		dir := target.InstanceDir(p.Namespace, release)
		for name, data := range t.Instance(p.Namespace, release, path.Join(t.PathPrefix, dir), dependsOn, p.Privileged) {
			files[path.Join(dir, name)] = append([]byte(target.Generated), data...)
		}
	}
	return files, nil
}

// ProjectManifests: the files of a project's target.ProjectManifestsDir,
// not marked: target.NamespaceFile, the Namespace (target.Namespace);
// target.SecretStoreFile, the platform's secret store in it, when the
// platform names a provider (target.SecretStore); target.AccessFile, the
// RoleBindings of its groups, when one has a role (target.Access);
// target.ControlPlaneFile, the control plane's Role and RoleBinding, when
// the platform names its identity (target.ControlPlane); each as the
// argocd target writes it; Flux's own, ServiceAccountFile, for
// a project that is not one of the platform's own (ServiceAccount); and
// their KustomizationFile, which sets no namespace: each manifest carries
// its own, the Namespace none.
func ProjectManifests(p target.Project) (map[string][]byte, error) {
	files := map[string][]byte{target.NamespaceFile: target.Namespace(p)}
	store, err := target.SecretStore(p)
	if err != nil {
		return nil, err
	}
	if store != nil {
		files[target.SecretStoreFile] = store
	}
	if access := target.Access(p); access != nil {
		files[target.AccessFile] = access
	}
	if cp := target.ControlPlane(p); cp != nil {
		files[target.ControlPlaneFile] = cp
	}
	if !p.Privileged {
		files[ServiceAccountFile] = ServiceAccount(p.Namespace)
	}
	var k target.Lines
	k.Add("apiVersion: kustomize.config.k8s.io/v1beta1")
	k.Add("kind: Kustomization")
	k.Add("resources:")
	for _, name := range slices.Sorted(maps.Keys(files)) {
		k.Add("  - %s", name)
	}
	files[KustomizationFile] = []byte(k.String())
	return files, nil
}

// ServiceAccount: the ServiceAccountFile of a project, not marked: the
// ServiceAccount ServiceAccountName of ReleasesNamespace, which
// helm-controller impersonates for the project's releases (Instance), the
// Role RoleName of the project's namespace, every verb on every resource
// of the namespace (the boundary is the namespace, not the kinds), and
// the RoleBinding of the one to the other: a cluster-scoped object, or
// one of another namespace, is refused by the API server.
func ServiceAccount(namespace string) []byte {
	var l target.Lines
	l.Add("apiVersion: v1")
	l.Add("kind: ServiceAccount")
	l.Add("metadata:")
	l.Add("  name: %q", ServiceAccountName(namespace))
	l.Add("  namespace: %s", ReleasesNamespace)
	l.Add("  labels:")
	l.Add("    %s: %q", target.ProjectLabel, namespace)
	l.Add("---")
	l.Add("apiVersion: rbac.authorization.k8s.io/v1")
	l.Add("kind: Role")
	l.Add("metadata:")
	l.Add("  name: %s", RoleName)
	l.Add("  namespace: %q", namespace)
	l.Add("  labels:")
	l.Add("    %s: %q", target.ProjectLabel, namespace)
	l.Add("rules:")
	l.Add("  - apiGroups: [\"*\"]")
	l.Add("    resources: [\"*\"]")
	l.Add("    verbs: [\"*\"]")
	l.Add("---")
	l.Add("apiVersion: rbac.authorization.k8s.io/v1")
	l.Add("kind: RoleBinding")
	l.Add("metadata:")
	l.Add("  name: %s", RoleName)
	l.Add("  namespace: %q", namespace)
	l.Add("  labels:")
	l.Add("    %s: %q", target.ProjectLabel, namespace)
	l.Add("roleRef:")
	l.Add("  apiGroup: rbac.authorization.k8s.io")
	l.Add("  kind: Role")
	l.Add("  name: %s", RoleName)
	l.Add("subjects:")
	l.Add("  - kind: ServiceAccount")
	l.Add("    name: %q", ServiceAccountName(namespace))
	l.Add("    namespace: %s", ReleasesNamespace)
	return []byte(l.String())
}

// check: the options.
func (t Target) check() error {
	if ns, name, ok := strings.Cut(t.source(), "/"); !ok || !chart.IsDNSLabel(ns) || !chart.IsDNSLabel(name) {
		return fmt.Errorf("Flux.Source %q: the GitRepository of the tree, <namespace>/<name>", t.Source)
	}
	for _, ns := range t.Keep {
		if !chart.IsDNSLabel(ns) {
			return fmt.Errorf("Flux.Keep %q: a namespace, a DNS label", ns)
		}
	}
	return target.CheckPrefix("Flux.PathPrefix", t.PathPrefix)
}

// source: Source, DefaultSource when empty.
func (t Target) source() string {
	if t.Source == "" {
		return DefaultSource
	}
	return t.Source
}

// Instance: the Flux files of a release of namespace, HelmReleaseFile and
// KustomizationFile, not marked: the HelmRelease of the umbrella chart of
// dir, its directory in the repository, its path prefix included, built at
// every revision, its values from the ConfigMap values-<release>, waiting
// for the releases dependsOn, sorted, installed as the project's
// ServiceAccountName (serviceAccountName) unless privileged, a release of
// one of the platform's own projects, which keeps helm-controller's own
// identity; the namespace is not created at install: the project's
// manifests hold it (ProjectManifests). Then the Kustomization of the
// HelmRelease and of that ConfigMap, of the chart's helm.ValuesFile. The
// options are those Files checks.
func (t Target) Instance(namespace, release, dir string, dependsOn []string, privileged bool) map[string][]byte {
	sourceNamespace, sourceName, _ := strings.Cut(t.source(), "/")
	values := "values-" + release
	var hr target.Lines
	hr.Add("---")
	hr.Add("apiVersion: helm.toolkit.fluxcd.io/v2")
	hr.Add("kind: HelmRelease")
	hr.Add("metadata:")
	hr.Add("  name: %q", release)
	hr.Add("  namespace: %s", ReleasesNamespace)
	hr.Add("  labels:")
	hr.Add("    %s: %q", target.ProjectLabel, namespace)
	hr.Add("    okdp.io/instance: %q", release)
	hr.Add("spec:")
	hr.Add("  interval: 10m")
	hr.Add("  releaseName: %q", release)
	hr.Add("  targetNamespace: %q", namespace)
	hr.Add("  storageNamespace: %q", namespace)
	if !privileged {
		hr.Add("  serviceAccountName: %q", ServiceAccountName(namespace))
	}
	// The umbrella of the directory, built at every revision: its version,
	// the package's, does not change when its dependencies do.
	hr.Add("  chart:")
	hr.Add("    spec:")
	hr.Add("      chart: %q", "./"+dir)
	hr.Add("      reconcileStrategy: Revision")
	hr.Add("      sourceRef:")
	hr.Add("        kind: GitRepository")
	hr.Add("        name: %s", sourceName)
	hr.Add("        namespace: %s", sourceNamespace)
	if len(dependsOn) > 0 {
		hr.Add("  dependsOn:")
		for _, d := range dependsOn {
			hr.Add("    - name: %q", d)
		}
	}
	hr.Add("  timeout: 15m")
	hr.Add("  install:")
	hr.Add("    createNamespace: false")
	hr.Add("    strategy:")
	hr.Add("      name: RetryOnFailure")
	hr.Add("      retryInterval: 2m")
	hr.Add("  upgrade:")
	hr.Add("    remediation:")
	hr.Add("      retries: 3")
	hr.Add("      strategy: rollback")
	hr.Add("  driftDetection:")
	hr.Add("    mode: enabled")
	hr.Add("  valuesFrom:")
	hr.Add("    - kind: ConfigMap")
	hr.Add("      name: %q", values)
	hr.Add("      valuesKey: %s", helm.ValuesFile)

	var k target.Lines
	k.Add("apiVersion: kustomize.config.k8s.io/v1beta1")
	k.Add("kind: Kustomization")
	k.Add("namespace: %s", ReleasesNamespace)
	k.Add("resources:")
	k.Add("  - %s", HelmReleaseFile)
	k.Add("configMapGenerator:")
	k.Add("  - name: %q", values)
	k.Add("    files:")
	k.Add("      - %s", helm.ValuesFile)
	k.Add("    options:")
	k.Add("      disableNameSuffixHash: true")
	k.Add("      labels:")
	k.Add("        reconcile.fluxcd.io/watch: Enabled")
	return map[string][]byte{HelmReleaseFile: []byte(hr.String()), KustomizationFile: []byte(k.String())}
}

// Project: the KustomizationFile of a project, not marked: the
// directories of its instances and of its manifests, relative to the
// project's, sorted.
func Project(dirs []string) []byte { return kustomization(dirs) }

// Projects: the root KustomizationFile, in target.ProjectsDir, not marked:
// the directories of the projects, their namespaces, sorted; an empty list
// when none.
func Projects(namespaces []string) []byte { return kustomization(namespaces) }

// kustomization: a Kustomization of the directories dirs, sorted. It sets
// no namespace, which kustomize would set on every resource of every
// directory under it, moving a project's manifests out of its namespace:
// each manifest carries its own, an instance's Kustomization
// ReleasesNamespace for its HelmRelease and its ConfigMap.
func kustomization(dirs []string) []byte {
	var k target.Lines
	k.Add("apiVersion: kustomize.config.k8s.io/v1beta1")
	k.Add("kind: Kustomization")
	if len(dirs) == 0 {
		k.Add("resources: []")
	} else {
		k.Add("resources:")
		for _, d := range slices.Sorted(slices.Values(dirs)) {
			k.Add("  - %s", d)
		}
	}
	return []byte(k.String())
}
