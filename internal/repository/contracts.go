package repository

import (
	"bytes"
	"io"
	"io/fs"
	"slices"
	"sort"
	"time"
)

// contractsFS: the contracts of a tree in memory, by file name
// (<contract>.schema.json), a flat fs.FS: what a compile reads
// (Inputs.Contracts), written once the repositories are read, then only
// read: safe for concurrent use.
type contractsFS map[string][]byte

// Open: the file name, or the root directory (".").
func (c contractsFS) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	if name == "." {
		return &contractsDir{entries: c.entries()}, nil
	}
	data, ok := c[name]
	if !ok {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return &contractFile{Reader: bytes.NewReader(data), info: fileInfo{name: name, size: int64(len(data))}}, nil
}

// entries: the files, by name.
func (c contractsFS) entries() []fs.DirEntry {
	out := make([]fs.DirEntry, 0, len(c))
	for name, data := range c {
		out = append(out, fs.FileInfoToDirEntry(fileInfo{name: name, size: int64(len(data))}))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// contractFile: an open contract.
type contractFile struct {
	*bytes.Reader
	info fileInfo
}

// Stat: its description.
func (f *contractFile) Stat() (fs.FileInfo, error) { return f.info, nil }

// Close: nothing to release.
func (f *contractFile) Close() error { return nil }

// contractsDir: the open root directory.
type contractsDir struct {
	entries []fs.DirEntry
	next    int
}

// Stat: the root's description.
func (d *contractsDir) Stat() (fs.FileInfo, error) { return fileInfo{name: ".", dir: true}, nil }

// Read: a directory holds no bytes.
func (d *contractsDir) Read([]byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: ".", Err: fs.ErrInvalid}
}

// Close: nothing to release.
func (d *contractsDir) Close() error { return nil }

// ReadDir: the next n entries, every one left when n <= 0 (fs.ReadDirFile).
func (d *contractsDir) ReadDir(n int) ([]fs.DirEntry, error) {
	left := d.entries[d.next:]
	if n <= 0 {
		d.next = len(d.entries)
		return slices.Clone(left), nil
	}
	if len(left) == 0 {
		return nil, io.EOF
	}
	if n > len(left) {
		n = len(left)
	}
	d.next += n
	return slices.Clone(left[:n]), nil
}

// fileInfo: what fs.FileInfo says of a contract or of the root.
type fileInfo struct {
	name string
	size int64
	dir  bool
}

func (i fileInfo) Name() string       { return i.name }
func (i fileInfo) Size() int64        { return i.size }
func (i fileInfo) ModTime() time.Time { return time.Time{} }
func (i fileInfo) IsDir() bool        { return i.dir }
func (i fileInfo) Sys() any           { return nil }
func (i fileInfo) Mode() fs.FileMode {
	if i.dir {
		return fs.ModeDir | 0o555
	}
	return 0o444
}
