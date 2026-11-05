package argocd

import (
	"strings"
	"testing"

	"github.com/okdp/okdp-compiler/internal/graph"
	"github.com/okdp/okdp-compiler/internal/okdp"
	"github.com/okdp/okdp-compiler/internal/target"
)

// TestCheck: the options, the repository required, the values written
// between quotes free of a quote, a backslash and a newline, the path of
// the tree in it.
func TestCheck(t *testing.T) {
	const quoted = " holds a quote, a backslash or a newline"
	for name, tc := range map[string]struct {
		target Target
		want   string
	}{
		"a repository":       {Target{RepoURL: "https://x/tree.git"}, ""},
		"all of them":        {Target{RepoURL: "https://x/tree.git", Revision: "main", PathPrefix: "deploy"}, ""},
		"no repository":      {Target{}, "ArgoCD.RepoURL is required"},
		"a quoted URL":       {Target{RepoURL: `https://x"`}, `ArgoCD.RepoURL "https://x\"":` + quoted},
		"a backslash":        {Target{RepoURL: "https://x", Revision: `a\b`}, `ArgoCD.Revision "a\\b":` + quoted},
		"a newline":          {Target{RepoURL: "https://x", Revision: "a\nb"}, `ArgoCD.Revision "a\nb":` + quoted},
		"two at once":        {Target{RepoURL: `https://x"`, Revision: "a\nb", PathPrefix: "/a"}, "ArgoCD.RepoURL \"https://x\\\"\":" + quoted + "\nArgoCD.Revision \"a\\nb\":" + quoted + "\nArgoCD.PathPrefix \"/a\": the path of the tree in the repository, relative, without an empty, . or .. segment"},
		"an absolute prefix": {Target{RepoURL: "https://x", PathPrefix: "/deploy"}, `ArgoCD.PathPrefix "/deploy": the path of the tree in the repository, relative, without an empty, . or .. segment`},
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

// application: the Application of release demo-web of namespace demo, in
// deploy/projects/demo/instances/demo-web, of revision main, sync wave 2.
const application = `apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: "demo-web"
  namespace: argocd
  labels:
    okdp.io/project: "demo"
    okdp.io/instance: "demo-web"
  annotations:
    argocd.argoproj.io/sync-wave: "2"
    argocd.argoproj.io/compare-options: ServerSideDiff=true,IncludeMutationWebhook=true
  finalizers:
    - resources-finalizer.argocd.argoproj.io
spec:
  project: "project-demo"
  source:
    repoURL: "https://x/tree.git"
    targetRevision: "main"
    path: "deploy/projects/demo/instances/demo-web"
    helm:
      releaseName: "demo-web"
      valueFiles:
        - instance.values.yaml
  destination:
    server: https://kubernetes.default.svc
    namespace: "demo"
  ignoreDifferences:
    - group: admissionregistration.k8s.io
      kind: MutatingWebhookConfiguration
      jqPathExpressions: ['.webhooks[]?.clientConfig.caBundle']
    - group: admissionregistration.k8s.io
      kind: ValidatingWebhookConfiguration
      jqPathExpressions: ['.webhooks[]?.clientConfig.caBundle']
    - group: apiextensions.k8s.io
      kind: CustomResourceDefinition
      jsonPointers: [/spec/conversion/webhook/clientConfig/caBundle]
  syncPolicy:
    automated:
      prune: true
      selfHeal: true
    syncOptions:
      - RespectIgnoreDifferences=true
      - ServerSideApply=true
    retry:
      limit: -1
      backoff:
        duration: 10s
        factor: 2
        maxDuration: 5m
`

// TestApplication: the Application of a release, its revision and sync wave
// as given; the revision HEAD when none is.
func TestApplication(t *testing.T) {
	dir := "deploy/projects/demo/instances/demo-web"
	tg := Target{RepoURL: "https://x/tree.git", Revision: "main"}
	if got := tg.Application("demo", "demo-web", dir, 2); string(got) != application {
		t.Errorf("got:\n%s", got)
	}
	want := strings.NewReplacer(`targetRevision: "main"`, `targetRevision: "HEAD"`, `sync-wave: "2"`, `sync-wave: "0"`).Replace(application)
	if got := (Target{RepoURL: "https://x/tree.git"}).Application("demo", "demo-web", dir, 0); string(got) != want {
		t.Errorf("the defaults:\n%s", got)
	}
}

// appProject: the AppProject of project demo, confined to its namespace.
const appProject = `apiVersion: argoproj.io/v1alpha1
kind: AppProject
metadata:
  name: "project-demo"
  namespace: argocd
  labels:
    okdp.io/project: "demo"
spec:
  description: "OKDP project demo: its instances, confined to its namespace"
  sourceRepos:
    - "https://x/tree.git"
  destinations:
    - server: https://kubernetes.default.svc
      namespace: "demo"
  clusterResourceWhitelist: []
`

// privilegedProject: the AppProject of project data, one of the
// platform's own.
const privilegedProject = `apiVersion: argoproj.io/v1alpha1
kind: AppProject
metadata:
  name: "project-data"
  namespace: argocd
  labels:
    okdp.io/project: "data"
spec:
  description: "OKDP project data, one of the platform's own: its instances install cluster-scoped objects and write every namespace"
  sourceRepos:
    - "https://x/tree.git"
  destinations:
    - server: https://kubernetes.default.svc
      namespace: "*"
  clusterResourceWhitelist:
    - group: "*"
      kind: "*"
`

// TestProjectManifests: a project's manifests: its Namespace, kept when
// the project is gone, the flux target's, its AppProject, confined to its
// namespace unless privileged, and the platform's secret store when the
// platform names a provider.
func TestProjectManifests(t *testing.T) {
	tg := Target{RepoURL: "https://x/tree.git"}
	store := &okdp.SecretStore{Name: "platform", ServiceAccount: "okdp-secrets", Provider: map[string]any{"vault": map[string]any{"server": "https://vault.example.org"}}}
	got, err := tg.ProjectManifests(target.Project{Namespace: "demo", SecretStore: store})
	if err != nil {
		t.Fatal(err)
	}
	if string(got[AppProjectFile]) != appProject || len(got) != 3 {
		t.Errorf("the AppProject:\n%s", got[AppProjectFile])
	}
	if ns := string(got[target.NamespaceFile]); ns != string(target.Namespace(target.Project{Namespace: "demo"})) {
		t.Errorf("the Namespace:\n%s", ns)
	}
	if s := string(got[target.SecretStoreFile]); !strings.Contains(s, "kind: SecretStore\n") || !strings.Contains(s, "  name: platform\n  namespace: demo\n") {
		t.Errorf("the SecretStore:\n%s", s)
	}
	got, err = tg.ProjectManifests(target.Project{Namespace: "data", Privileged: true})
	if err != nil {
		t.Fatal(err)
	}
	if string(got[AppProjectFile]) != privilegedProject || len(got) != 2 {
		t.Errorf("the privileged AppProject:\n%s", got[AppProjectFile])
	}
}

// TestFiles: the files of the helm target, then each instance's
// Application, its sync wave after those of the releases it depends on,
// and each project's manifests, marked generated.
func TestFiles(t *testing.T) {
	db := &okdp.Result{Namespace: "data", Release: "demo-db", Service: "db", Version: "1.0.0", Info: okdp.Info{Provides: []okdp.Output{{Name: "demo-db", Contract: "database"}}}, Privileged: true}
	web := &okdp.Result{Namespace: "demo", Release: "demo-web", Service: "web", Version: "1.0.0", Dependencies: []okdp.Dependency{{Instance: "demo-db", Namespace: "data", Contract: "database", Field: "database", Output: "demo-db"}}}
	results := []*okdp.Result{web, db}
	g, err := graph.New(results)
	if err != nil {
		t.Fatal(err)
	}
	files, err := Target{RepoURL: "https://x/tree.git", PathPrefix: "deploy"}.Files(results, g)
	if err != nil {
		t.Fatal(err)
	}
	for p, wave := range map[string]string{"projects/data/instances/demo-db/application.yaml": "0", "projects/demo/instances/demo-web/application.yaml": "1"} {
		got := string(files[p])
		if !strings.HasPrefix(got, target.Generated) || !strings.Contains(got, `sync-wave: "`+wave+`"`) {
			t.Errorf("%s:\n%s", p, got)
		}
	}
	if got := string(files["projects/demo/instances/demo-web/application.yaml"]); !strings.Contains(got, `path: "deploy/projects/demo/instances/demo-web"`) {
		t.Errorf("the path of the Application:\n%s", got)
	}
	if _, ok := files["projects/demo/instances/demo-web/instance.values.yaml"]; !ok {
		t.Error("the helm target's files are not written")
	}
	for p, want := range map[string]string{"projects/demo/project/namespace.yaml": "enforce: baseline", "projects/demo/project/appproject.yaml": `namespace: "demo"`, "projects/data/project/namespace.yaml": "enforce: privileged", "projects/data/project/appproject.yaml": `namespace: "*"`} {
		got := string(files[p])
		if !strings.HasPrefix(got, target.Generated) || !strings.Contains(got, want) {
			t.Errorf("%s:\n%s", p, got)
		}
	}
	if _, ok := files["projects/demo/project/secret-store.yaml"]; ok {
		t.Error("a secret store without a provider")
	}
	twice := []*okdp.Result{{Namespace: "a", Release: "x"}, {Namespace: "b", Release: "x"}}
	if _, err := (Target{RepoURL: "https://x"}).Files(twice, g); err == nil || !strings.Contains(err.Error(), "the Applications of argocd need distinct names") {
		t.Errorf("twice: %v", err)
	}
	if _, err := (Target{}).Files(results, g); err == nil {
		t.Error("a wrong option is refused first")
	}
}
