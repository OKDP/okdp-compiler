// Package outdir: the output directory of okdp-compile, the one place a
// compile writes: the files of a compiled tree written under the
// directory's projects/, which the command owns (Write), after a check that
// projects/ is its own (Check).
package outdir

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
)

// MarkerFile: projects/.okdp-compile, the mark of an output directory
// the command wrote (Marker): its projects/ the command's, which it
// removes from what it does not write (Write); an output whose
// projects/ holds files but not the mark is refused (Check).
const MarkerFile = "projects/.okdp-compile"

// Marker: the content of MarkerFile.
const Marker = "okdp-compile writes projects/ and removes from it every file it does not\nwrite. Remove this file, and it refuses to write here without --force.\n"

// Check: nil when output/projects/ does not exist, is empty, holds
// MarkerFile, or force is given (--force): the command then removes from
// it what it does not write; a projects/ that is a symbolic link, or not a
// directory, refused even with force.
func Check(output string, force bool) error {
	projects := filepath.Join(output, "projects")
	info, err := os.Lstat(projects)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return err
	case info.Mode()&fs.ModeSymlink != 0:
		return fmt.Errorf("%s is a symbolic link: the command writes no file through one", projects)
	case !info.IsDir():
		return fmt.Errorf("%s is not a directory", projects)
	case force:
		return nil
	}
	if info, err := os.Lstat(filepath.Join(output, filepath.FromSlash(MarkerFile))); err == nil && info.Mode().IsRegular() {
		return nil
	}
	if entries, err := os.ReadDir(projects); err == nil && len(entries) == 0 {
		return nil
	}
	return fmt.Errorf("%s%c exists and was not written by okdp-compile (no %s): choose an empty output, or remove it yourself", projects, filepath.Separator, MarkerFile)
}

// Write: files into dir, created if needed, with the directories of
// their paths, each written through an os.Root of dir: no symbolic link
// leads a file out of it. Before any file is written, every symbolic link
// under dir/projects/, at any depth, is removed (removeLinks): the link
// itself, not what it points to, so that no file of the command lands
// through one, even in dir; a projects/ that is a link is refused. Then
// every file under dir/projects/ that is not one of files removed, and
// the directories left empty: the command owns projects/. A project kept
// (--partial) is in files whole, as compiler.TreeFiles gives it: what the
// output held of it, but a manifest of before that today's settings do
// not write, removed.
func Write(dir string, files map[string][]byte) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	if err := removeLinks(root, "projects"); err != nil {
		return err
	}
	for name, b := range files {
		if err := root.MkdirAll(path.Dir(name), 0o755); err != nil {
			return err
		}
		if info, err := root.Lstat(name); err == nil && !info.Mode().IsRegular() {
			if err := root.Remove(name); err != nil {
				return err
			}
		}
		if err := root.WriteFile(name, b, 0o644); err != nil {
			return err
		}
	}
	var dirs []string
	err = fs.WalkDir(root.FS(), "projects", func(p string, e fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case e.IsDir():
			dirs = append(dirs, p)
			return nil
		}
		if _, ok := files[p]; !ok {
			return root.Remove(p)
		}
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	// Deepest first: a directory after those under it.
	for _, d := range slices.Backward(dirs) {
		if entries, err := fs.ReadDir(root.FS(), d); err == nil && len(entries) == 0 {
			if err := root.Remove(d); err != nil {
				return err
			}
		}
	}
	return nil
}

// removeLinks: every symbolic link under dir of root removed, at any
// depth, the link and not what it points to; none when dir does not
// exist; an error when dir itself is a link or not a directory.
func removeLinks(root *os.Root, dir string) error {
	info, err := root.Lstat(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return err
	case info.Mode()&fs.ModeSymlink != 0:
		return fmt.Errorf("%s is a symbolic link: the command writes no file through one", dir)
	case !info.IsDir():
		return fmt.Errorf("%s is not a directory", dir)
	}
	return fs.WalkDir(root.FS(), dir, func(p string, e fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case e.Type()&fs.ModeSymlink != 0:
			return root.Remove(p)
		}
		return nil
	})
}
