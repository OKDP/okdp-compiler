package flux

import (
	"strings"
	"testing"

	"github.com/okdp/okdp-compiler/internal/graph"
	"github.com/okdp/okdp-compiler/internal/okdp"
	"github.com/okdp/okdp-compiler/internal/target"
)

// TestCheck: the options, the GitRepository of the tree, <namespace>/<name>
// (DefaultSource when empty), the path of the tree in it, and the
// namespaces of the projects kept.
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
		"projects kept":       {Target{Keep: []string{"demo", "data"}}, ""},
		"a kept path":         {Target{Keep: []string{"../x"}}, `Flux.Keep "../x": a namespace, a DNS label`},
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
// deploy/projects/demo/instances/demo-web, waiting for demo-db and
// demo-cache, installed as the project's ServiceAccount.
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
  serviceAccountName: "project-demo"
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
    createNamespace: false
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
// wait for, the releases in the order given, installed as the project's
// ServiceAccount unless privileged, and the Kustomization of the
// HelmRelease and of its values ConfigMap.
func TestInstance(t *testing.T) {
	tg := Target{Source: "flux-system/tree", PathPrefix: "deploy"}
	dir := "projects/demo/instances/demo-web"
	got := tg.Instance("demo", "demo-web", "deploy/"+dir, []string{"demo-db", "demo-cache"}, false)
	if string(got[HelmReleaseFile]) != hr {
		t.Errorf("with dependsOn:\n%s", got[HelmReleaseFile])
	}
	privileged := strings.Replace(hr, "  serviceAccountName: \"project-demo\"\n", "", 1)
	if got := tg.Instance("demo", "demo-web", "deploy/"+dir, []string{"demo-db", "demo-cache"}, true); string(got[HelmReleaseFile]) != privileged || strings.Contains(privileged, "serviceAccountName") {
		t.Errorf("privileged:\n%s", got[HelmReleaseFile])
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
	if got := tg.Instance("demo", "demo-web", "deploy/"+dir, nil, false); string(got[HelmReleaseFile]) != without || strings.Contains(without, "dependsOn") {
		t.Errorf("without dependsOn:\n%s", got[HelmReleaseFile])
	}
	if got := (Target{}).Instance("demo", "demo-web", dir, nil, false)[HelmReleaseFile]; !strings.Contains(string(got), "name: okdp-gitops\n        namespace: flux-system\n") {
		t.Errorf("the default source:\n%s", got)
	}
}

// TestProject: the Kustomization of a project, its instances' directories
// sorted, none an empty list; the root's, the projects' directories;
// neither sets a namespace.
func TestProject(t *testing.T) {
	want := "apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nresources:\n  - instances/a\n  - instances/b\n  - project\n"
	if got := Project([]string{"instances/b", "project", "instances/a"}); string(got) != want {
		t.Errorf("got:\n%s", got)
	}
	want = "apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nresources: []\n"
	if got := Project(nil); string(got) != want {
		t.Errorf("none:\n%s", got)
	}
	want = "apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nresources:\n  - data\n  - demo\n"
	if got := Projects([]string{"demo", "data"}); string(got) != want {
		t.Errorf("the root:\n%s", got)
	}
}

// serviceAccount: the ServiceAccountFile of project demo.
const serviceAccount = `apiVersion: v1
kind: ServiceAccount
metadata:
  name: "project-demo"
  namespace: okdp-releases
  labels:
    okdp.io/project: "demo"
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: okdp-project
  namespace: "demo"
  labels:
    okdp.io/project: "demo"
rules:
  - apiGroups: ["*"]
    resources: ["*"]
    verbs: ["*"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: okdp-project
  namespace: "demo"
  labels:
    okdp.io/project: "demo"
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: okdp-project
subjects:
  - kind: ServiceAccount
    name: "project-demo"
    namespace: okdp-releases
`

// TestProjectManifests: a project's manifests: its Namespace, its
// ServiceAccount, Role and RoleBinding, the platform's secret store when
// the platform names a provider, and their Kustomization, without a
// namespace; a privileged project has no ServiceAccount.
func TestProjectManifests(t *testing.T) {
	store := &okdp.SecretStore{Name: "platform", ServiceAccount: "okdp-secrets", Provider: map[string]any{"vault": map[string]any{"server": "https://vault.example.org"}}}
	got, err := ProjectManifests(target.Project{Namespace: "demo", SecretStore: store})
	if err != nil {
		t.Fatal(err)
	}
	if string(got[ServiceAccountFile]) != serviceAccount {
		t.Errorf("the ServiceAccount:\n%s", got[ServiceAccountFile])
	}
	if want := "apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nresources:\n  - namespace.yaml\n  - secret-store.yaml\n  - service-account.yaml\n"; string(got[KustomizationFile]) != want || len(got) != 4 {
		t.Errorf("the Kustomization:\n%s", got[KustomizationFile])
	}
	if ns := string(got[target.NamespaceFile]); ns != string(target.Namespace(target.Project{Namespace: "demo"})) {
		t.Errorf("the Namespace:\n%s", ns)
	}
	if s := string(got[target.SecretStoreFile]); !strings.Contains(s, "kind: SecretStore\n") || !strings.Contains(s, "    vault:\n      server: https://vault.example.org\n") {
		t.Errorf("the SecretStore:\n%s", s)
	}
	got, err = ProjectManifests(target.Project{Namespace: "data", Privileged: true})
	if err != nil {
		t.Fatal(err)
	}
	if want := "apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\nresources:\n  - namespace.yaml\n"; string(got[KustomizationFile]) != want || len(got) != 2 {
		t.Errorf("privileged, no store:\n%s", got[KustomizationFile])
	}
	if ns := string(got[target.NamespaceFile]); !strings.Contains(ns, "pod-security.kubernetes.io/enforce: privileged\n") {
		t.Errorf("the privileged Namespace:\n%s", ns)
	}
}

// TestFiles: the files of the helm target, then each instance's
// HelmRelease, waiting for the releases it depends on, of any namespace,
// and Kustomization, each project's manifests, a project's and the root's
// Kustomizations, each marked generated; the root's alone, without
// results.
func TestFiles(t *testing.T) {
	db := &okdp.Result{Namespace: "data", Release: "demo-db", Service: "db", Version: "1.0.0", Info: okdp.Info{Provides: []okdp.Output{{Name: "demo-db", Contract: "database"}}}, Privileged: true}
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
		"projects/data/project/namespace.yaml", "projects/data/project/kustomization.yaml",
		"projects/demo/kustomization.yaml", "projects/demo/instances/demo-web/helmrelease.yaml", "projects/demo/instances/demo-web/kustomization.yaml",
		"projects/demo/instances/demo-web/instance.values.yaml", "projects/kustomization.yaml",
		"projects/demo/project/namespace.yaml", "projects/demo/project/service-account.yaml", "projects/demo/project/kustomization.yaml",
	} {
		if _, ok := files[p]; !ok {
			t.Errorf("%s is not written", p)
		}
	}
	for _, p := range []string{"projects/data/project/service-account.yaml", "projects/demo/project/secret-store.yaml"} {
		if _, ok := files[p]; ok {
			t.Errorf("%s is written", p)
		}
	}
	for _, p := range []string{"projects/demo/instances/demo-web/helmrelease.yaml", "projects/demo/kustomization.yaml", "projects/kustomization.yaml", "projects/demo/project/namespace.yaml", "projects/demo/project/service-account.yaml", "projects/demo/project/kustomization.yaml"} {
		if !strings.HasPrefix(string(files[p]), target.Generated) {
			t.Errorf("%s is not marked generated", p)
		}
	}
	rel := string(files["projects/demo/instances/demo-web/helmrelease.yaml"])
	if !strings.Contains(rel, "    - name: \"demo-db\"\n") || !strings.Contains(rel, `chart: "./deploy/projects/demo/instances/demo-web"`) || !strings.Contains(rel, "  serviceAccountName: \"project-demo\"\n") {
		t.Errorf("the HelmRelease of demo-web:\n%s", rel)
	}
	if rel := string(files["projects/data/instances/demo-db/helmrelease.yaml"]); strings.Contains(rel, "dependsOn") || strings.Contains(rel, "serviceAccountName") {
		t.Errorf("the HelmRelease of demo-db:\n%s", rel)
	}
	if got := string(files["projects/demo/kustomization.yaml"]); !strings.HasSuffix(got, "resources:\n  - instances/demo-web\n  - project\n") {
		t.Errorf("the project's Kustomization:\n%s", got)
	}
	if got := string(files["projects/kustomization.yaml"]); !strings.HasSuffix(got, "resources:\n  - data\n  - demo\n") {
		t.Errorf("the root Kustomization:\n%s", got)
	}
	if none, err := (Target{}).Files(nil, nil); err != nil || len(none) != 1 || !strings.HasSuffix(string(none["projects/kustomization.yaml"]), "resources: []\n") {
		t.Errorf("no results: %v %q", err, none)
	}
	// Projects kept as the output has them: listed by the root, once, no
	// file of theirs written.
	kept, err := Target{Keep: []string{"other", "demo"}}.Files(results, g)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(kept["projects/kustomization.yaml"]); !strings.HasSuffix(got, "resources:\n  - data\n  - demo\n  - other\n") {
		t.Errorf("the root Kustomization, projects kept:\n%s", got)
	}
	for p := range kept {
		if strings.HasPrefix(p, "projects/other/") {
			t.Errorf("%s is written", p)
		}
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
