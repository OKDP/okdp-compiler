// Charts: the upstream charts of a package, by reference (ChartRef), the
// platform's charts (global.okdp.charts: objects, oidcDcr), and the chart
// cache a compile reads their defaults and schemas from (Inputs.Charts):
// each chart as `helm pull -d <cache>` writes it, <chart>-<version>.tgz, or
// unpacked, <chart>-<version>/. The compiler reads nothing else of a chart:
// a deployer renders it from its repository.
package compiler

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"regexp"
	"strings"
)

// ChartRef: an upstream chart as its repository publishes it: what renders
// the values of a chart key (a Flux HelmRelease, an Argo CD source), and
// what the chart cache holds its defaults and schema under.
type ChartRef struct {
	Repository string `yaml:"repository" json:"repository"` // https://... or oci://...
	Chart      string `yaml:"chart" json:"chart"`           // the chart's name in the repository
	Version    string `yaml:"version" json:"version"`       // a pinned version
}

// chartVersion: a pinned chart version, SemVer as Helm requires it.
var chartVersion = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)

// check: a repository URL, the name of a chart, a pinned version. where
// names the reference in the errors.
func (r ChartRef) check(where string) error {
	switch {
	case r == ChartRef{}:
		return fmt.Errorf("%s is required: the upstream chart (repository, chart, version)", where)
	case !strings.HasPrefix(r.Repository, "https://") && !strings.HasPrefix(r.Repository, "http://") && !strings.HasPrefix(r.Repository, "oci://"):
		return fmt.Errorf("%s.repository %q: the URL of a chart repository (https://... or oci://...)", where, r.Repository)
	case r.Chart == "" || r.Chart != path.Base(r.Chart) || r.Chart == "." || r.Chart == ".." || strings.TrimSpace(r.Chart) != r.Chart:
		return fmt.Errorf("%s.chart %q: the name of a chart of the repository", where, r.Chart)
	case !chartVersion.MatchString(r.Version):
		return fmt.Errorf("%s.version %q: a pinned chart version (1.2.3), not a range", where, r.Version)
	}
	return nil
}

// id: <chart>-<version>, the chart's name in the chart cache and in the
// errors.
func (r ChartRef) id() string {
	return r.Chart + "-" + r.Version
}

// pull: the helm command that downloads the chart.
func (r ChartRef) pull() string {
	if strings.HasPrefix(r.Repository, "oci://") {
		return fmt.Sprintf("helm pull %s/%s --version %s", strings.TrimSuffix(r.Repository, "/"), r.Chart, r.Version)
	}
	return fmt.Sprintf("helm pull %s --repo %s --version %s", r.Chart, r.Repository, r.Version)
}

// platformCharts: the charts the platform renders the compiler's own charts
// with (global.okdp.charts of okdp): objects, the chart of OKDP's own objects
// (okdp-objects, required), and oidcDcr, the chart of the OAuth clients in
// dcr mode (DcrSource by default).
func platformCharts(okdp M) (objects, dcr ChartRef, err error) {
	charts, ok := okdp["charts"].(M)
	if !ok && okdp["charts"] != nil {
		return objects, dcr, fmt.Errorf("global.okdp.charts must be a map (objects, oidcDcr), got %s", short(okdp["charts"]))
	}
	if charts["objects"] == nil {
		return objects, dcr, errors.New("global.okdp.charts.objects is required: the chart OKDP's own objects are rendered with (okdp-objects: repository, chart, version). It is a platform value: set it in platform/platform-values.yaml")
	}
	if objects, err = platformChart(charts["objects"], "global.okdp.charts.objects"); err != nil {
		return objects, dcr, err
	}
	dcr = DcrSource
	if charts["oidcDcr"] != nil {
		dcr, err = platformChart(charts["oidcDcr"], "global.okdp.charts.oidcDcr")
	}
	return objects, dcr, err
}

// platformChart: a chart of global.okdp.charts, a map of strings, checked.
func platformChart(v any, where string) (ChartRef, error) {
	var r ChartRef
	m, ok := v.(M)
	if !ok {
		return r, fmt.Errorf("%s must be a map (repository, chart, version), got %s", where, short(v))
	}
	for _, f := range []struct {
		key string
		to  *string
	}{{"repository", &r.Repository}, {"chart", &r.Chart}, {"version", &r.Version}} {
		if x, set := m[f.key]; set {
			s, ok := x.(string)
			if !ok {
				return r, fmt.Errorf("%s.%s must be a string, got %s", where, f.key, short(x))
			}
			*f.to = s
		}
	}
	return r, r.check(where)
}

// upstreamChart: what the compiler reads of an upstream chart: its defaults
// (values.yaml, empty when the chart has none) and its schema
// (values.schema.json, nil when it has none).
type upstreamChart struct {
	defaults M
	schema   []byte
}

// chartCache: the upstream charts of one compile, each read once from
// Inputs.Charts.
type chartCache struct {
	fsys   fs.FS
	charts map[ChartRef]*upstreamChart
}

// get: the chart r: <chart>-<version>/ of the chart cache, else
// <chart>-<version>.tgz. Not changed by its readers.
func (c *chartCache) get(r ChartRef) (*upstreamChart, error) {
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
func chartFiles(fsys fs.FS, r ChartRef) (map[string][]byte, error) {
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
