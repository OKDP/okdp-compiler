// Cache: the chart cache a check reads the upstream charts' defaults and
// schemas from (FinalValues): each chart unpacked under its repository's directory
// (Ref.Dir), as `helm pull --untar -d
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
	"sync"
)

// utf8BOM: the byte order mark Helm's loader strips from a chart's files.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// upstreamChart: what the compiler reads of an upstream chart: its entry in
// the chart cache (<repository>/<chart>-<version>/<chart>, for the errors),
// its defaults (values.yaml, empty when the chart has none) and its schema
// (values.schema.json, nil when it has none), compiled once, when a check
// first needs it (validate).
type upstreamChart struct {
	entry    string
	defaults M
	schema   []byte
	once     sync.Once
	compiled *Schema
	err      error
}

// validate: values, what the chart's templates see (Helm's coalesced
// values), against the chart's schema, one line per violation with its path
// (Validate), "" when they hold or the chart has none. The schema is read as
// ParseSchema reads it, compiled as Helm 4 compiles it (CompileSchema), by
// the first check.
func (u *upstreamChart) validate(values M) (string, error) {
	if u.schema == nil {
		return "", nil
	}
	name := u.entry + "/values.schema.json"
	u.once.Do(func() { u.compiled, u.err = ParseSchema(name, u.schema) })
	if u.err != nil {
		return "", u.err
	}
	return Validate(name, u.compiled.root, values)
}

// ChartCache: the upstream charts of a chart cache, each read once and its
// schema compiled once, for every instance a set of compiles checks
// against it (FinalValues). Safe for concurrent use. It keeps what it read:
// not for a chart cache that changes.
type ChartCache struct {
	fsys   fs.FS
	mu     sync.Mutex
	charts map[Ref]*upstreamChart
}

// NewChartCache: the upstream charts of the chart cache fsys.
func NewChartCache(fsys fs.FS) *ChartCache {
	return &ChartCache{fsys: fsys, charts: map[Ref]*upstreamChart{}}
}

// FS: the chart cache the charts are read from.
func (c *ChartCache) FS() fs.FS { return c.fsys }

// get: the chart r, read once from its repository's directory of the chart
// cache: <repository>/<chart>-<version>/<chart> (Ref.Dir, readEntry; Ref.Check
// has checked the repository). A chart missing there fails with the command
// that pulls it, and is looked for again by the next call. Not changed by
// its readers.
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
