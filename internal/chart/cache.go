// Cache: the chart cache a compile reads the upstream charts' defaults and
// schemas from: each chart unpacked under its repository's directory
// (Ref.cacheDir), as `helm pull --untar -d
// <cache>/<repository>/<chart>-<version>` writes it:
// <repository>/<chart>-<version>/<chart>/. The compiler reads nothing else
// of a chart: a deployer renders it from its repository.
package chart

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"path"
)

// utf8BOM: the byte order mark Helm's loader strips from a chart's files.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// upstreamChart: what the compiler reads of an upstream chart: its entry in
// the chart cache (<repository>/<chart>-<version>/<chart>, for the errors),
// its defaults (values.yaml, empty when the chart has none) and its schema
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

// get: the chart r, read once from its repository's directory of the chart
// cache: <repository>/<chart>-<version>/<chart> (readEntry; Ref.Check has
// checked the repository). A chart missing there fails with the command
// that pulls it. Not changed by its readers.
func (c *chartCache) get(r Ref) (*upstreamChart, error) {
	if u, ok := c.charts[r]; ok {
		return u, nil
	}
	entry := path.Join(r.cacheDir(), r.id(), r.Chart)
	files, err := readEntry(c.fsys, entry)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("chart %s is not in the chart cache: %s", r.id(), r.pull())
	}
	if err != nil {
		return nil, err
	}
	u := &upstreamChart{entry: entry, defaults: M{}, schema: files["values.schema.json"]}
	if b, ok := files["values.yaml"]; ok {
		if u.defaults, err = ParseValues(b); err != nil {
			return nil, fmt.Errorf("%s/values.yaml: %w", entry, err)
		}
	}
	c.charts[r] = u
	return u, nil
}

// readEntry: values.yaml and values.schema.json of the unpacked chart name
// of the chart cache, those the chart has, without a UTF-8 byte order mark.
// fs.ErrNotExist when there is no such directory.
func readEntry(fsys fs.FS, name string) (map[string][]byte, error) {
	if _, err := fs.Stat(fsys, name); err != nil {
		return nil, err
	}
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
