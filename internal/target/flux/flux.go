// Package flux: the flux target: the umbrella chart of each instance, with
// the Flux HelmRelease that installs it, waiting for the releases its
// instance depends on (dependsOn), and the Kustomization of its values
// ConfigMap; a Kustomization per project. The format is okdp-gitops',
// byte for byte but for the mark of the first line, so that it can write
// its package instances with Instance and Project.
package flux

import (
	"fmt"
	"path"
	"slices"
	"strings"

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
	// its values ConfigMap, in its directory; a project's, its instances'
	// directories, in the project's.
	KustomizationFile = "kustomization.yaml"
)

// ReleasesNamespace: the namespace of the HelmReleases and of their values
// ConfigMaps, okdp-gitops': their names, the releases', must be unique.
const ReleasesNamespace = "okdp-releases"

// DefaultSource: the GitRepository of the tree, okdp-gitops'.
const DefaultSource = "flux-system/okdp-gitops"

// Target: the flux target.
type Target struct {
	Source     string // the GitRepository the HelmReleases read the charts from, <namespace>/<name>; DefaultSource when empty
	PathPrefix string // the path of the tree in that repository, "" its root
}

// Files: the helm target's files, then, in each instance's
// target.InstanceDir, its HelmReleaseFile and KustomizationFile
// (Instance), and in each project's target.ProjectDir, its
// KustomizationFile (Project), each marked generated (target.Generated).
// The options are checked first; two releases of one name in two
// namespaces are refused: their HelmReleases share ReleasesNamespace.
func (t Target) Files(results []*okdp.Result, g *graph.Graph) (map[string][]byte, error) {
	if err := t.check(); err != nil {
		return nil, err
	}
	if err := target.UniqueReleases(results, "the HelmReleases of "+ReleasesNamespace); err != nil {
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
		for name, data := range t.Instance(res.Namespace, res.Release, path.Join(t.PathPrefix, dir), dependsOn) {
			files[path.Join(dir, name)] = append([]byte(target.Generated), data...)
		}
		instances[res.Namespace] = append(instances[res.Namespace], path.Join("instances", res.Release))
	}
	for ns, dirs := range instances {
		files[path.Join(target.ProjectDir(ns), KustomizationFile)] = append([]byte(target.Generated), Project(dirs)...)
	}
	return files, nil
}

// check: the options.
func (t Target) check() error {
	if ns, name, ok := strings.Cut(t.source(), "/"); !ok || !chart.IsDNSLabel(ns) || !chart.IsDNSLabel(name) {
		return fmt.Errorf("Flux.Source %q: the GitRepository of the tree, <namespace>/<name>", t.Source)
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
// for the releases dependsOn, sorted; the Kustomization of the
// HelmRelease and of that ConfigMap, of the chart's
// helm.ValuesFile. The options are those Files checks.
func (t Target) Instance(namespace, release, dir string, dependsOn []string) map[string][]byte {
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
	hr.Add("    okdp.io/project: %q", namespace)
	hr.Add("    okdp.io/instance: %q", release)
	hr.Add("spec:")
	hr.Add("  interval: 10m")
	hr.Add("  releaseName: %q", release)
	hr.Add("  targetNamespace: %q", namespace)
	hr.Add("  storageNamespace: %q", namespace)
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
	hr.Add("    createNamespace: true")
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
// directories of its instances, relative to the project's, sorted.
func Project(dirs []string) []byte {
	var k target.Lines
	k.Add("apiVersion: kustomize.config.k8s.io/v1beta1")
	k.Add("kind: Kustomization")
	k.Add("namespace: %s", ReleasesNamespace)
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
