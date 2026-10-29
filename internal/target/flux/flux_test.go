package flux

import (
	"strings"
	"testing"

	"github.com/okdp/okdp-compiler/internal/graph"
	"github.com/okdp/okdp-compiler/internal/okdp"
	"github.com/okdp/okdp-compiler/internal/target"
)

// TestCheck: the options, the GitRepository of the tree, <namespace>/<name>
// (DefaultSource when empty), and the path of the tree in it.
func TestCheck(t *testing.T) {
	for name, tc := range map[string]struct {
		target Target
		want   string
	}{
		"the defaults":        {Target{}, ""},
		"a source and prefix": {Target{Source: "flux-system/tree", PathPrefix: "deploy/okdp"}, ""},
		"no namespace":        {Target{Source: "okdp"}, `Flux.Source "okdp": the GitRepository of the tree, <namespace>/<name>`},
		"a third element":     {Target{Source: "a/b/c"}, `Flux.Source "a/b/c": the GitRepository of the tree, <namespace>/<name>`},
		"not a label":         {Target{Source: "Flux/tree"}, `Flux.Source "Flux/tree": the GitRepository of the tree, <namespace>/<name>`},
		"an absolute prefix":  {Target{PathPrefix: "/deploy"}, `Flux.PathPrefix "/deploy": the path of the tree in the repository, relative, without an empty, . or .. segment`},
		"a dot prefix":        {Target{PathPrefix: "."}, `Flux.PathPrefix ".": the path of the tree in the repository, relative, without an empty, . or .. segment`},
		"a dotdot prefix":     {Target{PathPrefix: "a/../b"}, `Flux.PathPrefix "a/../b": the path of the tree in the repository, relative, without an empty, . or .. segment`},
	} {
		t.Run(name, func(t *testing.T) {
			var got string
			if err := tc.target.check(); err != nil {
				got = err.Error()
			}
			if got != tc.want {
				t.Errorf("got %q\nwant %q", got, tc.want)
			}
		})
	}
}

// hr: the HelmRelease of release demo-web of namespace demo, in
// deploy/projects/demo/instances/demo-web, waiting for demo-db and demo-cache.
const hr = `---
apiVersion: helm.toolkit.fluxcd.io/v2
kind: HelmRelease
metadata:
  name: "demo-web"
  namespace: okdp-releases
  labels:
    okdp.io/project: "demo"
    okdp.io/instance: "demo-web"
spec:
  interval: 10m
  releaseName: "demo-web"
  targetNamespace: "demo"
  storageNamespace: "demo"
  chart:
    spec:
      chart: "./deploy/projects/demo/instances/demo-web"
      reconcileStrategy: Revision
      sourceRef:
        kind: GitRepository
        name: tree
        namespace: flux-system
  dependsOn:
    - name: "demo-db"
    - name: "demo-cache"
  timeout: 15m
  install:
    createNamespace: true
    strategy:
      name: RetryOnFailure
      retryInterval: 2m
  upgrade:
    remediation:
      retries: 3
      strategy: rollback
  driftDetection:
    mode: enabled
  valuesFrom:
    - kind: ConfigMap
      name: "values-demo-web"
      valuesKey: instance.values.yaml
`

// TestInstance: the HelmRelease of a release, with and without releases to
// wait for, the releases in the order given, and the Kustomization of the
// HelmRelease and of its values ConfigMap.
func TestInstance(t *testing.T) {
	tg := Target{Source: "flux-system/tree", PathPrefix: "deploy"}
	dir := "projects/demo/instances/demo-web"
	got := tg.Instance("demo", "demo-web", "deploy/"+dir, []string{"demo-db", "demo-cache"})
	if string(got[HelmReleaseFile]) != hr {
		t.Errorf("with dependsOn:\n%s", got[HelmReleaseFile])
	}
	wantK := `apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
namespace: okdp-releases
resources:
  - helmrelease.yaml
configMapGenerator:
  - name: "values-demo-web"
    files:
      - instance.values.yaml
    options:
      disableNameSuffixHash: true
      labels:
        reconcile.fluxcd.io/watch: Enabled
`
	if string(got[KustomizationFile]) != wantK || len(got) != 2 {
		t.Errorf("kustomization:\n%s", got[KustomizationFile])
	}
	without := strings.Replace(hr, "  dependsOn:\n    - name: \"demo-db\"\n    - name: \"demo-cache\"\n", "", 1)
	if got := tg.Instance("demo", "demo-web", "deploy/"+dir, nil); string(got[HelmReleaseFile]) != without || strings.Contains(without, "dependsOn") {
		t.Errorf("without dependsOn:\n%s", got[HelmReleaseFile])
	}
	if got := (Target{}).Instance("demo", "demo-web", dir, nil)[HelmReleaseFile]; !strings.Contains(string(got), "name: okdp-gitops\n        namespace: flux-system\n") {
		t.Errorf("the default source:\n%s", got)
	}
}

