// Cache: the chart cache a compile reads the upstream charts' defaults and
// schemas from: each chart in its repository's directory (Ref.cacheDir), as
// `helm pull -d <cache>/<repository>` writes it,
// <repository>/<chart>-<version>.tgz, or unpacked,
// <repository>/<chart>-<version>/; else <chart>-<version> at the root,
// unless the compile uses the chart's name and version from two
// repositories. The compiler reads nothing else of a chart: a deployer
// renders it from its repository.
package chart

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"path"
	"slices"
	"strings"
)

// upstreamChart: what the compiler reads of an upstream chart: its entry in
// the chart cache (<repository>/<chart>-<version> or <chart>-<version>, for
// the errors), its defaults (values.yaml, empty when the chart has none) and
// its schema (values.schema.json, nil when it has none).
type upstreamChart struct {
	entry    string
	defaults M
	schema   []byte
}

// chartCache: the upstream charts of one compile, each read once from the
// chart cache fsys. repositories: by <chart>-<version>, the repositories
// the compile uses it from, by directory (Ref.cacheDir).
type chartCache struct {
	fsys         fs.FS
	charts       map[Ref]*upstreamChart
	repositories map[string]map[string]Ref
}

// newChartCache: the chart cache fsys, for a compile of the upstream charts
// refs. Of two refs in the same directory, the first is kept.
func newChartCache(fsys fs.FS, refs []Ref) *chartCache {
	c := &chartCache{fsys: fsys, charts: map[Ref]*upstreamChart{}, repositories: map[string]map[string]Ref{}}
	for _, r := range refs {
		repos := c.repositories[r.id()]
		if repos == nil {
			repos = map[string]Ref{}
			c.repositories[r.id()] = repos
		}
		if _, ok := repos[r.cacheDir()]; !ok {
			repos[r.cacheDir()] = r
		}
	}
	return c
}

// get: the chart r (files), read once. Not changed by its readers.
func (c *chartCache) get(r Ref) (*upstreamChart, error) {
	if u, ok := c.charts[r]; ok {
		return u, nil
	}
	entry, files, err := c.files(r)
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

// files: the entry of the chart r in the chart cache, and its values.yaml
// and values.schema.json, those it has: <repository>/<chart>-<version>,
// else <chart>-<version>, which cannot tell the repositories apart when the
// compile uses the chart's name and version from several.
func (c *chartCache) files(r Ref) (string, map[string][]byte, error) {
	if dir := r.cacheDir(); dir != "" {
		entry := path.Join(dir, r.id())
		if files, err := readEntry(c.fsys, entry); !errors.Is(err, fs.ErrNotExist) {
			return entry, files, err
		}
	}
	entry := r.id()
	files, err := readEntry(c.fsys, entry)
	switch repos := c.repositories[entry]; {
	case errors.Is(err, fs.ErrNotExist):
		return "", nil, fmt.Errorf("chart %s is not in the chart cache: %s", entry, r.pull())
	case len(repos) > 1:
		var pulls []string
		for _, dir := range slices.Sorted(maps.Keys(repos)) {
			pulls = append(pulls, repos[dir].pull())
		}
		return "", nil, fmt.Errorf("chart %s comes from %d repositories, and the chart cache holds it only as %s, which cannot tell them apart: pull each into its repository's directory: %s", entry, len(repos), entry, strings.Join(pulls, "; "))
	}
	return entry, files, err
}

// readEntry: values.yaml and values.schema.json of the chart cache entry
// name, those the chart has: the directory name, else the archive
// name.tgz. fs.ErrNotExist when there is neither.
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
			files[file] = b
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
