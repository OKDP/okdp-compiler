// Cache: the chart cache a compile reads the upstream charts' defaults and
// schemas from: each chart in its repository's directory (Ref.cacheDir), as
// `helm pull -d <cache>/<repository>` writes it,
// <repository>/<chart>-<version>.tgz, or unpacked,
// <repository>/<chart>-<version>/. The compiler reads nothing else of a
// chart: a deployer renders it from its repository.
package chart

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"strings"
)

// maxChartSize: the most a chart archive of the cache holds, its tar stream
// decompressed, as Helm caps a chart (MaxDecompressedChartSize, 100 MiB). A
// variable, for the tests.
var maxChartSize int64 = 100 << 20

// utf8BOM: the byte order mark Helm's loader strips from a chart's files.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// upstreamChart: what the compiler reads of an upstream chart: its entry in
// the chart cache (<repository>/<chart>-<version>, for the errors), its
// defaults (values.yaml, empty when the chart has none) and its schema
// (values.schema.json, nil when it has none).
type upstreamChart struct {
	entry    string
	defaults M
	schema   []byte
}

// chartCache: the upstream charts of one compile, each read once from the
// chart cache fsys.
type chartCache struct {
	fsys   fs.FS
	charts map[Ref]*upstreamChart
}

// newChartCache: the chart cache fsys, for one compile.
func newChartCache(fsys fs.FS) *chartCache {
	return &chartCache{fsys: fsys, charts: map[Ref]*upstreamChart{}}
}

// get: the chart r, read once from its repository's directory of the chart
// cache: <repository>/<chart>-<version> (readEntry). A chart missing there
// fails with the commands that pull it. Not changed by its readers.
func (c *chartCache) get(r Ref) (*upstreamChart, error) {
	if u, ok := c.charts[r]; ok {
		return u, nil
	}
	dir := r.cacheDir()
	if dir == "" {
		// Ref.Check refuses such a repository: a compile never has one.
		return nil, fmt.Errorf("chart %s: repository %q has no directory in the chart cache: the URL of a chart repository (https://... or oci://...)", r.id(), r.Repository)
	}
	entry := path.Join(dir, r.id())
	files, err := readEntry(c.fsys, entry)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("chart %s is not in the chart cache: %s", r.id(), r.pull())
	}
	if err != nil {
		return nil, err
	}
	u := &upstreamChart{entry: entry, defaults: M{}, schema: files["values.schema.json"]}
	if b, ok := files["values.yaml"]; ok {
		if u.defaults, err = loadValues(b); err != nil {
			return nil, fmt.Errorf("%s/values.yaml: %w", entry, err)
		}
	}
	c.charts[r] = u
	return u, nil
}

// readEntry: values.yaml and values.schema.json of the chart cache entry
// name, those the chart has, without a UTF-8 byte order mark: the directory
// name, else the archive name.tgz. fs.ErrNotExist when there is neither.
func readEntry(fsys fs.FS, name string) (map[string][]byte, error) {
	if fi, err := fs.Stat(fsys, name); err == nil && fi.IsDir() {
		files := map[string][]byte{}
		for _, file := range []string{"values.yaml", "values.schema.json"} {
			b, err := fs.ReadFile(fsys, path.Join(name, file))
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, err
			}
			files[file] = bytes.TrimPrefix(b, utf8BOM)
		}
		return files, nil
	}
	f, err := fsys.Open(name + ".tgz")
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }() // read only: nothing to flush
	files, err := readArchive(f)
	if err != nil {
		return nil, fmt.Errorf("%s.tgz: %w", name, err)
	}
	return files, nil
}

// readArchive: values.yaml and values.schema.json of a chart archive, the
// regular files of those names in the chart's directory (an entry's name
// without its first segment, split on / or \ as Helm's loader splits it),
// not those of its subcharts, a UTF-8 byte order mark stripped. Nothing
// else is kept or checked: the deployer's Helm loads the chart. The tar
// stream fails past maxChartSize bytes decompressed.
func readArchive(r io.Reader) (map[string][]byte, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, err
	}
	// One byte over the cap: past it, a tar block is cut and fails.
	lr := &io.LimitedReader{R: gz, N: maxChartSize + 1}
	tr := tar.NewReader(lr)
	files := map[string][]byte{}
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return files, nil
		}
		if err == nil {
			_, name, _ := strings.Cut(strings.ReplaceAll(h.Name, `\`, "/"), "/")
			if name = path.Clean(name); h.Typeflag == tar.TypeReg && (name == "values.yaml" || name == "values.schema.json") {
				var b []byte
				b, err = io.ReadAll(tr)
				files[name] = bytes.TrimPrefix(b, utf8BOM)
			}
		}
		if err != nil && lr.N == 0 {
			return nil, fmt.Errorf("%w (a chart is at most %d bytes decompressed)", err, maxChartSize)
		}
		if err != nil {
			return nil, err
		}
	}
}
