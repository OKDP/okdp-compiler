package target_test

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/okdp/okdp-compiler/internal/graph"
	"github.com/okdp/okdp-compiler/internal/okdp"
	"github.com/okdp/okdp-compiler/internal/target"
	"github.com/okdp/okdp-compiler/internal/target/argocd"
	"github.com/okdp/okdp-compiler/internal/target/flux"
	"github.com/okdp/okdp-compiler/internal/target/helm"
)

var update = flag.Bool("update", false, "rewrite testdata/targets/ from the current targets")

// contracts: database.
var contracts = fstest.MapFS{"database.schema.json": {Data: []byte(`{
	"x-okdp-contract": "database",
	"properties": {"host": {"type": "string"}},
	"required": ["host"]}`)}}

// packages: db, which provides a database, and web, which references one.
var packages = map[string]fstest.MapFS{
	"db": {
		"Chart.yaml": {Data: []byte("apiVersion: v2\nname: db\nversion: 1.0.0\ndependencies:\n  - {name: postgresql, repository: https://charts.example.org, version: 1.0.0}\n")},
		"okdp.yaml":  {Data: []byte("provides:\n  - {contract: database, values: {host: '${{ release.name }}-db.${{ release.namespace }}.svc'}}\n")},
	},
	"web": {
		"Chart.yaml":         {Data: []byte("apiVersion: v2\nname: web\nversion: 1.0.0\ndependencies:\n  - {name: app, repository: https://charts.example.org, version: 2.0.0}\n")},
		"okdp.yaml":          {Data: []byte("instance: {usage: a web application}\n")},
		"config.schema.json": {Data: []byte(`{"properties": {"database": {"type": "string", "x-okdp-connection-ref": {"contract": "database"}}}}`)},
		"values.cel.yaml":    {Data: []byte("app:\n  database: ${{ values.database.host }}\n")},
	},
}

// platform: the platform's values: data is one of its own projects, and
// its secret store has a provider, a SecretStore of each project.
var platform = map[string]any{"global": map[string]any{"okdp": map[string]any{
	"policy":  map[string]any{"privileged": []any{"data"}},
	"secrets": map[string]any{"store": "platform", "provider": map[string]any{"vault": map[string]any{"server": "https://vault.example.org", "path": "okdp", "auth": map[string]any{"kubernetes": map[string]any{"mountPath": "kubernetes", "role": "okdp", "serviceAccountRef": map[string]any{"name": "okdp-secrets"}}}}}},
}}}

// compiled: three instances, compiled with platform, and their graph:
// demo-web, which waits for demo-db, in namespace demo; data-db alone in
// data, privileged.
func compiled(t *testing.T) ([]*okdp.Result, *graph.Graph) {
	t.Helper()
	instance := func(pkg, release, namespace string, values map[string]any) okdp.Instance {
		def, err := okdp.LoadDefinition(packages[pkg])
		if err != nil {
			t.Fatal(err)
		}
		return okdp.Instance{Definition: def, Inputs: okdp.Inputs{Contracts: contracts, Context: []okdp.ContextLayer{{Values: platform}}, Values: []map[string]any{values}, Release: release, Namespace: namespace}}
	}
	var results []*okdp.Result
	for _, o := range okdp.CompileAll(context.Background(), []okdp.Instance{
		instance("web", "demo-web", "demo", map[string]any{"database": "demo-db"}),
		instance("db", "demo-db", "demo", nil),
		instance("db", "data-db", "data", nil),
	}) {
		if o.Err != nil {
			t.Fatal(o.Err)
		}
		results = append(results, o.Result)
	}
	g, err := graph.New(results)
	if err != nil {
		t.Fatal(err)
	}
	return results, g
}

// TestTargets: the files of each target, those of testdata/targets/<name>/,
// every one (go test -update rewrites them).
func TestTargets(t *testing.T) {
	results, g := compiled(t)
	for name, tg := range map[string]target.Target{
		"helm":   helm.Target{},
		"flux":   flux.Target{PathPrefix: "deploy"},
		"argocd": argocd.Target{RepoURL: "https://git.example.org/okdp/deployments.git", Revision: "main"},
	} {
		t.Run(name, func(t *testing.T) {
			files, err := tg.Files(results, g)
			if err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join("..", "..", "testdata", "targets", name)
			if *update {
				if err := os.RemoveAll(dir); err != nil {
					t.Fatal(err)
				}
				for p, data := range files {
					if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(p)), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(dir, p), data, 0o644); err != nil {
						t.Fatal(err)
					}
				}
			}
			want := map[string]string{}
			err = fs.WalkDir(os.DirFS(dir), ".", func(p string, d fs.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return err
				}
				data, err := os.ReadFile(filepath.Join(dir, p))
				want[p] = string(data)
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			if got := slices.Sorted(maps.Keys(files)); !slices.Equal(got, slices.Sorted(maps.Keys(want))) {
				t.Fatalf("files %v, want those of %s (go test -update)", got, dir)
			}
			for p, data := range files {
				if string(data) != want[p] {
					t.Errorf("%s differs from %s (go test -update):\n%s", p, filepath.Join(dir, p), data)
				}
			}
		})
	}
}

