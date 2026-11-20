// Package dirfs gives the files under a directory as an fs.FS that bounds
// what is read and follows no symbolic link.
package dirfs

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"sync"
)

// The bounds of what an FS reads, in bytes. A file holds at most
// MaxFileSize. The files read of one FS hold at most MaxDirSize together,
// each counted once.
const (
	MaxFileSize = 4 << 20
	MaxDirSize  = 64 << 20
)

// ErrSymlink is the error for a symbolic link under a directory read. A
// link is never followed, wherever it points.
var ErrSymlink = errors.New("a symbolic link, not followed")

// FS gives the files under a directory as os.DirFS does, but refuses a
// symbolic link (opened, listed in a directory or on the path of a
// file), a file that is neither regular nor a directory, a regular file
// over fileCap bytes, and a new file once the files opened pass dirCap
// together. The directory itself may be a link, since the user names it.
// A Stat goes through Open, so it counts its file. An FS is safe for
// concurrent use.
type FS struct {
	root    *os.Root
	label   string // what messages name: --repository <dir>, --rules <dir>
	fileCap int64
	dirCap  int64

	mu    sync.Mutex
	sizes map[string]int64 // the size of each regular file opened, by name
	total int64            // the sum of sizes
}

// New opens the FS of dir, bounded by MaxFileSize and MaxDirSize. label
// names the directory in messages. The caller releases the FS with Close.
func New(label, dir string) (*FS, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", label, err)
	}
	return &FS{root: root, label: label + " " + dir, fileCap: MaxFileSize, dirCap: MaxDirSize, sizes: map[string]int64{}}, nil
}

// Close releases the directory. The files already opened stay readable.
// It drops the error: the directory was only read.
func (d *FS) Close() { _ = d.root.Close() }

// Open opens the file name. It checks each element of the path with
// Lstat, opens the file through an os.Root, which cannot leave the
// directory, then checks that the file opened is the one it checked. An
// error is an *fs.PathError of name.
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
	// Check that the file opened is the one checked: no link was swapped
	// in since.
	if opened, err := f.Stat(); err != nil || !os.SameFile(info, opened) {
		_ = f.Close()
		return fail(errors.New("changed while it was opened"))
	}
	return &file{f: f, name: name, fileCap: d.fileCap, left: d.fileCap}, nil
}

// lstat returns the information of name after checking that neither
// name nor a directory on its path is a symbolic link.
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

// count returns nil when the regular file name, of size bytes, is within
// the bounds. A file opened before is not counted again.
func (d *FS) count(name string, size int64) error {
	if size > d.fileCap {
		return fmt.Errorf("%d bytes, over the %s a file read may hold", size, mib(d.fileCap))
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.sizes[name]; ok {
		return nil
	}
	if d.total+size > d.dirCap {
		return fmt.Errorf("the files read of %s pass %s together", d.label, mib(d.dirCap))
	}
	d.sizes[name] = size
	d.total += size
	return nil
}

func mib(n int64) string {
	if n%(1<<20) == 0 {
		return fmt.Sprintf("%d MiB", n>>20)
	}
	return fmt.Sprintf("%d bytes", n)
}

// file is a file of an FS. It exposes only the methods that fs.File and
// fs.ReadDirFile need. In particular it has no WriterTo, with which
// io.Copy would bypass Read and its bound.
type file struct {
	f       *os.File
	name    string
	fileCap int64
	left    int64 // the bytes Read may still return
}

func (f *file) Stat() (fs.FileInfo, error) { return f.f.Stat() }

func (f *file) Close() error { return f.f.Close() }

// Read returns the next bytes of the file, at most fileCap in all. It
// fails as soon as the file gives one byte more.
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

// ReadDir returns the entries of the directory. A symbolic link among
// them fails the listing, rather than being skipped silently.
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
