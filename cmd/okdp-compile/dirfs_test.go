package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
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

// TestDirFS: a directory without links reads as os.DirFS (fstest.TestFS);
// a symbolic link is refused, opened, on the path of a file or listed,
// whether it points inside the directory or out of it; a file that does
// not exist is fs.ErrNotExist; the directory itself may be a link.
func TestDirFS(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, map[string]string{"outside/secret.yaml": "c: 3\n"})
	tree := filepath.Join(dir, "tree")
	write(t, tree, map[string]string{"platform.yaml": "a: 1\n", "projects/demo/project.yaml": "b: 2\n"})
	d, err := openDir("--repository", tree)
	if err != nil {
		t.Fatal(err)
	}
	if err := fstest.TestFS(d, "platform.yaml", "projects/demo/project.yaml"); err != nil {
		t.Fatal(err)
	}
	d.Close()
	// Links: to a file out of the tree, to a directory in it, to a
	// directory out of it.
	for link, target := range map[string]string{
		"projects/demo/config.yaml": filepath.Join(dir, "outside", "secret.yaml"),
		"projects/data":             filepath.Join(tree, "projects", "demo"),
		"projects/out":              filepath.Join(dir, "outside"),
	} {
		t.Run(link, func(t *testing.T) {
			if err := os.Symlink(target, filepath.Join(tree, filepath.FromSlash(link))); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = os.Remove(filepath.Join(tree, filepath.FromSlash(link))) }()
			d, err := openDir("--repository", tree)
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			if _, err := fs.ReadFile(d, link); !errors.Is(err, errSymlink) || err.Error() != "open "+link+": a symbolic link, not followed" {
				t.Errorf("opened: %v", err)
			}
			if _, err := fs.Stat(d, link); !errors.Is(err, errSymlink) {
				t.Errorf("stat: %v", err)
			}
			parent := link[:strings.LastIndexByte(link, '/')]
			if _, err := fs.ReadDir(d, parent); !errors.Is(err, errSymlink) || err.Error() != "readdir "+parent+": "+link+": a symbolic link, not followed" {
				t.Errorf("listed: %v", err)
			}
			if !strings.HasSuffix(link, ".yaml") {
				if _, err := fs.ReadFile(d, link+"/project.yaml"); !errors.Is(err, errSymlink) || err.Error() != "open "+link+"/project.yaml: "+link+": a symbolic link, not followed" {
					t.Errorf("through it: %v", err)
				}
			}
			if _, err := fs.ReadFile(d, "platform.yaml"); err != nil {
				t.Errorf("a file beside it: %v", err)
			}
		})
	}
	d, err = openDir("--repository", tree)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
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
	linked, err := openDir("--repository", link)
	if err != nil {
		t.Fatal(err)
	}
	defer linked.Close()
	if b, err := fs.ReadFile(linked, "platform.yaml"); err != nil || string(b) != "a: 1\n" {
		t.Errorf("the directory a link: %q, %v", b, err)
	}
	if _, err := openDir("--repository", filepath.Join(dir, "nope")); err == nil || !strings.HasPrefix(err.Error(), "--repository: ") {
		t.Errorf("a missing directory: %v", err)
	}
}

// TestDirFSBounds: a file over fileCap is refused, and a new one once the
// files opened pass dirCap together, a file opened twice counted once; a
// file that grows past fileCap once opened fails its read.
func TestDirFSBounds(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, map[string]string{"a": strings.Repeat("a", 10), "b": strings.Repeat("b", 10), "c": strings.Repeat("c", 10), "big": strings.Repeat("x", 11)})
	d, err := openDir("--repository", dir)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
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
	g, err := openDir("--repository", dir)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
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
