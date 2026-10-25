// Package argocd: the argocd target: the umbrella chart of each instance,
// with the Argo CD Application that installs it, its sync wave after those
// of the releases its instance depends on. The format is okdp-gitops',
// byte for byte but for the mark of the first line, so that it can write
// its package instances with Application.
package argocd

import (
	"errors"
	"path"

	"github.com/okdp/okdp-compiler/internal/graph"
	"github.com/okdp/okdp-compiler/internal/okdp"
	"github.com/okdp/okdp-compiler/internal/target"
	"github.com/okdp/okdp-compiler/internal/target/helm"
)

// ApplicationFile: an instance's Application, in its directory.
const ApplicationFile = "application.yaml"

// Namespace: the namespace of the Applications: their names, the
// releases', must be unique.
const Namespace = "argocd"

// Target: the argocd target.
type Target struct {
	RepoURL    string // the repository Argo CD reads the tree from: required
	Revision   string // its revision; HEAD when empty
	PathPrefix string // the path of the tree in the repository, "" its root
}

// Files: the helm target's files, then, in each instance's
// target.InstanceDir, its ApplicationFile (Application), its sync wave
// the release's (graph.Graph.Wave), marked generated (target.Generated).
// The options are checked first; two releases of one name in two
// namespaces are refused: their Applications share Namespace.
func (t Target) Files(results []*okdp.Result, g *graph.Graph) (map[string][]byte, error) {
	if err := t.check(); err != nil {
		return nil, err
	}
	if err := target.UniqueReleases(results, "the Applications of "+Namespace); err != nil {
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
	return files, nil
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
	a.Add("    okdp.io/project: %q", namespace)
	a.Add("    okdp.io/instance: %q", release)
	a.Add("  annotations:")
	a.Add("    argocd.argoproj.io/sync-wave: \"%d\"", wave)
	a.Add("    argocd.argoproj.io/compare-options: ServerSideDiff=true,IncludeMutationWebhook=true")
	a.Add("  finalizers:")
	a.Add("    - resources-finalizer.argocd.argoproj.io")
	a.Add("spec:")
	a.Add("  project: %q", "project-"+namespace)
	a.Add("  source:")
	a.Add("    repoURL: %q", t.RepoURL)
	a.Add("    targetRevision: %q", revision)
	a.Add("    path: %q", dir)
	a.Add("    helm:")
	a.Add("      releaseName: %q", release)
	a.Add("      valueFiles:")
	a.Add("        - %s", helm.ValuesFile)
	a.Add("  destination:")
	a.Add("    server: https://kubernetes.default.svc")
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
