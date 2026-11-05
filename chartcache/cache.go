// Package chartcache keeps a chart cache: the upstream charts the compiled
// instances name, pulled from their own repositories (OCI registries,
// https chart repositories) and unpacked in okdp-compiler's layout, for the
// checks of a compile (compiler.Inputs.Charts) and the chart defaults the
// console shows:
//
//	<dir>/<repository without scheme>/<chart>-<version>/<chart>/   Chart.yaml, values.yaml, values.schema.json
//
// Only the files the server and the compiler read are unpacked.
package chartcache

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	compiler "github.com/okdp/okdp-compiler"
	"github.com/okdp/okdp-compiler/internal/dirfs"
	"golang.org/x/sync/singleflight"
	"sigs.k8s.io/yaml"
)

// Bounds of a chart archive: charts are kilobytes. The compressed stream is
// bounded where it is pulled, the inflated archive and each file here, since
// a gzip bomb fits in a few kilobytes.
const (
	MaxChartSize        = 32 << 20
	MaxUncompressedSize = 256 << 20
	MaxFileSize         = 4 << 20
)

// FetchTimeout bounds one chart pull: a registry that accepts the
// connection and never answers must not pin a compile.
const FetchTimeout = 60 * time.Second

// retryAfter: a chart that could not be pulled is not tried again before.
const retryAfter = time.Minute

// Files are the files of a chart the cache keeps, at its root.
var Files = []string{"Chart.yaml", "values.yaml", "values.schema.json"}

// Puller pulls the archive (.tgz) of a chart version and hands it to read.
type Puller interface {
	Pull(ctx context.Context, ref compiler.ChartRef, read func(io.Reader) error) error
}

// Cache is a chart cache directory. Safe for concurrent use.
type Cache struct {
	Dir    string
	puller Puller

	inflight singleflight.Group
	failures sync.Map // chart dir -> time.Time of the last failure
}

// NewCache keeps the charts under dir, pulled with puller.
func NewCache(dir string, puller Puller) *Cache {
	return &Cache{Dir: dir, puller: puller}
}

// ChartDir is the directory of a chart in the cache, slash-separated:
// <repository without scheme>/<chart>-<version>/<chart>. A repository,
// a chart or a version holding a ? or a # (a URL's query or fragment)
// is refused: the directory of none.
func ChartDir(ref compiler.ChartRef) (string, error) {
	repo := ref.Repository
	for _, scheme := range []string{"oci://", "https://", "http://"} {
		repo = strings.TrimPrefix(repo, scheme)
	}
	repo = strings.TrimSuffix(repo, "/")
	for segment := range strings.SplitSeq(repo, "/") {
		if segment == "" || segment == "." || segment == ".." || strings.ContainsAny(segment, `\?#`) {
			return "", fmt.Errorf("repository %q is no directory of the chart cache", ref.Repository)
		}
	}
	for _, s := range []string{ref.Chart, ref.Version} {
		if s == "" || strings.ContainsAny(s, `/\?#`) || s == "." || s == ".." {
			return "", fmt.Errorf("chart %q %q is no directory of the chart cache", ref.Chart, ref.Version)
		}
	}
	return path.Join(repo, ref.Chart+"-"+ref.Version, ref.Chart), nil
}

// FS is the cache as okdp-compiler reads it (Inputs.Charts), bounded and
// with no symbolic link followed, as okdp-compile reads its --charts: a
// link refused wherever it points, a file that is neither regular nor a
// directory refused, a file over 4 MiB, or the files read of one chart
// through one FS over 64 MiB together, refused (dirfs.NewCharts): each
// chart has its own budget, so that the charts one project names do not
// exhaust the others'. A cache directory that cannot be opened (none yet)
// gives an FS whose every file fails with that error.
func (c *Cache) FS() fs.FS {
	fsys, err := dirfs.NewCharts("the chart cache", c.Dir)
	if err != nil {
		return errFS{err}
	}
	return fsys
}

// errFS: an fs.FS whose every Open fails with err.
type errFS struct{ err error }

func (e errFS) Open(name string) (fs.File, error) {
	return nil, &fs.PathError{Op: "open", Path: name, Err: e.err}
}

// Ensure pulls the charts the cache lacks, the compiler.ChartSource of a
// compile (compiler.CompileTree): an error names each chart that cannot be
// pulled, and the compile then goes on without the checks.
func (c *Cache) Ensure(ctx context.Context, refs []compiler.ChartRef) (fs.FS, error) {
	var errs []error
	for _, ref := range refs {
		if err := c.Get(ctx, ref); err != nil {
			errs = append(errs, fmt.Errorf("chart %s/%s:%s: %w", ref.Repository, ref.Chart, ref.Version, err))
		}
	}
	return c.FS(), errors.Join(errs...)
}

