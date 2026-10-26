package target_test

import (
	"context"
	"flag"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
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

// compiled: three instances, compiled, and their graph: demo-web, which
// waits for demo-db, in namespace demo; data-db alone in data.
func compiled(t *testing.T) ([]*okdp.Result, *graph.Graph) {
	t.Helper()
	instance := func(pkg, release, namespace string, values map[string]any) okdp.Instance {
		def, err := okdp.LoadDefinition(packages[pkg])
		if err != nil {
			t.Fatal(err)
		}
		return okdp.Instance{Definition: def, Inputs: okdp.Inputs{Contracts: contracts, Values: []map[string]any{values}, Release: release, Namespace: namespace}}
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
