// Package dirfs gives the files under a directory as an fs.FS that bounds
// what is read.
package dirfs

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// The bounds of what an FS reads, in bytes. A file holds at most
// MaxFileSize. The files read of one FS hold at most MaxDirSize together,
// each counted once.
const (
	MaxFileSize = 4 << 20
	MaxDirSize  = 64 << 20
)

// FS gives the files under a directory as os.DirFS does, symbolic links
// followed, but refuses a file that is neither regular nor a directory, a
// regular file over fileCap bytes, and a new file once the files opened
// pass dirCap together. A Stat goes through Open, so it counts its file.
// An FS is safe for concurrent use.
type FS struct {
	dir     string
	label   string // what messages name: --repository <dir>, --rules <dir>
	fileCap int64
	dirCap  int64

	mu    sync.Mutex
	sizes map[string]int64 // the size of each regular file opened, by name
	total int64            // the sum of sizes
}

// New returns the FS of dir, bounded by MaxFileSize and MaxDirSize, after
// checking that dir is a directory. label names the directory in
// messages.
func New(label, dir string) (*FS, error) {
	info, err := os.Stat(dir)
	switch {
	case err != nil:
		return nil, fmt.Errorf("%s: %w", label, err)
	case !info.IsDir():
		return nil, fmt.Errorf("%s: %s is not a directory", label, dir)
	}
	return &FS{dir: dir, label: label + " " + dir, fileCap: MaxFileSize, dirCap: MaxDirSize, sizes: map[string]int64{}}, nil
}

// Open opens the file name. It checks the file with Stat before opening
// it, so that a FIFO is never opened, then checks that the file opened is
// the one it checked. An error is an *fs.PathError of name.
func (d *FS) Open(name string) (fs.File, error) {
	fail := func(err error) (fs.File, error) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: err}
	}
	if !fs.ValidPath(name) {
		return fail(fs.ErrInvalid)
	}
	p := filepath.Join(d.dir, filepath.FromSlash(name))
	info, err := os.Stat(p)
	if err != nil {
		if pe := (*fs.PathError)(nil); errors.As(err, &pe) {
			err = pe.Err
		}
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
	f, err := os.Open(p)
	if err != nil {
		if pe := (*fs.PathError)(nil); errors.As(err, &pe) {
			err = pe.Err
		}
		return fail(err)
	}
	// Check that the file opened is the one checked: none was swapped in
	// since.
	if opened, err := f.Stat(); err != nil || !os.SameFile(info, opened) {
		_ = f.Close()
		return fail(errors.New("changed while it was opened"))
	}
	return &file{f: f, name: name, fileCap: d.fileCap, left: d.fileCap}, nil
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

// ReadDir returns the entries of the directory. Unlike os.DirFS, it gives
// a symbolic link as the file it points to, so that a walk enters a
// linked directory; a link that points nowhere is left as it is, and
// fails when opened.
func (f *file) ReadDir(n int) ([]fs.DirEntry, error) {
	entries, err := f.f.ReadDir(n)
	for i, e := range entries {
		if e.Type()&fs.ModeSymlink == 0 {
			continue
		}
		if info, err := os.Stat(filepath.Join(f.f.Name(), e.Name())); err == nil {
			entries[i] = fs.FileInfoToDirEntry(info)
		}
	}
	return entries, err
}