// TestOptions: each target refuses its wrong options, and two releases of
// one name in two namespaces, whose objects would share one.
func TestOptions(t *testing.T) {
	results, g := compiled(t)
	twice := []*okdp.Result{results[1], {Release: "demo-db", Namespace: "data"}}
	for name, tc := range map[string]struct {
		target  target.Target
		results []*okdp.Result
		want    string
	}{
		"flux source":     {flux.Target{Source: "okdp"}, results, `Flux.Source "okdp": the GitRepository of the tree, <namespace>/<name>`},
		"flux prefix":     {flux.Target{PathPrefix: "/deploy"}, results, `Flux.PathPrefix "/deploy": the path of the tree in the repository, relative, without an empty, . or .. segment`},
		"flux dot":        {flux.Target{PathPrefix: "deploy/../x"}, results, `Flux.PathPrefix "deploy/../x": the path of the tree in the repository, relative, without an empty, . or .. segment`},
		"flux releases":   {flux.Target{}, twice, `release "demo-db" is in namespaces "data" and "demo": the HelmReleases of okdp-releases need distinct names`},
		"argocd url":      {argocd.Target{}, results, `ArgoCD.RepoURL is required`},
		"argocd quote":    {argocd.Target{RepoURL: `https://x"`, Revision: "a\nb"}, results, "ArgoCD.RepoURL \"https://x\\\"\": holds a quote, a backslash or a newline\nArgoCD.Revision \"a\\nb\": holds a quote, a backslash or a newline"},
		"argocd releases": {argocd.Target{RepoURL: "https://x"}, twice, `release "demo-db" is in namespaces "data" and "demo": the Applications of argocd need distinct names`},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := tc.target.Files(tc.results, g); err == nil || err.Error() != tc.want {
				t.Errorf("got %v\nwant %s", err, tc.want)
			}
		})
	}
}

