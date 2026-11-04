// Package argocd: the argocd target: the umbrella chart of each instance,
// with the Argo CD Application that installs it, its sync wave after those
// of the releases its instance depends on, and each project's own
// manifests, its Namespace, the platform's secret store in it and the
// AppProject its Applications are in, confined to its namespace. The
// format is okdp-gitops', which commits the files as they are. An
// Application that applies the Applications (app of apps) reads the
// directory projects/, recurse, include */instances/*/application.yaml;
// an ApplicationSet over the directories projects/*/project applies the
// projects' manifests.
package argocd

import (
	"errors"
	"path"

	"github.com/okdp/okdp-compiler/internal/graph"
	"github.com/okdp/okdp-compiler/internal/okdp"
	"github.com/okdp/okdp-compiler/internal/target"
	"github.com/okdp/okdp-compiler/internal/target/helm"
)

// The files of the target besides the umbrella charts.
const (
	// ApplicationFile: an instance's Application, in its directory.
	ApplicationFile = "application.yaml"
	// AppProjectFile: a project's AppProject, in its
	// target.ProjectManifestsDir (AppProject).
	AppProjectFile = "appproject.yaml"
)

// Namespace: the namespace of the Applications and of the AppProjects:
// the Applications' names, the releases', must be unique.
const Namespace = "argocd"

// ReleaseObjects: the objects of the target named after the releases,
// which share Namespace: what a release in two namespaces makes clash
// (target.UniqueReleases).
const ReleaseObjects = "the Applications of " + Namespace

// ProjectName: project-<namespace>, the AppProject of a project, which
// its Applications are in.
func ProjectName(namespace string) string { return "project-" + namespace }

// Server: the cluster the Applications are destined to, the one Argo CD
// runs in.
const Server = "https://kubernetes.default.svc"

// Target: the argocd target.
type Target struct {
	RepoURL    string // the repository Argo CD reads the tree from: required
	Revision   string // its revision; HEAD when empty
	PathPrefix string // the path of the tree in the repository, "" its root
}

// Files: the helm target's files, then, in each instance's
// target.InstanceDir, its ApplicationFile (Application), its sync wave
// the release's (graph.Graph.Wave), and in each project's
// target.ProjectManifests, its manifests (ProjectManifests), each marked
// generated (target.Generated). The options are checked first; two
// releases of one name in two namespaces are refused: their Applications
// share Namespace; and a project of Namespace (target.OwnNamespaces).
// The platform's own objects in Namespace, the app of apps among them,
// take a name a release cannot (a dot: okdp.instances), the Applications
// being named after the releases, <project>-<instance>, the AppProjects
// project-<namespace>: an Application okdp-instances would be the
// release of the instance instances of a project okdp.
func (t Target) Files(results []*okdp.Result, g *graph.Graph) (map[string][]byte, error) {
	if err := t.check(); err != nil {
		return nil, err
	}
	if err := target.UniqueReleases(results, ReleaseObjects); err != nil {
		return nil, err
	}
	if err := target.OwnNamespaces(results, map[string]string{Namespace: ReleaseObjects + " and the AppProjects"}); err != nil {
		return nil, err
	}
	files, err := helm.Target{}.Files(results, g)
	if err != nil {
		return nil, err
	}
	for _, res := range results {
		dir := target.InstanceDir(res.Namespace, res.Release)
		wave := g.Wave(graph.Node{Namespace: res.Namespace, Release: res.Release})
		files[path.Join(dir, ApplicationFile)] = append([]byte(target.Generated), t.Application(res.Namespace, res.Release, path.Join(t.PathPrefix, dir), wave)...)
	}
	for _, p := range target.Projects(results) {
		manifests, err := t.ProjectManifests(p)
		if err != nil {
			return nil, err
		}
		for name, data := range manifests {
			files[path.Join(target.ProjectManifests(p.Namespace), name)] = append([]byte(target.Generated), data...)
		}
	}
	return files, nil
}

// ProjectManifests: the files of a project's target.ProjectManifestsDir,
// not marked: target.NamespaceFile, the Namespace, kept when the project
// is gone (target.Namespace); target.SecretStoreFile, the platform's
// secret store in it, when the platform names a provider
// (target.SecretStore), both as the flux target writes them; and
// AppProjectFile, the AppProject of its Applications (AppProject), Argo
// CD's own. The options are those Files checks.
func (t Target) ProjectManifests(p target.Project) (map[string][]byte, error) {
	files := map[string][]byte{target.NamespaceFile: target.Namespace(p), AppProjectFile: t.AppProject(p)}
	store, err := target.SecretStore(p)
	if err != nil {
		return nil, err
	}
	if store != nil {
		files[target.SecretStoreFile] = store
	}
	return files, nil
}

