package dirfs

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

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

// TestDirFS: a directory reads as os.DirFS (fstest.TestFS); a symbolic
// link is followed, opened, on the path of a file or listed, whether it
// points inside the directory or out of it, and a walk enters a linked
// directory; a file that does not exist is fs.ErrNotExist; the directory
// itself may be a link.
func TestDirFS(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, map[string]string{"outside/secret.yaml": "c: 3\n"})
	tree := filepath.Join(dir, "tree")
	write(t, tree, map[string]string{"platform.yaml": "a: 1\n", "projects/demo/project.yaml": "b: 2\n"})
	d, err := New("--repository", tree)
	if err != nil {
		t.Fatal(err)
	}
	if err := fstest.TestFS(d, "platform.yaml", "projects/demo/project.yaml"); err != nil {
		t.Fatal(err)
	}
	// Links: to a file out of the tree, to a directory in it, to a
	// directory out of it.
	for link, tc := range map[string]struct {
		target string
		file   string // a file read through the link
		want   string
	}{
		"projects/demo/config.yaml": {filepath.Join(dir, "outside", "secret.yaml"), "projects/demo/config.yaml", "c: 3\n"},
		"projects/data":             {filepath.Join(tree, "projects", "demo"), "projects/data/project.yaml", "b: 2\n"},
		"projects/out":              {filepath.Join(dir, "outside"), "projects/out/secret.yaml", "c: 3\n"},
	} {
		t.Run(link, func(t *testing.T) {
			if err := os.Symlink(tc.target, filepath.Join(tree, filepath.FromSlash(link))); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = os.Remove(filepath.Join(tree, filepath.FromSlash(link))) }()
			d, err := New("--repository", tree)
			if err != nil {
				t.Fatal(err)
			}
			if b, err := fs.ReadFile(d, tc.file); err != nil || string(b) != tc.want {
				t.Errorf("read through it: %q, %v", b, err)
			}
			var walked []string
			err = fs.WalkDir(d, ".", func(p string, e fs.DirEntry, err error) error {
				if err == nil && !e.IsDir() {
					walked = append(walked, p)
				}
				return err
			})
			if err != nil || !slices.Contains(walked, tc.file) {
				t.Errorf("walked: %v, %v", walked, err)
			}
		})
	}
	// A link that points nowhere: listed, and fails when opened.
	if err := os.Symlink(filepath.Join(dir, "nope"), filepath.Join(tree, "dangling.yaml")); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.ReadDir(d, "."); err != nil {
		t.Errorf("listed: %v", err)
	}
	if _, err := fs.ReadFile(d, "dangling.yaml"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a dangling link: %v", err)
	}
	if err := os.Remove(filepath.Join(tree, "dangling.yaml")); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.ReadFile(d, "nope.yaml"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a missing file: %v", err)
	}
	if _, err := fs.ReadFile(d, "platform.yaml/x"); err == nil {
		t.Error("a file as a directory")
	}
	if _, err := d.Open("../outside/secret.yaml"); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("out of the directory: %v", err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(tree, link); err != nil {
		t.Fatal(err)
	}
	linked, err := New("--repository", link)
	if err != nil {
		t.Fatal(err)
	}
	if b, err := fs.ReadFile(linked, "platform.yaml"); err != nil || string(b) != "a: 1\n" {
		t.Errorf("the directory a link: %q, %v", b, err)
	}
	if _, err := New("--repository", filepath.Join(dir, "nope")); err == nil || !strings.HasPrefix(err.Error(), "--repository: ") {
		t.Errorf("a missing directory: %v", err)
	}
	if _, err := New("--repository", filepath.Join(tree, "platform.yaml")); err == nil || !strings.HasSuffix(err.Error(), "is not a directory") {
		t.Errorf("a file: %v", err)
	}
}

// TestDirFSBounds: a file over fileCap is refused, and a new one once the
// files opened pass dirCap together, a file opened twice counted once; a
// file that grows past fileCap once opened fails its read.
func TestDirFSBounds(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, map[string]string{"a": strings.Repeat("a", 10), "b": strings.Repeat("b", 10), "c": strings.Repeat("c", 10), "big": strings.Repeat("x", 11)})
	d, err := New("--repository", dir)
	if err != nil {
		t.Fatal(err)
	}
	d.fileCap, d.dirCap = 10, 25
	if _, err := fs.ReadFile(d, "big"); err == nil || err.Error() != "open big: 11 bytes, over the 10 bytes a file read may hold" {
		t.Errorf("a file over the cap: %v", err)
	}
	for _, name := range []string{"a", "b", "a", "b"} {
		if _, err := fs.ReadFile(d, name); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := fs.ReadFile(d, "c"); err == nil || err.Error() != "open c: the files read of --repository "+dir+" pass 25 bytes together" {
		t.Errorf("past the directory's cap: %v", err)
	}
	if _, err := fs.ReadDir(d, "."); err != nil {
		t.Errorf("a directory past the cap: %v", err)
	}
	// Grown once opened: the read fails at the cap.
	g, err := New("--repository", dir)
	if err != nil {
		t.Fatal(err)
	}
	g.fileCap = 10
	f, err := g.Open("a")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	write(t, dir, map[string]string{"a": strings.Repeat("a", 20)})
	b := make([]byte, 64)
	n, err := f.Read(b)
	if n != 10 || err == nil || err.Error() != "read a: over the 10 bytes a file read may hold" {
		t.Errorf("grown: %d bytes, %v", n, err)
	}
}