// File returns a file of a chart, one of Files (values.yaml...), pulled
// first when the cache lacks it, read through FS. A chart without the file
// answers fs.ErrNotExist; a name not in Files is refused.
func (c *Cache) File(ctx context.Context, ref compiler.ChartRef, name string) ([]byte, error) {
	if !slices.Contains(Files, name) {
		return nil, fmt.Errorf("%q is no file of a chart the cache keeps: %s", name, strings.Join(Files, ", "))
	}
	if err := c.Get(ctx, ref); err != nil {
		return nil, err
	}
	dir, _ := ChartDir(ref)
	fsys, err := dirfs.NewCharts("the chart cache", c.Dir)
	if err != nil {
		return nil, err
	}
	defer fsys.Close()
	return fs.ReadFile(fsys, dir+"/"+name)
}

// Get pulls a chart into the cache unless it is there. Concurrent calls for
// one chart share the pull; a failed pull is not tried again for a minute.
func (c *Cache) Get(ctx context.Context, ref compiler.ChartRef) error {
	dir, err := ChartDir(ref)
	if err != nil {
		return err
	}
	full := filepath.Join(c.Dir, filepath.FromSlash(dir))
	if _, err := os.Stat(filepath.Join(full, "Chart.yaml")); err == nil {
		return nil
	}
	if at, failed := c.failures.Load(dir); failed && time.Since(at.(time.Time)) < retryAfter {
		return fmt.Errorf("%s %s of %s could not be pulled a moment ago", ref.Chart, ref.Version, ref.Repository)
	}
	result := c.inflight.DoChan(dir, func() (any, error) {
		pullCtx, cancel := context.WithTimeout(context.Background(), FetchTimeout)
		defer cancel()
		err := c.pull(pullCtx, ref, full)
		if err != nil {
			c.failures.Store(dir, time.Now())
		} else {
			c.failures.Delete(dir)
		}
		return nil, err
	})
	select {
	case <-ctx.Done():
		return ctx.Err()
	case r := <-result:
		return r.Err
	}
}

// pull unpacks a chart into a temporary directory, then moves it in place;
// a pull that fails leaves no directory of the chart (its parent, created
// for it, removed when it is left empty).
func (c *Cache) pull(ctx context.Context, ref compiler.ChartRef, dir string) (err error) {
	if c.puller == nil {
		return errors.New("no chart puller")
	}
	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(parent) // only when empty
		}
	}()
	tmp, err := os.MkdirTemp(parent, ".pull-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	err = c.puller.Pull(ctx, ref, func(archive io.Reader) error {
		return unpack(archive, ref.Chart, tmp)
	})
	if err != nil {
		return fmt.Errorf("pulling %s %s of %s: %w", ref.Chart, ref.Version, ref.Repository, err)
	}
	if err := checkChart(tmp, ref); err != nil {
		return fmt.Errorf("pulling %s %s of %s: %w", ref.Chart, ref.Version, ref.Repository, err)
	}
	// Readable by every user of a shared cache: a pull's job of another
	// uid reads what this one pulled (MkdirTemp creates it 0700).
	if err := os.Chmod(tmp, 0o755); err != nil {
		return err
	}
	if err := os.Rename(tmp, dir); err != nil && !errors.Is(err, fs.ErrExist) {
		if _, statErr := os.Stat(filepath.Join(dir, "Chart.yaml")); statErr == nil {
			return nil // unpacked by another process meanwhile
		}
		return err
	}
	return nil
}

// checkChart: nil when dir holds the Chart.yaml of ref: its name the
// chart, its version the version, as the ref names them; an archive of
// another chart, or of another version, refused.
func checkChart(dir string, ref compiler.ChartRef) error {
	b, err := os.ReadFile(filepath.Join(dir, "Chart.yaml"))
	if err != nil {
		return fmt.Errorf("the archive holds no %s/Chart.yaml", ref.Chart)
	}
	var meta struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if err := yaml.Unmarshal(b, &meta); err != nil {
		return fmt.Errorf("its Chart.yaml: %w", err)
	}
	if meta.Name != ref.Chart || meta.Version != ref.Version {
		return fmt.Errorf("its Chart.yaml is of %q %q, not the chart named", meta.Name, meta.Version)
	}
	return nil
}

// unpack writes the kept files of the chart directory of a chart archive.
func unpack(r io.Reader, chart, dir string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("the chart is not a gzip archive: %w", err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(io.LimitReader(gz, MaxUncompressedSize))
	keep := map[string]bool{}
	for _, f := range Files {
		keep[chart+"/"+f] = true
	}
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("reading the chart archive: %w", err)
		}
		name := path.Clean(strings.TrimPrefix(hdr.Name, "./"))
		if hdr.Typeflag != tar.TypeReg || !keep[name] {
			continue
		}
		if hdr.Size > MaxFileSize {
			return fmt.Errorf("%s is %d bytes, more than the %d it may weigh", hdr.Name, hdr.Size, MaxFileSize)
		}
		data, err := io.ReadAll(io.LimitReader(tr, MaxFileSize+1))
		if err != nil {
			return fmt.Errorf("reading %s: %w", hdr.Name, err)
		}
		if int64(len(data)) > MaxFileSize {
			return fmt.Errorf("%s is more than %d bytes", hdr.Name, MaxFileSize)
		}
		if err := os.WriteFile(filepath.Join(dir, path.Base(name)), data, 0o644); err != nil {
			return err
		}
	}
}