// AppProject: the AppProjectFile of a project, not marked: the AppProject
// ProjectName in Namespace, labelled target.ProjectLabel, its source
// RepoURL; its destinations the project's namespace of Server, no
// cluster-scoped resource (an empty clusterResourceWhitelist: a
// ClusterRole or a CRD of an instance is refused), or, for one of the
// platform's own projects, every namespace and every cluster-scoped
// resource. The options are those Files checks.
func (t Target) AppProject(p target.Project) []byte {
	var a target.Lines
	a.Add("apiVersion: argoproj.io/v1alpha1")
	a.Add("kind: AppProject")
	a.Add("metadata:")
	a.Add("  name: %q", ProjectName(p.Namespace))
	a.Add("  namespace: %s", Namespace)
	a.Add("  labels:")
	a.Add("    %s: %q", target.ProjectLabel, p.Namespace)
	a.Add("spec:")
	if p.Privileged {
		a.Add("  description: %q", "OKDP project "+p.Namespace+", one of the platform's own: its instances install cluster-scoped objects and write every namespace")
	} else {
		a.Add("  description: %q", "OKDP project "+p.Namespace+": its instances, confined to its namespace")
	}
	a.Add("  sourceRepos:")
	a.Add("    - %q", t.RepoURL)
	a.Add("  destinations:")
	a.Add("    - server: %s", Server)
	if p.Privileged {
		a.Add("      namespace: \"*\"")
		a.Add("  clusterResourceWhitelist:")
		a.Add("    - group: \"*\"")
		a.Add("      kind: \"*\"")
	} else {
		a.Add("      namespace: %q", p.Namespace)
		a.Add("  clusterResourceWhitelist: []")
	}
	return []byte(a.String())
}

// check: the options.
func (t Target) check() error {
	if t.RepoURL == "" {
		return errors.New("ArgoCD.RepoURL is required")
	}
	return errors.Join(target.CheckQuoted("ArgoCD.RepoURL", t.RepoURL), target.CheckQuoted("ArgoCD.Revision", t.Revision), target.CheckPrefix("ArgoCD.PathPrefix", t.PathPrefix))
}

// Application: the ApplicationFile of a release of namespace, not marked:
// the umbrella chart of dir, its directory in the repository, its path
// prefix included, with its helm.ValuesFile, in the AppProject
// project-<namespace>, its sync wave wave. The options are those Files
// checks.
func (t Target) Application(namespace, release, dir string, wave int) []byte {
	revision := t.Revision
	if revision == "" {
		revision = "HEAD"
	}
	var a target.Lines
	a.Add("apiVersion: argoproj.io/v1alpha1")
	a.Add("kind: Application")
	a.Add("metadata:")
	a.Add("  name: %q", release)
	a.Add("  namespace: %s", Namespace)
	a.Add("  labels:")
	a.Add("    %s: %q", target.ProjectLabel, namespace)
	a.Add("    okdp.io/instance: %q", release)
	a.Add("  annotations:")
	a.Add("    argocd.argoproj.io/sync-wave: \"%d\"", wave)
	a.Add("    argocd.argoproj.io/compare-options: ServerSideDiff=true,IncludeMutationWebhook=true")
	a.Add("  finalizers:")
	a.Add("    - resources-finalizer.argocd.argoproj.io")
	a.Add("spec:")
	a.Add("  project: %q", ProjectName(namespace))
	a.Add("  source:")
	a.Add("    repoURL: %q", t.RepoURL)
	a.Add("    targetRevision: %q", revision)
	a.Add("    path: %q", dir)
	a.Add("    helm:")
	a.Add("      releaseName: %q", release)
	a.Add("      valueFiles:")
	a.Add("        - %s", helm.ValuesFile)
	a.Add("  destination:")
	a.Add("    server: %s", Server)
	a.Add("    namespace: %q", namespace)
	a.Add("  ignoreDifferences:")
	a.Add("    - group: admissionregistration.k8s.io")
	a.Add("      kind: MutatingWebhookConfiguration")
	a.Add("      jqPathExpressions: ['.webhooks[]?.clientConfig.caBundle']")
	a.Add("    - group: admissionregistration.k8s.io")
	a.Add("      kind: ValidatingWebhookConfiguration")
	a.Add("      jqPathExpressions: ['.webhooks[]?.clientConfig.caBundle']")
	a.Add("    - group: apiextensions.k8s.io")
	a.Add("      kind: CustomResourceDefinition")
	a.Add("      jsonPointers: [/spec/conversion/webhook/clientConfig/caBundle]")
	a.Add("  syncPolicy:")
	a.Add("    automated:")
	a.Add("      prune: true")
	a.Add("      selfHeal: true")
	a.Add("    syncOptions:")
	a.Add("      - RespectIgnoreDifferences=true")
	a.Add("      - ServerSideApply=true")
	a.Add("    retry:")
	a.Add("      limit: -1")
	a.Add("      backoff:")
	a.Add("        duration: 10s")
	a.Add("        factor: 2")
	a.Add("        maxDuration: 5m")
	return []byte(a.String())
}
