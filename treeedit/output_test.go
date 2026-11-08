package treeedit

import (
	"errors"
	"io/fs"
	"reflect"
	"slices"
	"strings"
	"testing"

	compiler "github.com/okdp/okdp-compiler"
)

// Flux always, Argo CD when the root says where it reads the output from;
// both under the output's directory in its repository.
func TestOutputTargets(t *testing.T) {
	root := "kind: Application\nspec: {source: {repoURL: https://git.example.org/out.git, targetRevision: main}}\n"
	for name, tc := range map[string]struct {
		files  memTree
		prefix string
		want   []compiler.Target
	}{
		"flux only": {memTree{}, "", []compiler.Target{compiler.Flux{PathPrefix: "compiled"}}},
		"prefix":    {memTree{}, "/deploy/", []compiler.Target{compiler.Flux{PathPrefix: "deploy/compiled"}}},
		"argo": {memTree{ArgoCDRootPath: []byte(root)}, "deploy", []compiler.Target{
			compiler.Flux{PathPrefix: "deploy/compiled"},
			compiler.ArgoCD{RepoURL: "https://git.example.org/out.git", Revision: "main", PathPrefix: "deploy/compiled"},
		}},
	} {
		got, err := OutputTargets(tc.files, tc.prefix)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if len(got) != len(tc.want) {
			t.Errorf("%s: %v, want %v", name, got, tc.want)
			continue
		}
		for i := range got {
			if !reflect.DeepEqual(got[i], tc.want[i]) {
				t.Errorf("%s: target %d = %#v, want %#v", name, i, got[i], tc.want[i])
			}
		}
	}

	if _, err := OutputTargets(memTree{ArgoCDRootPath: []byte("kind: Application\nspec: {}\n")}, ""); err == nil || !strings.HasPrefix(err.Error(), ArgoCDRootPath+": ") {
		t.Errorf("a root with no repoURL: %v", err)
	}
	boom := errors.New("boom")
	if _, err := OutputTargets(failingFile{boom}, ""); !errors.Is(err, boom) {
		t.Errorf("a root that cannot be read: %v", err)
	}
}

type failingFile struct{ err error }

func (f failingFile) ReadFile(string) ([]byte, error)  { return nil, f.err }
func (f failingFile) ReadDir(string) ([]string, error) { return nil, nil }
func (f failingFile) Exists(string) bool               { return false }

// WriteOutput removes compiled/ and writes the files under it; nothing
// else of the tree is touched.
func TestWriteOutput(t *testing.T) {
	tree := memTree{
		"platform.yaml":           []byte("global: {}\n"),
		"compiled/old/file.yaml":  []byte("old\n"),
		"argocd/root.yaml":        []byte("root\n"),
		"compiled/projects/x.yml": []byte("old\n"),
	}
	files := map[string][]byte{"projects/demo/a.yaml": []byte("a\n"), "kustomization.yaml": []byte("k\n")}
	if err := WriteOutput(tree, files); err != nil {
		t.Fatal(err)
	}
	var got []string
	for p := range tree {
		got = append(got, p)
	}
	slices.Sort(got)
	want := []string{"argocd/root.yaml", "compiled/kustomization.yaml", "compiled/projects/demo/a.yaml", "platform.yaml"}
	if !slices.Equal(got, want) {
		t.Errorf("files = %v, want %v", got, want)
	}
	if string(tree["compiled/projects/demo/a.yaml"]) != "a\n" {
		t.Errorf("content = %q", tree["compiled/projects/demo/a.yaml"])
	}
}

// OutputFS is compiled/ of the tree: files whole, directories by the names
// ReadDir gives, what it lacks an fs.ErrNotExist that wraps ErrNotFound, a
// link refused.
func TestOutputFS(t *testing.T) {
	tree := linkTree{
		memTree: memTree{
			"compiled/kustomization.yaml":         []byte("k\n"),
			"compiled/projects/demo/Chart.yaml":   []byte("c\n"),
			"compiled/projects/demo/link/in.yaml": []byte("hidden\n"),
			"platform.yaml":                       []byte("not in the output\n"),
		},
		links: map[string]bool{"compiled/projects/demo/link": true, "compiled/projects/demo/other": true},
	}
	out := OutputFS(tree)
	if data, err := fs.ReadFile(out, "projects/demo/Chart.yaml"); err != nil || string(data) != "c\n" {
		t.Errorf("a file: %q, %v", data, err)
	}
	names := func(dir string) []string {
		entries, err := fs.ReadDir(out, dir)
		if err != nil {
			t.Fatalf("ReadDir(%s): %v", dir, err)
		}
		var got []string
		for _, e := range entries {
			kind := "file"
			switch {
			case e.Type()&fs.ModeSymlink != 0:
				kind = "link"
			case e.IsDir():
				kind = "dir"
			}
			got = append(got, e.Name()+":"+kind)
		}
		return got
	}
	if got := names("."); !slices.Equal(got, []string{"kustomization.yaml:file", "projects:dir"}) {
		t.Errorf("root = %v", got)
	}
	if got := names("projects/demo"); !slices.Equal(got, []string{"Chart.yaml:file", "link:link"}) {
		t.Errorf("projects/demo = %v", got)
	}

	if _, err := fs.Stat(out, "projects/other"); !errors.Is(err, fs.ErrNotExist) || !errors.Is(err, ErrNotFound) {
		t.Errorf("a missing path: %v", err)
	}
	if _, err := out.Open("projects/demo/link/in.yaml"); !errors.Is(err, ErrSymlink) {
		t.Errorf("through a link: %v", err)
	}
	if _, err := out.Open("../platform.yaml"); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("outside the output: %v", err)
	}
}
