package treeedit

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"testing/fstest"

	compiler "github.com/okdp/okdp-compiler"
)

// ErrSymlink is returned for a path that is, or goes through, a symbolic
// link of the tree: a link may reach outside it, or make one file two;
// the tree holds regular files and directories only. A Reader returns it
// (wrapped) for a read through a link, ReadSources and OutputFS refuse
// the tree that holds one.
var ErrSymlink = errors.New("a symbolic link, which the deployments repository must not hold")

// Bounds of what a compile is given of a tree: its files are kilobytes.
// A tree over them does not compile, the file named (ErrTooLarge).
const (
	// MaxTreeFileSize is the size a file of the tree may have, in bytes.
	MaxTreeFileSize = 4 << 20
	// MaxTreeSize is the size the files of a tree may have together, in
	// bytes.
	MaxTreeSize = 64 << 20
)

// ErrTooLarge is returned for a file of the tree over MaxTreeFileSize,
// or that would take the tree over MaxTreeSize: refused by its size,
// before it is read when the Reader can tell (TooLargeError).
var ErrTooLarge = errors.New("a file over the bounds of the tree")

type tooLargeError struct {
	msg string
}

func (e *tooLargeError) Error() string { return e.msg }
func (e *tooLargeError) Unwrap() error { return ErrTooLarge }

// TooLargeError is the error of the file p of size bytes, over
// MaxTreeFileSize; it wraps ErrTooLarge.
func TooLargeError(p string, size int64) error {
	return &tooLargeError{fmt.Sprintf("%s is %d bytes, more than the %d a file of the tree may weigh", p, size, MaxTreeFileSize)}
}

// TreeTooLargeError is the error of a tree over its bound of limit
// bytes; it wraps ErrTooLarge.
func TreeTooLargeError(limit int64) error {
	return &tooLargeError{fmt.Sprintf("the tree holds more than %d bytes", limit)}
}

// ReadSources reads the parts of the tree r holds that a compile reads,
// platform.yaml (compiler.PlatformFile), CatalogsFile (whose menu the
// compile checks) and every file under projects/, at their paths, as the
// tree compiler.LoadRepository takes: a file of MaxTreeFileSize at most,
// MaxTreeSize together (ErrTooLarge); a path that is, or goes through, a
// symbolic link fails it (ErrSymlink). Nothing else of r is read.
func ReadSources(r Reader) (fstest.MapFS, error) {
	c := &sources{tree: fstest.MapFS{}}
	for _, p := range []string{compiler.PlatformFile, CatalogsFile} {
		data, err := r.ReadFile(p)
		switch {
		case err == nil:
			if err := c.add(p, data); err != nil {
				return nil, err
			}
		case !errors.Is(err, ErrNotFound):
			return nil, err
		}
	}
	projects, err := r.ReadDir(compiler.ProjectsDir)
	if err != nil {
		return nil, err
	}
	for _, p := range projects {
		if err := c.copyDir(r, ProjectDir(p)); err != nil {
			return nil, err
		}
	}
	return c.tree, nil
}

// sources is a tree being read, its size so far.
type sources struct {
	tree fstest.MapFS
	size int
}

// add puts a file in the tree, within the bounds.
func (c *sources) add(p string, data []byte) error {
	if len(data) > MaxTreeFileSize {
		return TooLargeError(p, int64(len(data)))
	}
	if c.size += len(data); c.size > MaxTreeSize {
		return TreeTooLargeError(MaxTreeSize)
	}
	c.tree[p] = &fstest.MapFile{Data: data, Mode: 0o444}
	return nil
}

// copyDir copies the files under dir of r into the tree, its directories
// too. An entry that cannot be read as a file is read as a directory,
// but a symbolic link, which fails the copy.
func (c *sources) copyDir(r Reader, dir string) error {
	names, err := r.ReadDir(dir)
	if err != nil {
		return err
	}
	c.tree[dir] = &fstest.MapFile{Mode: fs.ModeDir | 0o555}
	for _, n := range names {
		p := path.Join(dir, n)
		data, err := r.ReadFile(p)
		if err == nil {
			if err := c.add(p, data); err != nil {
				return err
			}
			continue
		}
		if errors.Is(err, ErrSymlink) || errors.Is(err, ErrTooLarge) {
			return err
		}
		if err := c.copyDir(r, p); err != nil {
			return err
		}
	}
	return nil
}
