package outdir

import (
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tree: the files under dir, path -> content.
func tree(t *testing.T, dir string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return err
		}
		b, err := os.ReadFile(path)
		rel, _ := filepath.Rel(dir, path)
		files[rel] = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// write: the files of files under dir, path -> content.
func write(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestWrite: the files written through the output directory, every
// symbolic link under projects/ removed first, a file's or a directory's,
// in the output or out of it, nothing written through one; a projects/
// that is a link refused; what the command does not write under projects/
// removed, a link itself, not what it points to.
func TestWrite(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	secret := filepath.Join(outside, "secret")
	if err := os.WriteFile(secret, []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	write(t, dir, map[string]string{"projects/demo/instances/a/x.yaml": "old\n"})
	for link, target := range map[string]string{"projects/demo/instances/a/Chart.yaml": secret, "projects/demo/instances/a/stale": secret} {
		if err := os.Symlink(target, filepath.Join(dir, filepath.FromSlash(link))); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string][]byte{"projects/demo/instances/a/Chart.yaml": []byte("new\n"), MarkerFile: []byte(Marker)}
	if err := Write(dir, files); err != nil {
		t.Fatal(err)
	}
	if got, want := tree(t, dir), map[string]string{filepath.FromSlash("projects/demo/instances/a/Chart.yaml"): "new\n", filepath.FromSlash(MarkerFile): Marker}; !maps.Equal(got, want) {
		t.Errorf("got %v", got)
	}
	if b, err := os.ReadFile(secret); err != nil || string(b) != "keep\n" {
		t.Errorf("the file a link pointed to: %q, %v", b, err)
	}
	// A directory link, out of the output or in it, at any depth under
	// projects/: removed before anything is written, the files written
	// in a directory of their own, nothing through the link.
	elsewhere := filepath.Join(dir, "elsewhere")
	if err := os.MkdirAll(filepath.Join(elsewhere, "instances"), 0o755); err != nil {
		t.Fatal(err)
	}
	for link, target := range map[string]string{"projects/data": outside, "projects/demo": "../elsewhere", "projects/solo/instances": "../../elsewhere/instances"} {
		p := filepath.Join(dir, filepath.FromSlash(link))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.RemoveAll(p); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, p); err != nil {
			t.Fatal(err)
		}
	}
	files = map[string][]byte{"projects/data/secret": []byte("overwritten\n"), "projects/demo/kustomization.yaml": []byte("demo\n"), "projects/solo/instances/a/Chart.yaml": []byte("solo\n"), MarkerFile: []byte(Marker)}
	if err := Write(dir, files); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{}
	for p, b := range files {
		want[filepath.FromSlash(p)] = string(b)
	}
	if got := tree(t, dir); !maps.Equal(got, want) {
		t.Errorf("got %v", got)
	}
	for _, p := range []string{"projects/data", "projects/demo", "projects/solo/instances"} {
		if info, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(p))); err != nil || !info.IsDir() {
			t.Errorf("%s: not a directory: %v, %v", p, info, err)
		}
	}
	if b, err := os.ReadFile(secret); err != nil || string(b) != "keep\n" {
		t.Errorf("out of the output: %q, %v", b, err)
	}
	// A projects/ that is a link: refused, nothing written.
	linked := t.TempDir()
	if err := os.Symlink(filepath.Join(dir, "projects"), filepath.Join(linked, "projects")); err != nil {
		t.Fatal(err)
	}
	if err := Write(linked, map[string][]byte{"projects/demo/kustomization.yaml": []byte("linked\n")}); err == nil {
		t.Error("written through projects/, a link")
	}
	if b, err := os.ReadFile(filepath.Join(dir, "projects", "demo", "kustomization.yaml")); err != nil || string(b) != "demo\n" {
		t.Errorf("through projects/: %q, %v", b, err)
	}
}

// TestCheck: an output whose projects/ the command may write: none, an
// empty one, one with its marker, or any with force; a projects/ that is
// a symbolic link or not a directory refused, force or not; one that holds
// files but not the marker refused.
func TestCheck(t *testing.T) {
	for name, tc := range map[string]struct {
		files map[string]string // written under the output
		empty bool              // an empty projects/
		link  bool              // projects/ a symbolic link
		gone  bool              // no output directory
		force bool
		want  string // an error containing it; "" for none
	}{
		"no output":             {gone: true},
		"no projects":           {},
		"empty projects":        {empty: true},
		"marked":                {files: map[string]string{MarkerFile: Marker, "projects/a/x": "x\n"}},
		"not marked":            {files: map[string]string{"projects/a/x": "x\n"}, want: "was not written by okdp-compile (no " + MarkerFile + ")"},
		"not marked, forced":    {files: map[string]string{"projects/a/x": "x\n"}, force: true},
		"marker is a directory": {files: map[string]string{MarkerFile + "/x": "x\n"}, want: "was not written by okdp-compile"},
		"a link":                {link: true, want: "is a symbolic link"},
		"a link, forced":        {link: true, force: true, want: "is a symbolic link"},
		"a file":                {files: map[string]string{"projects": "x\n"}, force: true, want: "is not a directory"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, dir, tc.files)
			switch {
			case tc.empty:
				if err := os.Mkdir(filepath.Join(dir, "projects"), 0o755); err != nil {
					t.Fatal(err)
				}
			case tc.link:
				if err := os.Symlink(t.TempDir(), filepath.Join(dir, "projects")); err != nil {
					t.Fatal(err)
				}
			case tc.gone:
				if err := os.Remove(dir); err != nil {
					t.Fatal(err)
				}
			}
			err := Check(dir, tc.force)
			switch {
			case tc.want == "" && err != nil:
				t.Errorf("got %v", err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
}
