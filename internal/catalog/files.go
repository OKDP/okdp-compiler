package catalog

import (
	"bytes"
	"io/fs"
	"maps"
	"slices"
	"time"
)

// memFS is a read-only file system of files at its root, by name: the
// contracts of a Catalog. It holds no directory but the root, which
// ReadDir lists.
type memFS map[string][]byte

func (m memFS) Open(name string) (fs.File, error) {
	if name == "." {
		return &memFile{info: memInfo{name: ".", dir: true}}, nil
	}
	b, ok := m[name]
	if !ok {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return &memFile{r: bytes.NewReader(b), info: memInfo{name: name, size: int64(len(b))}}, nil
}

// ReadDir lists the root, its files sorted by name.
func (m memFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if name != "." {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrNotExist}
	}
	out := make([]fs.DirEntry, 0, len(m))
	for _, name := range slices.Sorted(maps.Keys(m)) {
		out = append(out, memInfo{name: name, size: int64(len(m[name]))})
	}
	return out, nil
}

// memFile is an open file of memFS, or its root, which reads nothing.
type memFile struct {
	r    *bytes.Reader // nil for the root
	info memInfo
}

func (f *memFile) Stat() (fs.FileInfo, error) { return f.info, nil }
func (f *memFile) Close() error               { return nil }

func (f *memFile) Read(b []byte) (int, error) {
	if f.r == nil {
		return 0, &fs.PathError{Op: "read", Path: f.info.name, Err: fs.ErrInvalid}
	}
	return f.r.Read(b)
}

// memInfo describes a file of memFS, or its root, as an fs.FileInfo and
// an fs.DirEntry.
type memInfo struct {
	name string
	size int64
	dir  bool
}

func (i memInfo) Name() string               { return i.name }
func (i memInfo) Size() int64                { return i.size }
func (i memInfo) ModTime() time.Time         { return time.Time{} }
func (i memInfo) IsDir() bool                { return i.dir }
func (i memInfo) Sys() any                   { return nil }
func (i memInfo) Type() fs.FileMode          { return i.Mode().Type() }
func (i memInfo) Info() (fs.FileInfo, error) { return i, nil }

func (i memInfo) Mode() fs.FileMode {
	if i.dir {
		return fs.ModeDir | 0o555
	}
	return 0
}
