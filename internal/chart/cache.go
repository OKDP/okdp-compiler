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
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"path"
	"regexp"
	"slices"
	"strings"
)

// maxChartSize: the most a chart of the cache holds, decompressed, as Helm
// caps a chart (MaxDecompressedChartSize, 100 MiB). A variable, for the
// tests.
var maxChartSize int64 = 100 << 20

// utf8BOM: the byte order mark Helm's loader strips from a chart's files.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// drivePath: a path in a chart archive that names a Windows drive.
var drivePath = regexp.MustCompile(`^[a-zA-Z]:/`)

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
// name, those the chart has, without a UTF-8 byte order mark: the directory
// name, else the archive name.tgz. fs.ErrNotExist when there is neither.
func readEntry(fsys fs.FS, name string) (map[string][]byte, error) {
	if fi, err := fs.Stat(fsys, name); err == nil && fi.IsDir() {
		files := map[string][]byte{}
		remaining := maxChartSize
		for _, file := range []string{"values.yaml", "values.schema.json"} {
			b, err := readFile(fsys, path.Join(name, file), &remaining)
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

// readFile: the file name of an unpacked chart, taken from the bytes
// remaining of the chart's maxChartSize, as Helm 4.3 reads the files of a
// chart directory (a file over them fails, whatever its size says).
func readFile(fsys fs.FS, name string, remaining *int64) ([]byte, error) {
	f, err := fsys.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }() // read only: nothing to flush
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	tooLarge := fmt.Errorf("%s: chart exceeds maximum decompressed size of %d bytes", name, maxChartSize)
	if fi.Size() > *remaining {
		return nil, tooLarge
	}
	b, err := io.ReadAll(io.LimitReader(f, *remaining+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > *remaining {
		return nil, tooLarge
	}
	*remaining -= int64(len(b))
	return b, nil
}

// readArchive: values.yaml and values.schema.json of a chart archive, not
// those of its subcharts (<chart>/charts/<sub>/values.yaml), as Helm's
// loader reads an archive (LoadArchiveFiles): each entry in the chart's
// directory (archiveName), every file counted against maxChartSize
// decompressed, a UTF-8 byte order mark stripped. The errors are Helm 4's.
func readArchive(r io.Reader) (map[string][]byte, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	files := map[string][]byte{}
	remaining := maxChartSize
	tooLarge := fmt.Errorf("decompressed chart is larger than the maximum size %d", maxChartSize)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return files, nil
		}
		if err != nil {
			return nil, err
		}
		if h.FileInfo().IsDir() || h.Typeflag == tar.TypeXGlobalHeader || h.Typeflag == tar.TypeXHeader {
			continue
		}
		name, err := archiveName(h.Name)
		if err != nil {
			return nil, err
		}
		if h.Size > remaining {
			return nil, tooLarge
		}
		// The other files are read too, to count them, as Helm does.
		w := io.Discard
		var b bytes.Buffer
		keep := h.FileInfo().Mode().IsRegular() && (name == "values.yaml" || name == "values.schema.json")
		if keep {
			w = &b
		}
		n, err := io.Copy(w, io.LimitReader(tr, remaining))
		if err != nil {
			return nil, err
		}
		if remaining -= n; n < h.Size || remaining <= 0 {
			return nil, tooLarge
		}
		if keep {
			files[name] = bytes.TrimPrefix(b.Bytes(), utf8BOM)
		}
	}
}

// archiveName: the path in its chart of the archive entry name, as Helm's
// loader reads it: split on `\` if it has one (an archive written on
// Windows), else on /, and its first segment, the chart's directory,
// dropped. An absolute path, a path out of the chart or a Windows drive
// fails.
func archiveName(name string) (string, error) {
	sep := "/"
	if strings.Contains(name, `\`) {
		sep = `\`
	}
	_, n, _ := strings.Cut(name, sep)
	n = strings.ReplaceAll(n, sep, "/")
	if path.IsAbs(n) {
		return "", errors.New("chart illegally contains absolute paths")
	}
	switch n = path.Clean(n); {
	case n == ".":
		return "", fmt.Errorf("chart illegally contains content outside the base directory: %q", name)
	case strings.HasPrefix(n, ".."):
		return "", errors.New("chart illegally references parent directory")
	case drivePath.MatchString(n):
		return "", errors.New("chart contains illegally named files")
	}
	return n, nil
}
