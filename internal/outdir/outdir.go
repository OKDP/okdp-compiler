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
)

// MarkerFile marks an output whose projects/ the command wrote, and from
// which it may therefore remove the files it does not write.
const MarkerFile = "projects/.okdp-compile"

// Marker is the content of MarkerFile.
const Marker = "okdp-compile writes projects/ and removes from it every file it does not\nwrite. Remove this file, and it refuses to write here without --force.\n"

// Check returns nil when the command may write output/projects/: the
// directory does not exist, is empty, holds MarkerFile, or force is set.
func Check(output string, force bool) error {
	projects := filepath.Join(output, "projects")
	info, err := os.Stat(projects)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return err
	case !info.IsDir():
		return fmt.Errorf("%s is not a directory", projects)
	case force:
		return nil
	}
	if info, err := os.Stat(filepath.Join(output, filepath.FromSlash(MarkerFile))); err == nil && info.Mode().IsRegular() {
		return nil
	}
	if entries, err := os.ReadDir(projects); err == nil && len(entries) == 0 {
		return nil
	}
	return fmt.Errorf("%s%c exists and was not written by okdp-compile (no %s): choose an empty output, or remove it yourself", projects, filepath.Separator, MarkerFile)
}

// Write writes files, whose keys are slash-separated paths relative to
// dir, and creates dir if needed. Symbolic links are followed.
//
// After writing, Write removes every file under dir/projects/ that is
// not one of files, then the directories left empty. A link to a
// directory is entered, and kept; any other link not in files is
// removed, not what it points to.
func Write(dir string, files map[string][]byte) error {
	for name, b := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, b, 0o644); err != nil {
			return err
		}
	}
	err := prune(dir, "projects", files)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// prune removes every file under dir/rel that is not one of files, then
// the directories under it left empty.
func prune(dir, rel string, files map[string][]byte) error {
	entries, err := os.ReadDir(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		return err
	}
	for _, e := range entries {
		p := path.Join(rel, e.Name())
		full := filepath.Join(dir, filepath.FromSlash(p))
		isDir, link := e.IsDir(), e.Type()&fs.ModeSymlink != 0
		if link {
			info, err := os.Stat(full)
			isDir = err == nil && info.IsDir()
		}
		if !isDir {
			if _, ok := files[p]; !ok {
				if err := os.Remove(full); err != nil {
					return err
				}
			}
			continue
		}
		if err := prune(dir, p, files); err != nil {
			return err
		}
		if left, err := os.ReadDir(full); err == nil && len(left) == 0 && !link {
			if err := os.Remove(full); err != nil {
				return err
			}
		}
	}
	return nil
}
