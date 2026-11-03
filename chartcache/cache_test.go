package chartcache

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	compiler "github.com/okdp/okdp-compiler"
	"github.com/okdp/okdp-compiler/internal/dirfs"
)

// archive: a chart .tgz holding files (path -> content).
func archive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, name := range slices.Sorted(maps.Keys(files)) {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(files[name])), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(files[name])); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestChartDirIsTheCompilersLayout: a chart's directory, its repository
// without the scheme; a repository or a chart that is no directory of the
// cache refused.
func TestChartDirIsTheCompilersLayout(t *testing.T) {
	for ref, want := range map[compiler.ChartRef]string{
		{Repository: "oci://quay.io/adaltas", Chart: "oidc-dcr", Version: "0.6.0"}:   "quay.io/adaltas/oidc-dcr-0.6.0/oidc-dcr",
		{Repository: "https://charts.example.org/", Chart: "app", Version: "1.8.0"}:  "charts.example.org/app-1.8.0/app",
		{Repository: "http://localhost:8080/charts", Chart: "app", Version: "1.0.0"}: "localhost:8080/charts/app-1.0.0/app",
	} {
		if got, err := ChartDir(ref); err != nil || got != want {
			t.Errorf("%v: %q %v, want %q", ref, got, err, want)
		}
	}
	for _, ref := range []compiler.ChartRef{
		{Repository: "https://x/../y", Chart: "app", Version: "1.0.0"},
		{Repository: "https://", Chart: "app", Version: "1.0.0"},
		{Repository: "https://x", Chart: "../app", Version: "1.0.0"},
		{Repository: "https://x:8443?y=1", Chart: "app", Version: "1.0.0"},
		{Repository: "https://x/charts#f", Chart: "app", Version: "1.0.0"},
		{Repository: "https://x", Chart: "app?x", Version: "1.0.0"},
		{Repository: "https://x", Chart: "app", Version: "1.0.0#y"},
	} {
		if _, err := ChartDir(ref); err == nil {
			t.Errorf("%v: no error", ref)
		}
	}
}

// TestTheCacheUnpacksTheChartsOfTheirRepositories: a chart of an https
// repository is found in its index.yaml and unpacked in the cache, its
// root values.yaml, values.schema.json and Chart.yaml only; one that
// cannot be pulled leaves the compile unchecked.
func TestTheCacheUnpacksTheChartsOfTheirRepositories(t *testing.T) {
	pulls := 0
	repo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/charts/index.yaml":
			_, _ = w.Write([]byte("apiVersion: v1\nentries:\n  app:\n    - version: 1.8.0\n      urls: [app-1.8.0.tgz]\n    - version: 1.7.0\n      urls: [app-1.7.0.tgz]\n"))
		case "/charts/app-1.8.0.tgz":
			pulls++
			_, _ = w.Write(archive(t, map[string]string{
				"app/Chart.yaml":             "name: app\nversion: 1.8.0\n",
				"app/values.yaml":            "replicaCount: 1\n",
				"app/values.schema.json":     "{}",
				"app/templates/deploy.yaml":  "kind: Deployment\n",
				"app/charts/sub/values.yaml": "x: 1\n",
			}))
		default:
			http.NotFound(w, r)
		}
	}))
	defer repo.Close()

	dir := t.TempDir()
	cache := NewCache(dir, NewRepositoryPuller([]string{strings.TrimPrefix(repo.URL, "http://")}))
	ref := compiler.ChartRef{Repository: repo.URL + "/charts", Chart: "app", Version: "1.8.0"}
	values, err := cache.File(t.Context(), ref, "values.yaml")
	if err != nil || string(values) != "replicaCount: 1\n" {
		t.Fatalf("values.yaml: %q %v", values, err)
	}
	chartDir, _ := ChartDir(ref)
	entries, err := os.ReadDir(filepath.Join(dir, chartDir))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if want := []string{"Chart.yaml", "values.schema.json", "values.yaml"}; !reflect.DeepEqual(names, want) {
		t.Errorf("unpacked: %v, want %v", names, want)
	}
	if info, err := os.Stat(filepath.Join(dir, chartDir)); err != nil || info.Mode().Perm() != 0o755 {
		t.Errorf("the chart directory, not readable by every user: %v, %v", info, err)
	}

	fsys, err := cache.Ensure(t.Context(), []compiler.ChartRef{ref})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Stat(fsys, chartDir+"/values.yaml"); err != nil {
		t.Error(err)
	}
	if pulls != 1 {
		t.Errorf("pulled %d times, want once", pulls)
	}

	missing := compiler.ChartRef{Repository: repo.URL + "/charts", Chart: "app", Version: "9.9.9"}
	if _, err := cache.Ensure(t.Context(), []compiler.ChartRef{ref, missing}); err == nil || !strings.Contains(err.Error(), "chart "+missing.Repository+"/app:9.9.9: ") {
		t.Errorf("a chart that cannot be pulled: %v", err)
	}
	if _, err := cache.File(t.Context(), missing, "values.yaml"); err == nil || !strings.Contains(err.Error(), "a moment ago") {
		t.Errorf("not tried again at once: %v", err)
	}
}

