// Package dirfs: the files under a directory as an fs.FS, bounded and
// with no symbolic link followed: what okdp-compile reads of a repository,
// the chart cache or the output, and the chart cache of package
// chartcache gives a compile (Cache.FS).
package dirfs

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"strings"
	"sync"
)

// The bounds of what is read of a directory (FS): a file at most
// MaxFileSize bytes, the files read of one directory, each counted once,
// at most MaxDirSize together; of a chart cache (NewCharts), the files of
// each chart's directory. A tree's files are YAML and JSON documents, an
// upstream chart's in the cache its values and schema.
const (
	MaxFileSize = 4 << 20
	MaxDirSize  = 64 << 20
)

// ErrSymlink: what a symbolic link under a directory read gives: it is
// not followed, wherever it points.
var ErrSymlink = errors.New("a symbolic link, not followed")

// FS: the files under a directory (of --repository, --charts, a chart
// cache), as
// os.DirFS gives them, but bounded: a symbolic link is refused, opened,
// listed in a directory or on the path of a file (each element of the
// path checked with Lstat, then the file opened through an os.Root, which
// cannot leave the directory, and checked to be the one checked); a file
// that is neither regular nor a directory (a FIFO, a device) is refused;
// a regular file over fileCap bytes is refused, and so is a new one once
// the files opened pass dirCap together. A Stat goes through Open (fs.Stat),
// its file counted. Safe for concurrent use. The directory itself may be a
// link: the user names it.
type FS struct {
	root    *os.Root
	label   string // --repository <dir>, --charts <dir>: what messages name
	fileCap int64
	dirCap  int64

	perDir bool // each directory's files counted apart (NewCharts)

	mu     sync.Mutex
	sizes  map[string]int64 // the regular files opened, by name
	totals map[string]int64 // their sizes, by directory when perDir, else all under ""
}

// New: the FS of dir, which messages name as label (--repository,
// --charts, the chart cache), its bounds MaxFileSize and MaxDirSize.
// Close releases it.
func New(label, dir string) (*FS, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", label, err)
	}
	return &FS{root: root, label: label + " " + dir, fileCap: MaxFileSize, dirCap: MaxDirSize, sizes: map[string]int64{}, totals: map[string]int64{}}, nil
}

// NewCharts: the FS of dir, a chart cache (--charts, chartcache.Cache),
// as New's but for the bound of the files read together: MaxDirSize
// for the files of each directory, a chart's, not for the cache's. A
// chart's files are read by the instances that name it: one budget for
// the cache would let the charts one project names, each within its
// bounds, exhaust it, and refuse every later instance of the compile,
// another project's included.
func NewCharts(label, dir string) (*FS, error) {
	d, err := New(label, dir)
	if err == nil {
		d.perDir = true
	}
	return d, err
}

// Close: the directory released, its files opened readable until each is
// closed; an error closing a directory only read is of no use.
func (d *FS) Close() { _ = d.root.Close() }

// Open: the file name, a path fs.ValidPath accepts, if neither it nor a
// directory on its path is a symbolic link, and it is a directory or a
// regular file within the bounds. An error is an *fs.PathError of name,
// fs.ErrNotExist for a file that does not exist.
func (d *FS) Open(name string) (fs.File, error) {
	fail := func(err error) (fs.File, error) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: err}
	}
	if !fs.ValidPath(name) {
		return fail(fs.ErrInvalid)
	}
	info, err := d.lstat(name)
	if err != nil {
		return fail(err)
	}
	switch {
	case info.IsDir():
	case info.Mode().IsRegular():
		if err := d.count(name, info.Size()); err != nil {
			return fail(err)
		}
	default:
		return fail(errors.New("not a regular file nor a directory: not read"))
	}
	f, err := d.root.Open(name)
	if err != nil {
		return nil, err
	}
	// The file opened is the one checked: no link swapped in since.
	if opened, err := f.Stat(); err != nil || !os.SameFile(info, opened) {
		_ = f.Close()
		return fail(errors.New("changed while it was opened"))
	}
	return &file{f: f, name: name, fileCap: d.fileCap, left: d.fileCap}, nil
}

// lstat: the information of name, after each directory on its path, none
// of them, nor name, a symbolic link: its error the element's, without its
// path but for an element of the path that is not name.
func (d *FS) lstat(name string) (fs.FileInfo, error) {
	if name == "." {
		return d.root.Lstat(".")
	}
	var info fs.FileInfo
	for i := 0; i >= 0; {
		elem := name
		if j := strings.IndexByte(name[i:], '/'); j >= 0 {
			elem, i = name[:i+j], i+j+1
		} else {
			i = -1
		}
		var err error
		if info, err = d.root.Lstat(elem); err != nil {
			if pe := (*fs.PathError)(nil); errors.As(err, &pe) {
				err = pe.Err
			}
			return nil, err
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			if elem != name {
				return nil, fmt.Errorf("%s: %w", elem, ErrSymlink)
			}
			return nil, ErrSymlink
		}
	}
	return info, nil
}

// count: nil when a regular file of size bytes may be read: within
// fileCap, and, unless it was opened before, the files opened with it
// within dirCap: those of the FS, or of its directory when perDir.
func (d *FS) count(name string, size int64) error {
	if size > d.fileCap {
		return fmt.Errorf("%d bytes, over the %s a file read may hold", size, mib(d.fileCap))
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.sizes[name]; ok {
		return nil
	}
	scope, label := "", d.label
	if d.perDir {
		scope = path.Dir(name)
		label = d.label + ", " + scope + ","
	}
	if d.totals[scope]+size > d.dirCap {
		return fmt.Errorf("the files read of %s pass %s together", label, mib(d.dirCap))
	}
	d.sizes[name] = size
	d.totals[scope] += size
	return nil
}

// mib: n bytes, in MiB when a multiple of one.
func mib(n int64) string {
	if n%(1<<20) == 0 {
		return fmt.Sprintf("%d MiB", n>>20)
	}
	return fmt.Sprintf("%d bytes", n)
}

// file: a file of an FS: what fs.File and fs.ReadDirFile need of an
// *os.File, and no more (no WriterTo, which io.Copy would read past Read
// with): its reads bounded, a file grown past fileCap since it was
// checked failing; its directory entries, a symbolic link among them
// refused.
type file struct {
	f       *os.File
	name    string
	fileCap int64
	left    int64 // the bytes it may still give
}

func (f *file) Stat() (fs.FileInfo, error) { return f.f.Stat() }

func (f *file) Close() error { return f.f.Close() }

// Read: the next bytes, at most fileCap in all; one more fails the read.
func (f *file) Read(b []byte) (int, error) {
	if int64(len(b)) > f.left+1 {
		b = b[:f.left+1]
	}
	n, err := f.f.Read(b)
	if f.left -= int64(n); f.left < 0 {
		return n - 1, &fs.PathError{Op: "read", Path: f.name, Err: fmt.Errorf("over the %s a file read may hold", mib(f.fileCap))}
	}
	return n, err
}

// ReadDir: the entries of the directory, as os.File gives them; a
// symbolic link among them fails the listing, named: a tree holds none
// where it is read, rather than one skipped.
func (f *file) ReadDir(n int) ([]fs.DirEntry, error) {
	entries, err := f.f.ReadDir(n)
	for _, e := range entries {
		if e.Type()&fs.ModeSymlink != 0 {
			p := e.Name()
			if f.name != "." {
				p = f.name + "/" + p
			}
			return nil, &fs.PathError{Op: "readdir", Path: f.name, Err: fmt.Errorf("%s: %w", p, ErrSymlink)}
		}
	}
	return entries, err
}