// TestProject: the Kustomization of a project, its instances' directories
// sorted, none an empty list.
func TestProject(t *testing.T) {
	want := "apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nnamespace: okdp-releases\nresources:\n  - instances/a\n  - instances/b\n"
	if got := Project([]string{"instances/b", "instances/a"}); string(got) != want {
		t.Errorf("got:\n%s", got)
	}
	want = "apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nnamespace: okdp-releases\nresources: []\n"
	if got := Project(nil); string(got) != want {
		t.Errorf("none:\n%s", got)
	}
}

// TestFiles: the files of the helm target, then each instance's
// HelmRelease, waiting for the releases it depends on, of any namespace,
// and Kustomization, a project's, each marked generated.
func TestFiles(t *testing.T) {
	db := &okdp.Result{Namespace: "data", Release: "demo-db", Service: "db", Version: "1.0.0", Info: okdp.Info{Provides: []okdp.Output{{Name: "demo-db", Contract: "database"}}}}
	web := &okdp.Result{Namespace: "demo", Release: "demo-web", Service: "web", Version: "1.0.0", Dependencies: []okdp.Dependency{{Instance: "demo-db", Namespace: "data", Contract: "database", Field: "database", Output: "demo-db"}}}
	results := []*okdp.Result{web, db}
	g, err := graph.New(results)
	if err != nil {
		t.Fatal(err)
	}
	files, err := Target{PathPrefix: "deploy"}.Files(results, g)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{
		"projects/data/kustomization.yaml", "projects/data/instances/demo-db/helmrelease.yaml", "projects/data/instances/demo-db/kustomization.yaml",
		"projects/demo/kustomization.yaml", "projects/demo/instances/demo-web/helmrelease.yaml", "projects/demo/instances/demo-web/kustomization.yaml",
		"projects/demo/instances/demo-web/instance.values.yaml",
	} {
		if _, ok := files[p]; !ok {
			t.Errorf("%s is not written", p)
		}
	}
	for _, p := range []string{"projects/demo/instances/demo-web/helmrelease.yaml", "projects/demo/kustomization.yaml"} {
		if !strings.HasPrefix(string(files[p]), target.Generated) {
			t.Errorf("%s is not marked generated", p)
		}
	}
	rel := string(files["projects/demo/instances/demo-web/helmrelease.yaml"])
	if !strings.Contains(rel, "    - name: \"demo-db\"\n") || !strings.Contains(rel, `chart: "./deploy/projects/demo/instances/demo-web"`) {
		t.Errorf("the HelmRelease of demo-web:\n%s", rel)
	}
	if rel := string(files["projects/data/instances/demo-db/helmrelease.yaml"]); strings.Contains(rel, "dependsOn") {
		t.Errorf("the HelmRelease of demo-db:\n%s", rel)
	}
	if got := string(files["projects/demo/kustomization.yaml"]); !strings.HasSuffix(got, "resources:\n  - instances/demo-web\n") {
		t.Errorf("the project's Kustomization:\n%s", got)
	}
	// Two releases of one name in two namespaces.
	twice := []*okdp.Result{{Namespace: "a", Release: "x"}, {Namespace: "b", Release: "x"}}
	if _, err := (Target{}).Files(twice, g); err == nil || !strings.Contains(err.Error(), "the HelmReleases of okdp-releases need distinct names") {
		t.Errorf("twice: %v", err)
	}
	if _, err := (Target{Source: "x"}).Files(results, g); err == nil {
		t.Error("a wrong option is refused first")
	}
}
