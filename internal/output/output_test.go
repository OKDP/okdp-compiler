package output

import (
	"errors"
	"maps"
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
	"github.com/okdp/okdp-compiler/internal/tree"
)

// compiled: a tree of instances, each <namespace>/<name>, its release
// <namespace>-<name>, compiled but those of refused.
func compiled(names []string, refused ...string) *tree.Tree {
	t := &tree.Tree{}
	for _, n := range names {
		ns, name, _ := strings.Cut(n, "/")
		ti := &tree.Instance{Name: name, Inputs: okdp.Inputs{Namespace: ns, Release: ns + "-" + name}}
		if slices.Contains(refused, n) {
			ti.Outcome.Err = errors.New("refused")
		} else {
			ti.Outcome.Result = &okdp.Result{Namespace: ns, Release: ns + "-" + name}
		}
		t.Instances = append(t.Instances, ti)
	}
	return t
}

// file: a file of an output.
func file(s string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(s)} }

// TestFiles: the projects written by every target, merged; a project
// kept as the previous output holds it, its manifests and its
// HelmReleases of today's settings, listed in the root Kustomization
// when it holds its kustomization.yaml; none of it without a previous
// output.
func TestFiles(t *testing.T) {
	tr := compiled([]string{"data/db", "demo/ok", "demo/web", "lab/x"}, "demo/web", "lab/x")
	previous := fstest.MapFS{
		"projects/demo/kustomization.yaml":                 file("old demo\n"),
		"projects/demo/instances/demo-ok/Chart.yaml":       file("old ok\n"),
		"projects/demo/instances/demo-ok/helmrelease.yaml": file("spec:\n  dependsOn:\n    - name: demo-web\n"),
		"projects/demo/project/stale.yaml":                 file("old\n"),
		"projects/data/instances/data-db/Chart.yaml":       file("old db\n"),
		"projects/lab/instances/lab-x/Chart.yaml":          file("old x\n"),
	}
	files, kept, err := Files(tr, []target.Target{flux.Target{}, argocd.Target{RepoURL: "https://git.example.org/out.git"}}, previous)
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 2 || kept[0].Namespace != "demo" || kept[1].Namespace != "lab" {
		t.Fatalf("kept: %+v", kept)
	}
	for p, want := range map[string]string{
		"projects/demo/kustomization.yaml":           "old demo\n",
		"projects/demo/instances/demo-ok/Chart.yaml": "old ok\n",
		"projects/lab/instances/lab-x/Chart.yaml":    "old x\n",
	} {
		if got := string(files[p]); got != want {
			t.Errorf("%s: %q, want %q", p, got, want)
		}
	}
	hr := string(files["projects/demo/instances/demo-ok/helmrelease.yaml"])
	if !strings.HasPrefix(hr, target.Generated) || !strings.Contains(hr, `serviceAccountName: "project-demo"`) || !strings.Contains(hr, "dependsOn:\n    - name: \"demo-web\"\n") {
		t.Errorf("the kept HelmRelease, of today's identity, its dependsOn kept:\n%s", hr)
	}
	for _, p := range []string{"projects/demo/project/namespace.yaml", "projects/demo/project/service-account.yaml", "projects/demo/project/appproject.yaml", "projects/demo/project/kustomization.yaml", "projects/demo/project/access.yaml"} {
		if _, ok := files[p]; !ok {
			t.Errorf("%s not re-derived", p)
		}
	}
	if _, ok := files["projects/demo/project/stale.yaml"]; ok {
		t.Error("a kept project's manifest of before kept")
	}
	if _, ok := files["projects/lab/project/namespace.yaml"]; !ok {
		t.Error("lab's manifests not re-derived")
	}
	for _, p := range []string{"projects/data/instances/data-db/helmrelease.yaml", "projects/data/instances/data-db/application.yaml"} {
		if _, ok := files[p]; !ok {
			t.Errorf("%s not written", p)
		}
	}
	if string(files["projects/data/instances/data-db/Chart.yaml"]) == "old db\n" {
		t.Error("a project written kept its previous output")
	}
	if _, ok := files["projects/demo/instances/demo-ok/application.yaml"]; ok {
		t.Error("a kept project written")
	}
	if root := string(files["projects/kustomization.yaml"]); !strings.HasSuffix(root, "resources:\n  - data\n  - demo\n") {
		t.Errorf("root, lab without its kustomization.yaml left out:\n%s", root)
	}

	files, _, err = Files(tr, []target.Target{&flux.Target{}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if root := string(files["projects/kustomization.yaml"]); !strings.HasSuffix(root, "resources:\n  - data\n") {
		t.Errorf("root without a previous output:\n%s", root)
	}
	if slices.ContainsFunc(slices.Collect(maps.Keys(files)), func(p string) bool { return strings.HasPrefix(p, "projects/demo/") }) {
		t.Error("a kept project without a previous output has files")
	}
}

// TestFilesReleases: a release of a project written in the output a kept
// project keeps, or in two projects of the tree, fails the Flux and Argo
// CD targets, whose objects share a namespace; not the helm target.
func TestFilesReleases(t *testing.T) {
	tr := compiled([]string{"data/db", "demo/web"}, "demo/web")
	previous := fstest.MapFS{"projects/demo/instances/data-db/Chart.yaml": file("x\n")}
	for _, c := range []struct {
		target target.Target
		want   string
	}{
		{flux.Target{}, `release "data-db" is in namespaces "data" and "demo": the HelmReleases of okdp-releases need distinct names`},
		{argocd.Target{RepoURL: "https://x"}, `release "data-db" is in namespaces "data" and "demo": the Applications of argocd need distinct names`},
	} {
		_, _, err := Files(tr, []target.Target{c.target}, previous)
		if re, ok := errors.AsType[*target.ReleaseError](err); !ok || err.Error() != c.want || re.Release != "data-db" {
			t.Errorf("%T: %v", c.target, err)
		}
	}
	if _, _, err := Files(tr, []target.Target{helm.Target{}}, previous); err != nil {
		t.Errorf("helm: %v", err)
	}
}

// TestAReleaseCollisionKeepsItsProjects: a release name two projects of
// the tree share keeps both, and those that depend on a release of
// them, the rest written: each kept as the previous output holds it,
// nothing of one that it does not hold; refused, the other kept all the
// same; the output of both kept holding it fails; not the helm target,
// whose files are by namespace.
func TestAReleaseCollisionKeepsItsProjects(t *testing.T) {
	tr := compiled([]string{"data/pg", "demo/app-x", "demo/ok", "demo-app/x", "web/w"})
	tr.Instance("demo-app", "x").Outcome.Result.Info.Provides = []okdp.Output{{Contract: "database", Name: "db"}}
	w := tr.Instance("web", "w").Outcome.Result
	w.Dependencies = []okdp.Dependency{{Namespace: "demo-app", Instance: "demo-app-x", Contract: "database", Field: "db", Output: "db"}}
	previous := fstest.MapFS{
		"projects/demo/kustomization.yaml":              file("old demo\n"),
		"projects/demo/instances/demo-app-x/Chart.yaml": file("old app-x\n"),
		"projects/web/instances/web-w/Chart.yaml":       file("old w\n"),
	}
	files, kept, err := Files(tr, []target.Target{flux.Target{}, argocd.Target{RepoURL: "https://git.example.org/out.git"}}, previous)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, k := range kept {
		names = append(names, k.Namespace)
	}
	if !slices.Equal(names, []string{"demo", "demo-app", "web"}) {
		t.Fatalf("kept: %v", names)
	}
	want := `release "demo-app-x" is in namespaces "demo" and "demo-app": the HelmReleases of okdp-releases and the Applications of argocd need distinct names`
	for _, k := range kept[:2] {
		if len(k.Instances) != 1 || k.Instances[0].Release() != "demo-app-x" || k.Instances[0].Depends {
			t.Fatalf("%s: %+v", k.Namespace, k.Instances)
		}
		err := k.Instances[0].Err
		if re, ok := errors.AsType[*target.ReleaseError](err); !ok || re.Release != "demo-app-x" || err.Error() != want {
			t.Errorf("%s: %v", k.Namespace, err)
		}
		if p := k.Namespace + "/" + k.Instances[0].Name + ": " + want; !slices.Equal(k.Problems, []string{p}) {
			t.Errorf("%s: %q", k.Namespace, k.Problems)
		}
	}
	if ki := kept[2].Instances; len(ki) != 1 || !ki[0].Depends || ki[0].Name != "w" {
		t.Errorf("web: %+v", ki)
	}
	for _, p := range []string{"projects/data/instances/data-pg/helmrelease.yaml", "projects/data/instances/data-pg/application.yaml"} {
		if _, ok := files[p]; !ok {
			t.Errorf("%s not written", p)
		}
	}
	for p, want := range map[string]string{
		"projects/demo/kustomization.yaml":              "old demo\n",
		"projects/demo/instances/demo-app-x/Chart.yaml": "old app-x\n",
		"projects/web/instances/web-w/Chart.yaml":       "old w\n",
	} {
		if got := string(files[p]); got != want {
			t.Errorf("%s: %q, want %q", p, got, want)
		}
	}
	for p := range files {
		if strings.HasPrefix(p, "projects/demo-app/") || strings.HasPrefix(p, "projects/demo/instances/demo-ok/") {
			t.Errorf("%s: written", p)
		}
	}
	if root := string(files["projects/kustomization.yaml"]); !strings.HasSuffix(root, "resources:\n  - data\n  - demo\n") {
		t.Errorf("root:\n%s", root)
	}

	// demo/app-x refused: demo kept for it, demo-app for the release.
	refused := compiled([]string{"data/pg", "demo/app-x", "demo-app/x"}, "demo/app-x")
	_, kept, err = Files(refused, []target.Target{flux.Target{}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 2 || kept[0].Instances[0].Err.Error() != "refused" {
		t.Fatalf("kept: %+v", kept)
	}
	if _, ok := errors.AsType[*target.ReleaseError](kept[1].Instances[0].Err); !ok || kept[1].Namespace != "demo-app" {
		t.Errorf("demo-app: %+v", kept[1])
	}

	// The output of both kept holds the release: it would be written twice.
	previous["projects/demo-app/instances/demo-app-x/Chart.yaml"] = file("old x\n")
	if _, _, err := Files(tr, []target.Target{flux.Target{}}, previous); err == nil || err.Error() != `release "demo-app-x" is in namespaces "demo" and "demo-app": the HelmReleases of okdp-releases need distinct names` {
		t.Errorf("two kept outputs: %v", err)
	}

	files, kept, err = Files(tr, []target.Target{helm.Target{}}, nil)
	if err != nil || len(kept) != 0 {
		t.Fatalf("helm: %v %+v", err, kept)
	}
	for _, p := range []string{"projects/demo/instances/demo-app-x/Chart.yaml", "projects/demo-app/instances/demo-app-x/Chart.yaml"} {
		if _, ok := files[p]; !ok {
			t.Errorf("helm: %s not written", p)
		}
	}
}

// differs: a target that writes one file, of its content.
type differs string

func (d differs) Files([]*okdp.Result, *graph.Graph) (map[string][]byte, error) {
	return map[string][]byte{"projects/x": []byte(d)}, nil
}

// TestFilesMerge: a path two targets write alike is written once; written
// differently, it fails.
func TestFilesMerge(t *testing.T) {
	tr := compiled([]string{"demo/a"})
	if files, _, err := Files(tr, []target.Target{differs("a"), differs("a")}, nil); err != nil || string(files["projects/x"]) != "a" {
		t.Errorf("alike: %v %v", files, err)
	}
	if _, _, err := Files(tr, []target.Target{differs("a"), differs("b")}, nil); err == nil || err.Error() != "projects/x: two targets write it differently" {
		t.Errorf("differently: %v", err)
	}
}

// TestReleaseTaken: the instance of another namespace whose release an
// instance has too; none for one of its own, or of a release of its own.
func TestReleaseTaken(t *testing.T) {
	tr := compiled([]string{"a-b/c", "a/b-c", "a/d"})
	if got := ReleaseTaken(tr, "a", "b-c"); got == nil || got.Namespace() != "a-b" || got.Name != "c" {
		t.Errorf("a/b-c: %v", got)
	}
	if got := ReleaseTaken(tr, "a-b", "c"); got == nil || got.Namespace() != "a" || got.Name != "b-c" {
		t.Errorf("a-b/c: %v", got)
	}
	if got := ReleaseTaken(tr, "a", "d"); got != nil {
		t.Errorf("a/d: %v", got)
	}
	if got := ReleaseTaken(tr, "a", "nowhere"); got != nil {
		t.Errorf("no instance: %v", got)
	}
}

// TestFilesRederive: a project kept is written of today's settings: the
// files re-derived listed (Kept.Rederived); an access refused gives no
// group a role, the platform's settings refused fail it; with no target
// that rewrites it (the helm target), its output as it is.
func TestFilesRederive(t *testing.T) {
	tr := compiled([]string{"demo/web"}, "demo/web")
	previous := fstest.MapFS{
		"projects/demo/kustomization.yaml":            file("old demo\n"),
		"projects/demo/project/access.yaml":           file("old access\n"),
		"projects/demo/instances/demo-web/Chart.yaml": file("old web\n"),
	}
	tr.Instances[0].Inputs.Context = []okdp.ContextLayer{{Level: 1, Values: map[string]any{"access": map[string]any{"a b": "viewer"}}}}
	files, kept, err := Files(tr, []target.Target{flux.Target{}}, previous)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := files["projects/demo/project/access.yaml"]; ok {
		t.Error("an access refused: a group has a role")
	}
	want := []string{"projects/demo/project/kustomization.yaml", "projects/demo/project/namespace.yaml", "projects/demo/project/service-account.yaml"}
	if len(kept) != 1 || !slices.Equal(kept[0].Rederived, want) {
		t.Errorf("Rederived: %+v", kept)
	}
	files, kept, err = Files(tr, []target.Target{helm.Target{}}, previous)
	if err != nil || string(files["projects/demo/project/access.yaml"]) != "old access\n" || kept[0].Rederived != nil {
		t.Errorf("helm: %v %+v", err, kept)
	}
	tr.Instances[0].Inputs.Context = []okdp.ContextLayer{{Values: map[string]any{"global": map[string]any{"okdp": map[string]any{"policy": "x"}}}}}
	if _, _, err := Files(tr, []target.Target{flux.Target{}}, previous); err == nil || !strings.Contains(err.Error(), "the project kept demo: its manifests of today's settings") {
		t.Errorf("the platform's settings refused: %v", err)
	}
}

// TestFilesUnloaded: a project that does not load (Tree.Refused) is kept
// as the previous output holds it, its manifests of today's settings when
// its project.yaml reads (its context, no instance), as they were when it
// does not; a context the platform refuses fails not the tree; a project
// that is not in the output is none.
func TestFilesUnloaded(t *testing.T) {
	tr := compiled([]string{"data/db"})
	tr.Refused = []tree.Refused{
		{Namespace: "demo", Problems: []string{"db/projects/demo/instances/x/values.yaml: not a file"}, Inputs: okdp.Inputs{Namespace: "demo"}, Own: true},
		{Namespace: "lost", Problems: []string{"db/projects/lost/project.yaml: parse"}, Inputs: okdp.Inputs{Namespace: "lost"}},
		{Namespace: "new", Problems: []string{"db/projects/new/instances/x/values.yaml: not a file"}, Inputs: okdp.Inputs{Namespace: "new"}, Own: true},
	}
	previous := fstest.MapFS{
		"projects/demo/kustomization.yaml":            file("old demo\n"),
		"projects/demo/project/access.yaml":           file("old access\n"),
		"projects/demo/instances/demo-web/Chart.yaml": file("old web\n"),
		"projects/lost/instances/lost-web/Chart.yaml": file("old lost\n"),
	}
	files, kept, err := Files(tr, []target.Target{flux.Target{}}, previous)
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 3 || !kept[0].Unloaded || kept[0].Namespace != "demo" || kept[0].Instances != nil || kept[0].Problems[0] != tr.Refused[0].Problems[0] {
		t.Fatalf("kept: %+v", kept)
	}
	if string(files["projects/demo/instances/demo-web/Chart.yaml"]) != "old web\n" || files["projects/demo/project/namespace.yaml"] == nil || string(files["projects/demo/project/access.yaml"]) == "old access\n" {
		t.Errorf("demo: %v", slices.Sorted(maps.Keys(files)))
	}
	if string(files["projects/lost/instances/lost-web/Chart.yaml"]) != "old lost\n" || kept[1].Rederived != nil {
		t.Errorf("lost, no context: %v %+v", slices.Sorted(maps.Keys(files)), kept[1])
	}
	if _, ok := files["projects/new/project/namespace.yaml"]; ok {
		t.Error("a project the output does not hold is written")
	}
	// A context the platform's settings refuse: its output kept as it was.
	tr.Refused[0].Inputs.Context = []okdp.ContextLayer{{Values: map[string]any{"global": map[string]any{"okdp": map[string]any{"policy": "x"}}}}}
	files, _, err = Files(tr, []target.Target{flux.Target{}}, previous)
	if err != nil || string(files["projects/demo/project/access.yaml"]) != "old access\n" {
		t.Errorf("settings refused: %v", err)
	}
}

// TestFilesDropped: a project kept whose namespace the compiler or the
// platform reserves now is dropped, whether it loads or not and whatever
// the target: nothing of it kept, written nor listed in the root
// Kustomization or among the releases, Kept.Dropped says why, the others
// kept as ever; one the output does not hold is not dropped.
func TestFilesDropped(t *testing.T) {
	reserved := []okdp.ContextLayer{{Values: map[string]any{"global": map[string]any{"okdp": map[string]any{"policy": map[string]any{"reserved": []any{"openbao"}}}}}}}
	tr := compiled([]string{"openbao/web", "kube-system/web", "demo/web", "fresh/web"}, "openbao/web", "kube-system/web", "demo/web", "fresh/web")
	for _, ti := range tr.Instances {
		ti.Inputs.Context = reserved
	}
	tr.Refused = []tree.Refused{{Namespace: "loose", Problems: []string{"x"}, Inputs: okdp.Inputs{Namespace: "loose", Context: reserved}, Own: true}}
	previous := fstest.MapFS{}
	for _, ns := range []string{"openbao", "kube-system", "demo"} {
		previous["projects/"+ns+"/kustomization.yaml"] = file("old " + ns + "\n")
		previous["projects/"+ns+"/instances/"+ns+"-web/Chart.yaml"] = file("old web\n")
	}
	for _, tg := range []target.Target{flux.Target{}, helm.Target{}} {
		files, kept, err := Files(tr, []target.Target{tg}, previous)
		if err != nil {
			t.Fatalf("%T: %v", tg, err)
		}
		dropped := map[string]string{}
		for _, k := range kept {
			if k.Dropped != "" {
				dropped[k.Namespace] = k.Dropped
			}
		}
		if len(dropped) != 2 || !strings.Contains(dropped["openbao"], "reserved by the platform (global.okdp.policy.reserved)") || !strings.Contains(dropped["kube-system"], "kube-* is Kubernetes' own") {
			t.Errorf("%T: dropped %q", tg, dropped)
		}
		for p := range files {
			if strings.HasPrefix(p, "projects/openbao/") || strings.HasPrefix(p, "projects/kube-system/") {
				t.Errorf("%T: %s of a project dropped", tg, p)
			}
		}
		if string(files["projects/demo/instances/demo-web/Chart.yaml"]) != "old web\n" {
			t.Errorf("%T: demo: %v", tg, slices.Sorted(maps.Keys(files)))
		}
		if _, isFlux := tg.(flux.Target); isFlux && strings.Contains(string(files["projects/kustomization.yaml"]), "openbao") {
			t.Errorf("the root Kustomization lists openbao: %s", files["projects/kustomization.yaml"])
		}
	}
	// A dropped project's release no longer counts: another namespace may
	// have it.
	other := compiled([]string{"openbao/web", "a/b-web"}, "openbao/web")
	other.Instances[0].Inputs.Context = reserved
	other.Instances[1].Inputs.Namespace, other.Instances[1].Inputs.Release, other.Instances[1].Name = "a", "a-b-web", "b-web"
	if _, kept, err := Files(other, []target.Target{flux.Target{}}, fstest.MapFS{"projects/openbao/instances/a-b-web/Chart.yaml": file("old\n")}); err != nil || kept[0].Dropped == "" {
		t.Errorf("a release of a project dropped: %v %+v", err, kept)
	}
}

// TestPlatformObjectNames: the names the targets write in every namespace
// are among okdp.PlatformObjectNames, which a release may not take
// (okdp.checkRelease).
func TestPlatformObjectNames(t *testing.T) {
	names := []string{flux.RoleName, target.NetworkPolicyName, target.ControlPlaneName, target.AccessName(okdp.RoleViewer), target.AccessName(okdp.RoleEditor), target.AccessName(okdp.RoleAdmin)}
	for _, n := range names {
		if !slices.Contains(okdp.PlatformObjectNames, n) {
			t.Errorf("%s: written by a target in every namespace, not in okdp.PlatformObjectNames", n)
		}
	}
}