// TestUnpackRefusesAnOversizedFile: a file of the archive over
// MaxFileSize fails the unpack.
func TestUnpackRefusesAnOversizedFile(t *testing.T) {
	big := strings.Repeat("x", MaxFileSize+1)
	if err := unpack(bytes.NewReader(archive(t, map[string]string{"app/values.yaml": big})), "app", t.TempDir()); err == nil || !strings.Contains(err.Error(), "more than") {
		t.Errorf("an oversized file: %v", err)
	}
}

// fakePuller hands archive to read, whatever the ref.
type fakePuller struct{ archive []byte }

func (p fakePuller) Pull(_ context.Context, _ compiler.ChartRef, read func(io.Reader) error) error {
	return read(bytes.NewReader(p.archive))
}

// TestTheChartIsTheOneNamed: an archive whose Chart.yaml names another
// chart, another version, or no version, is refused, no directory of the
// chart left; the chart named is kept.
func TestTheChartIsTheOneNamed(t *testing.T) {
	ref := compiler.ChartRef{Repository: "https://charts.example.org", Chart: "app", Version: "1.0.0"}
	for chart, want := range map[string]string{
		"name: app\nversion: 1.0.0\n":   "",
		"name: other\nversion: 1.0.0\n": `its Chart.yaml is of "other" "1.0.0", not the chart named`,
		"name: app\nversion: 9.9.9\n":   `its Chart.yaml is of "app" "9.9.9", not the chart named`,
		"name: app\n":                   `its Chart.yaml is of "app" "", not the chart named`,
		"name: [app\n":                  "its Chart.yaml: ",
	} {
		dir := t.TempDir()
		err := NewCache(dir, fakePuller{archive(t, map[string]string{"app/Chart.yaml": chart})}).Get(t.Context(), ref)
		if want == "" {
			if err != nil {
				t.Errorf("%q: %v", chart, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v, want %q", chart, err, want)
		}
		if entries, err := os.ReadDir(filepath.Join(dir, "charts.example.org")); err != nil || len(entries) > 0 {
			t.Errorf("%q: left %v, %v", chart, entries, err)
		}
	}
}

// TestFileIsOneOfFiles: a name that is not one of Files is refused, before
// anything is pulled or read.
func TestFileIsOneOfFiles(t *testing.T) {
	ref := compiler.ChartRef{Repository: "https://charts.example.org", Chart: "app", Version: "1.0.0"}
	cache := NewCache(t.TempDir(), fakePuller{archive(t, map[string]string{"app/Chart.yaml": "name: app\nversion: 1.0.0\n", "app/values.yaml": "a: 1\n"})})
	for _, name := range []string{"../x", "../../../../../../etc/hostname", "templates/deploy.yaml", "/etc/hostname", ""} {
		if b, err := cache.File(t.Context(), ref, name); err == nil || !strings.Contains(err.Error(), "is no file of a chart the cache keeps") {
			t.Errorf("%q: %q, %v", name, b, err)
		}
	}
	if b, err := cache.File(t.Context(), ref, "values.yaml"); err != nil || string(b) != "a: 1\n" {
		t.Errorf("values.yaml: %q, %v", b, err)
	}
	if _, err := cache.File(t.Context(), ref, "values.schema.json"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a file the chart lacks: %v", err)
	}
}

// TestFSFollowsNoLink: the cache as a compile reads it refuses a symbolic
// link, to a file or a directory, in the cache or out of it; a cache
// directory that does not exist yet holds no file.
func TestFSFollowsNoLink(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("s\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ref := compiler.ChartRef{Repository: "https://charts.example.org", Chart: "app", Version: "1.0.0"}
	cache := NewCache(dir, fakePuller{archive(t, map[string]string{"app/Chart.yaml": "name: app\nversion: 1.0.0\n"})})
	if err := cache.Get(t.Context(), ref); err != nil {
		t.Fatal(err)
	}
	chartDir, _ := ChartDir(ref)
	for link, target := range map[string]string{
		chartDir + "/values.yaml":        filepath.Join(outside, "secret"),
		"charts.example.org/other-1.0.0": outside,
		"charts.example.org/app-2.0.0":   filepath.Join(dir, "charts.example.org", "app-1.0.0"),
	} {
		if err := os.Symlink(target, filepath.Join(dir, filepath.FromSlash(link))); err != nil {
			t.Fatal(err)
		}
	}
	fsys := cache.FS()
	if _, err := fs.ReadFile(fsys, chartDir+"/Chart.yaml"); err != nil {
		t.Errorf("Chart.yaml: %v", err)
	}
	for _, name := range []string{chartDir + "/values.yaml", "charts.example.org/other-1.0.0/secret", "charts.example.org/app-2.0.0/app/Chart.yaml"} {
		if b, err := fs.ReadFile(fsys, name); !errors.Is(err, dirfs.ErrSymlink) {
			t.Errorf("%s: %q, %v", name, b, err)
		}
	}
	if b, err := cache.File(t.Context(), ref, "values.yaml"); !errors.Is(err, dirfs.ErrSymlink) {
		t.Errorf("File through a link: %q, %v", b, err)
	}
	if _, err := fs.ReadFile(NewCache(filepath.Join(dir, "none"), nil).FS(), "x/Chart.yaml"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("no cache directory: %v", err)
	}
}

// tlsRepository: an https chart repository whose index.yaml lists app
// 1.0.0 at the URL archive gives it (the server's own URL), and serves
// the archive at /app-1.0.0.tgz, a redirect to redirect at /redirect;
// the puller trusting its certificate.
func tlsRepository(t *testing.T, insecureHosts []string, archive func(self string) string, redirect string, tgz []byte) (*httptest.Server, *RepositoryPuller) {
	t.Helper()
	var s *httptest.Server
	s = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.yaml":
			_, _ = fmt.Fprintf(w, "entries:\n  app:\n    - version: 1.0.0\n      urls: [%q]\n", archive(s.URL))
		case "/app-1.0.0.tgz":
			_, _ = w.Write(tgz)
		case "/redirect":
			http.Redirect(w, r, redirect, http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.Close)
	p := NewRepositoryPuller(insecureHosts)
	p.http.Transport = s.Client().Transport
	return s, p
}

// TestIndexIsHTTPS: a chart repository, the archive its index.yaml names
// and every redirect are https, or http on a host of InsecureHosts; a
// redirect from https to http is refused whatever the host.
func TestIndexIsHTTPS(t *testing.T) {
	tgz := archive(t, map[string]string{"app/Chart.yaml": "name: app\nversion: 1.0.0\n"})
	served := 0
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.yaml":
			_, _ = w.Write([]byte("entries:\n  app:\n    - version: 1.0.0\n      urls: [app-1.0.0.tgz]\n"))
		case "/app-1.0.0.tgz":
			served++
			_, _ = w.Write(tgz)
		case "/redirect.tgz":
			http.Redirect(w, r, "/app-1.0.0.tgz", http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer plain.Close()
	plainHost := strings.TrimPrefix(plain.URL, "http://")
	pull := func(p *RepositoryPuller, repository string) error {
		return p.Pull(t.Context(), compiler.ChartRef{Repository: repository, Chart: "app", Version: "1.0.0"}, func(r io.Reader) error {
			_, err := io.ReadAll(r)
			return err
		})
	}
	// Plain http: refused unless its host is insecure, nothing fetched.
	if err := pull(NewRepositoryPuller(nil), plain.URL); err == nil || !strings.Contains(err.Error(), "plain http, and "+plainHost+" is not one of the insecure hosts") {
		t.Errorf("an http repository: %v", err)
	}
	if served != 0 {
		t.Errorf("fetched %d times", served)
	}
	if err := pull(NewRepositoryPuller([]string{plainHost}), plain.URL); err != nil || served != 1 {
		t.Errorf("an insecure host: %v, fetched %d times", err, served)
	}
	// An https index: its archive https, or http on an insecure host.
	for name, c := range map[string]struct {
		insecure []string
		archive  func(self string) string
		want     string
	}{
		"https":                               {nil, func(self string) string { return self + "/app-1.0.0.tgz" }, ""},
		"relative":                            {nil, func(string) string { return "app-1.0.0.tgz" }, ""},
		"an http archive":                     {nil, func(string) string { return plain.URL + "/app-1.0.0.tgz" }, "plain http, and " + plainHost + " is not one of the insecure hosts"},
		"an http archive of an insecure host": {[]string{plainHost}, func(string) string { return plain.URL + "/app-1.0.0.tgz" }, ""},
		"another scheme":                      {nil, func(string) string { return "file:///etc/hostname" }, "not an https url"},
		"a redirect to http":                  {[]string{plainHost}, func(self string) string { return self + "/redirect" }, "from https to http refused"},
	} {
		t.Run(name, func(t *testing.T) {
			s, p := tlsRepository(t, c.insecure, c.archive, plain.URL+"/app-1.0.0.tgz", tgz)
			err := pull(p, s.URL)
			switch {
			case c.want == "" && err != nil:
				t.Errorf("refused: %v", err)
			case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
				t.Errorf("%v, want %q", err, c.want)
			}
		})
	}
	// http to http on an insecure host: followed.
	p := NewRepositoryPuller([]string{plainHost})
	if _, err := p.get(t.Context(), plain.URL+"/redirect.tgz", MaxChartSize); err != nil {
		t.Errorf("a redirect of an insecure host to itself: %v", err)
	}
}

// Cache is a compiler.ChartSource.
var _ compiler.ChartSource = (*Cache)(nil)
