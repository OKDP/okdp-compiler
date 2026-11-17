// Package outdir manages the output directory of okdp-compile. The
// command owns the directory's projects/: it writes the files of a
// compiled tree under it and removes every other file.
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

// MarkerFile marks an output whose projects/ the command wrote, and from
// which it may therefore remove the files it does not write.
const MarkerFile = "projects/.okdp-compile"

// Marker is the content of MarkerFile.
const Marker = "okdp-compile writes projects/ and removes from it every file it does not\nwrite. Remove this file, and it refuses to write here without --force.\n"

// Check returns nil when the command may write output/projects/: the
// directory does not exist, is empty, holds MarkerFile, or force is set.
// A projects/ that is a symbolic link is refused even with force.
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

// Write writes files, whose keys are paths relative to dir, and creates
// dir if needed. No symbolic link redirects a write: every file is
// written through an os.Root of dir, and every link under dir/projects/
// is removed first (the link, not its target), so that no file lands
// through one, even inside dir. A projects/ that is a link is refused.
//
// After writing, Write removes every file under dir/projects/ that is
// not one of files, then the directories left empty.
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

// removeLinks removes every symbolic link under dir of root, at any
// depth. It fails when dir itself is a link or not a directory.
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
