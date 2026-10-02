// Cache: the chart cache a compile reads the upstream charts' defaults and
// schemas from: each chart as `helm pull -d <cache>` writes it,
// <chart>-<version>.tgz, or unpacked, <chart>-<version>/. The compiler reads
// nothing else of a chart: a deployer renders it from its repository.
package chart

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"strings"
)

// upstreamChart: what the compiler reads of an upstream chart: its defaults
// (values.yaml, empty when the chart has none) and its schema
// (values.schema.json, nil when it has none).
type upstreamChart struct {
	defaults M
	schema   []byte
}

// chartCache: the upstream charts of one compile, each read once from the
// chart cache fsys.
type chartCache struct {
	fsys   fs.FS
	charts map[Ref]*upstreamChart
}

// get: the chart r: <chart>-<version>/ of the chart cache, else
// <chart>-<version>.tgz. Not changed by its readers.
func (c *chartCache) get(r Ref) (*upstreamChart, error) {
	if u, ok := c.charts[r]; ok {
		return u, nil
	}
	files, err := chartFiles(c.fsys, r)
	if err != nil {
		return nil, err
	}
	u := &upstreamChart{defaults: M{}, schema: files["values.schema.json"]}
	if b, ok := files["values.yaml"]; ok {
		if u.defaults, err = loadValues(b); err != nil {
			return nil, fmt.Errorf("%s/values.yaml: %w", r.id(), err)
		}
	}
	c.charts[r] = u
	return u, nil
}

// chartFiles: values.yaml and values.schema.json of the chart r in the
// chart cache, those the chart has.
func chartFiles(fsys fs.FS, r Ref) (map[string][]byte, error) {
	dir := r.id()
	if fi, err := fs.Stat(fsys, dir); err == nil && fi.IsDir() {
		files := map[string][]byte{}
		for _, name := range []string{"values.yaml", "values.schema.json"} {
			b, err := fs.ReadFile(fsys, path.Join(dir, name))
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, err
			}
			files[name] = b
		}
		return files, nil
	}
	f, err := fsys.Open(dir + ".tgz")
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("chart %s is not in the chart cache: %s -d <chart cache>", dir, r.pull())
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }() // read only: nothing to flush
	files, err := readArchive(f)
	if err != nil {
		return nil, fmt.Errorf("%s.tgz: %w", dir, err)
	}
	return files, nil
}

// readArchive: values.yaml and values.schema.json of a chart archive, as
// `helm package` writes it: <chart>/values.yaml, not those of its
// subcharts.
func readArchive(r io.Reader) (map[string][]byte, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	files := map[string][]byte{}
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return files, nil
		}
		if err != nil {
			return nil, err
		}
		_, name, _ := strings.Cut(path.Clean(h.Name), "/")
		if !h.FileInfo().Mode().IsRegular() || (name != "values.yaml" && name != "values.schema.json") {
			continue
		}
		if files[name], err = io.ReadAll(tr); err != nil {
			return nil, err
		}
	}
}