// TestProjects: a project per namespace of the results, in namespace
// order, privileged when any of its results is, its secret store the
// first set.
func TestProjects(t *testing.T) {
	store := &okdp.SecretStore{Name: "platform", ServiceAccount: "okdp-secrets", Provider: map[string]any{"fake": map[string]any{}}}
	got := target.Projects([]*okdp.Result{
		{Namespace: "demo", Release: "demo-web"},
		{Namespace: "data", Release: "data-db", Privileged: true, SecretStore: store},
		{Namespace: "demo", Release: "demo-db", Privileged: true, SecretStore: store},
	})
	want := []target.Project{{Namespace: "data", Privileged: true, SecretStore: store}, {Namespace: "demo", Privileged: true, SecretStore: store}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if got := target.Projects(nil); got != nil {
		t.Errorf("no result: %v", got)
	}
}

// TestNamespace: the Namespace of a project: its Pod Security Admission
// labels, baseline enforced unless privileged, and kept by both engines
// when the project is gone.
func TestNamespace(t *testing.T) {
	want := `apiVersion: v1
kind: Namespace
metadata:
  name: "demo"
  labels:
    okdp.io/project: "demo"
    kustomize.toolkit.fluxcd.io/prune: disabled
    pod-security.kubernetes.io/enforce: baseline
    pod-security.kubernetes.io/audit: restricted
    pod-security.kubernetes.io/warn: restricted
  annotations:
    argocd.argoproj.io/sync-options: Delete=false
`
	if got := target.Namespace(target.Project{Namespace: "demo"}); string(got) != want {
		t.Errorf("got:\n%s", got)
	}
	want = `apiVersion: v1
kind: Namespace
metadata:
  name: "data"
  labels:
    okdp.io/project: "data"
    kustomize.toolkit.fluxcd.io/prune: disabled
    pod-security.kubernetes.io/enforce: privileged
  annotations:
    argocd.argoproj.io/sync-options: Delete=false
`
	if got := target.Namespace(target.Project{Namespace: "data", Privileged: true}); string(got) != want {
		t.Errorf("privileged:\n%s", got)
	}
}

// TestOneTree: the files both deployer targets write are the same, those
// of the umbrella charts and of the projects' manifests (the Namespace,
// the secret store): one tree holds the files of both; each writes files
// of its own beside them.
func TestOneTree(t *testing.T) {
	results, g := compiled(t)
	fluxFiles, err := flux.Target{}.Files(results, g)
	if err != nil {
		t.Fatal(err)
	}
	argoFiles, err := argocd.Target{RepoURL: "https://git.example.org/okdp/deployments.git"}.Files(results, g)
	if err != nil {
		t.Fatal(err)
	}
	shared := 0
	for p, b := range fluxFiles {
		if a, ok := argoFiles[p]; ok {
			shared++
			if !bytes.Equal(a, b) {
				t.Errorf("%s: the targets write it differently", p)
			}
		}
	}
	for _, p := range []string{"projects/demo/project/namespace.yaml", "projects/demo/project/secret-store.yaml", "projects/data/project/namespace.yaml"} {
		if _, ok := fluxFiles[p]; !ok {
			t.Errorf("%s is not written by both", p)
		}
	}
	if shared == 0 {
		t.Error("no file in common")
	}
}

// TestSecretStore: the platform's secret store in a project's namespace,
// its ServiceAccount without a mounted token and the SecretStore, its
// provider as the platform writes it; nil without a provider.
func TestSecretStore(t *testing.T) {
	want := `apiVersion: v1
kind: ServiceAccount
metadata:
  name: okdp-secrets
  namespace: demo
  labels:
    okdp.io/project: demo
automountServiceAccountToken: false
---
apiVersion: external-secrets.io/v1
kind: SecretStore
metadata:
  name: platform
  namespace: demo
  labels:
    okdp.io/project: demo
spec:
  provider:
    vault:
      auth:
        kubernetes:
          role: 'okdp: x'
      server: https://vault.example.org
`
	store := &okdp.SecretStore{Name: "platform", ServiceAccount: "okdp-secrets", Provider: map[string]any{"vault": map[string]any{"server": "https://vault.example.org", "auth": map[string]any{"kubernetes": map[string]any{"role": "okdp: x"}}}}}
	got, err := target.SecretStore(target.Project{Namespace: "demo", SecretStore: store})
	if err != nil || string(got) != want {
		t.Errorf("got %v:\n%s", err, got)
	}
	if got, err := target.SecretStore(target.Project{Namespace: "demo"}); got != nil || err != nil {
		t.Errorf("no provider: %q, %v", got, err)
	}
}

// TestReleases: a *ReleaseError per release in two namespaces or more,
// which each deployer target wraps.
func TestReleases(t *testing.T) {
	results := []*okdp.Result{{Release: "demo-db", Namespace: "demo"}, {Release: "demo-db", Namespace: "data"}, {Release: "a-b", Namespace: "a"}, {Release: "a-b", Namespace: "x"}, {Release: "a-b", Namespace: "b"}, {Release: "one", Namespace: "a"}}
	err := target.Releases(results)
	var got []target.ReleaseError
	for _, e := range err.(interface{ Unwrap() []error }).Unwrap() {
		var re *target.ReleaseError
		if !errors.As(e, &re) {
			t.Fatalf("%v: not a *ReleaseError", e)
		}
		got = append(got, *re)
	}
	want := []target.ReleaseError{{Release: "a-b", Namespaces: []string{"a", "b", "x"}}, {Release: "demo-db", Namespaces: []string{"data", "demo"}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if want := `release "a-b" is in namespaces "a", "b" and "x"`; got[0].Error() != want {
		t.Errorf("got %q, want %q", got[0].Error(), want)
	}
	if err := target.Releases(results[2:3]); err != nil {
		t.Errorf("one release: %v", err)
	}
	_, err = (flux.Target{}).Files(results[:2], nil)
	if re := (*target.ReleaseError)(nil); !errors.As(err, &re) || re.Release != "demo-db" {
		t.Errorf("flux: got %v, want a *ReleaseError", err)
	}
}

// TestOwnNamespaces: a project of a namespace a target writes its own
// objects in is refused, by the flux target (okdp-releases, its Source's
// namespace) and the argocd target (argocd), one error per namespace;
// the namespaces the targets write in by default are those the compile
// reserves (okdp.EngineNamespaces), so that the compile refuses them
// first.
func TestOwnNamespaces(t *testing.T) {
	g, err := graph.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		target interface {
			Files([]*okdp.Result, *graph.Graph) (map[string][]byte, error)
		}
		results []*okdp.Result
		want    string
	}{
		"flux releases": {flux.Target{}, []*okdp.Result{{Namespace: flux.ReleasesNamespace, Release: "okdp-releases-x"}}, `project "okdp-releases": the namespace is reserved: it holds the HelmReleases of okdp-releases and the identities of the projects, no project's`},
		"flux source":   {flux.Target{Source: "gitops/tree"}, []*okdp.Result{{Namespace: "gitops", Release: "gitops-x"}, {Namespace: "demo", Release: "demo-x"}}, `project "gitops": the namespace is reserved: it holds the GitRepository of the tree (Flux.Source), no project's`},
		"argocd":        {argocd.Target{RepoURL: "https://x"}, []*okdp.Result{{Namespace: argocd.Namespace, Release: "argocd-x"}}, `project "argocd": the namespace is reserved: it holds the Applications of argocd and the AppProjects, no project's`},
	} {
		if _, err := tc.target.Files(tc.results, g); err == nil || err.Error() != tc.want {
			t.Errorf("%s: got %v\nwant %s", name, err, tc.want)
		}
	}
	source, _, _ := strings.Cut(flux.DefaultSource, "/")
	for _, ns := range []string{flux.ReleasesNamespace, source, argocd.Namespace} {
		if !slices.Contains(okdp.EngineNamespaces(), ns) {
			t.Errorf("%s: a target's namespace the compile does not reserve (okdp.EngineNamespaces)", ns)
		}
	}
}
