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

// TestWrite: the files written, symbolic links followed: a file's, a
// directory's, projects/ itself; what the command does not write under
// projects/ removed, a file's link itself, not what it points to, a
// directory's link entered and kept.
func TestWrite(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	secret := filepath.Join(outside, "secret")
	if err := os.WriteFile(secret, []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	write(t, dir, map[string]string{"projects/demo/instances/a/x.yaml": "old\n"})
	if err := os.Symlink(secret, filepath.Join(dir, "projects/demo/instances/a/stale")); err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{"projects/demo/instances/a/Chart.yaml": []byte("new\n"), MarkerFile: []byte(Marker)}
	if err := Write(dir, files); err != nil {
		t.Fatal(err)
	}
	if got, want := tree(t, dir), map[string]string{filepath.FromSlash("projects/demo/instances/a/Chart.yaml"): "new\n", filepath.FromSlash(MarkerFile): Marker}; !maps.Equal(got, want) {
		t.Errorf("got %v", got)
	}
	if b, err := os.ReadFile(secret); err != nil || string(b) != "keep\n" {
		t.Errorf("the file a stale link pointed to: %q, %v", b, err)
	}
	// A link to a file: written through. A link to a directory: entered,
	// what is not written under it removed, the link kept.
	if err := os.Symlink(secret, filepath.Join(dir, "projects/demo/instances/a/values.yaml")); err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(outside, "data")
	write(t, data, map[string]string{"stale.yaml": "stale\n"})
	if err := os.Symlink(data, filepath.Join(dir, "projects/data")); err != nil {
		t.Fatal(err)
	}
	files = map[string][]byte{"projects/demo/instances/a/values.yaml": []byte("through\n"), "projects/data/kustomization.yaml": []byte("data\n"), MarkerFile: []byte(Marker)}
	if err := Write(dir, files); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(secret); err != nil || string(b) != "through\n" {
		t.Errorf("written through a file's link: %q, %v", b, err)
	}
	if got, want := tree(t, data), map[string]string{"kustomization.yaml": "data\n"}; !maps.Equal(got, want) {
		t.Errorf("through a directory's link: %v", got)
	}
	if info, err := os.Lstat(filepath.Join(dir, "projects/data")); err != nil || info.Mode()&fs.ModeSymlink == 0 {
		t.Errorf("the directory's link: %v, %v", info, err)
	}
	// A projects/ that is a link: written through.
	linked := t.TempDir()
	if err := os.Symlink(filepath.Join(dir, "projects"), filepath.Join(linked, "projects")); err != nil {
		t.Fatal(err)
	}
	if err := Write(linked, map[string][]byte{"projects/demo/kustomization.yaml": []byte("linked\n"), MarkerFile: []byte(Marker)}); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "projects", "demo", "kustomization.yaml")); err != nil || string(b) != "linked\n" {
		t.Errorf("through projects/: %q, %v", b, err)
	}
}

// TestCheck: an output whose projects/ the command may write: none, an
// empty one, one with its marker, or any with force, a symbolic link to
// one too; a projects/ that is not a directory refused, force or not; one
// that holds files but not the marker refused.
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
		"a link":                {link: true},
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
