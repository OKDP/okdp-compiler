// This file reads the chart cache: where a check finds the defaults and
// the schemas of the upstream charts, each unpacked under its repository's
// directory (Ref.Dir) as `helm pull --untar` writes it:
//
//	<repository>/<chart>-<version>/<chart>/
package merge

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sync"

	"github.com/okdp/okdp-compiler/chart"
)

// utf8BOM is the byte order mark Helm's loader strips from a chart's files.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// upstreamChart is what the compiler reads of an upstream chart. Its
// schema is compiled once, when a check first needs it (validate).
type upstreamChart struct {
	entry    string // its directory in the chart cache, for the errors
	defaults M      // its values.yaml, empty when the chart has none
	schema   []byte // its values.schema.json, nil when the chart has none
	once     sync.Once
	compiled *Schema
	err      error
}

// validate checks values, which must be what the chart's templates see
// (Helm's coalesced values), against the chart's schema. It returns one
// line per violation, or "" when the values hold or the chart has no
// schema.
func (u *upstreamChart) validate(values M) (string, error) {
	if u.schema == nil {
		return "", nil
	}
	name := u.entry + "/values.schema.json"
	u.once.Do(func() { u.compiled, u.err = chart.ParseSchema(name, u.schema) })
	if u.err != nil {
		return "", u.err
	}
	return u.compiled.Validate(name, values)
}

// ChartCache holds the upstream charts of a chart cache. It reads each
// chart once and compiles its schema once, for every instance checked
// against it. It is safe for concurrent use. It keeps what it has read, so
// it does not suit a chart cache that changes.
type ChartCache struct {
	fsys   fs.FS
	mu     sync.Mutex
	charts map[Ref]*upstreamChart
}

// NewChartCache returns the upstream charts of the chart cache fsys.
func NewChartCache(fsys fs.FS) *ChartCache {
	return &ChartCache{fsys: fsys, charts: map[Ref]*upstreamChart{}}
}

// FS returns the chart cache the charts are read from.
func (c *ChartCache) FS() fs.FS { return c.fsys }

// get returns the chart r, read once from its directory of the chart
// cache (Ref.Dir). A chart missing from the cache fails with the command
// that pulls it, and the next call looks for it again. Readers do not
// change the chart they get.
func (c *ChartCache) get(r Ref) (*upstreamChart, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if u, ok := c.charts[r]; ok {
		return u, nil
	}
	entry, err := r.Dir()
	if err != nil {
		return nil, err
	}
	files, err := readEntry(c.fsys, entry)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("chart %s is not in the chart cache: %s", r.ID(), r.Pull())
	}
	if err != nil {
		return nil, err
	}
	u := &upstreamChart{entry: entry, defaults: M{}, schema: files["values.schema.json"]}
	if b, ok := files["values.yaml"]; ok {
		if u.defaults, err = chart.ParseValues(b); err != nil {
			return nil, fmt.Errorf("%s/values.yaml: %w", entry, err)
		}
	}
	c.charts[r] = u
	return u, nil
}

// readEntry reads values.yaml and values.schema.json from name, an
// unpacked chart. It returns the files the chart has, without their byte
// order mark, and fs.ErrNotExist when the directory does not exist.
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
