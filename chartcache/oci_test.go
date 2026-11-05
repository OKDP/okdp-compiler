package chartcache

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	compiler "github.com/okdp/okdp-compiler"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
)

// stored: a chart .tgz holding files, gzip without compression: two
// archives of files of the same lengths have the same length.
func stored(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz, err := gzip.NewWriterLevel(&buf, gzip.NoCompression)
	if err != nil {
		t.Fatal(err)
	}
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

// registry: a fake OCI registry over plain HTTP, the chart
// <repository>:<tag> a manifest of one Helm chart layer, its descriptor
// that of layer, and the blob it serves served, which may be another.
func registry(t *testing.T, repository, tag string, layer, served []byte) *httptest.Server {
	t.Helper()
	config := []byte("{}")
	manifest, err := json.Marshal(ocispec.Manifest{
		SchemaVersion: 2,
		MediaType:     ocispec.MediaTypeImageManifest,
		Config:        ocispec.Descriptor{MediaType: "application/vnd.cncf.helm.config.v1+json", Digest: digest.FromBytes(config), Size: int64(len(config))},
		Layers:        []ocispec.Descriptor{{MediaType: HelmChartContentMediaType, Digest: digest.FromBytes(layer), Size: int64(len(layer))}},
	})
	if err != nil {
		t.Fatal(err)
	}
	blobs := map[string][]byte{digest.FromBytes(layer).String(): served, digest.FromBytes(config).String(): config}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch p := r.URL.Path; {
		case p == "/v2/" || p == "/v2":
			w.WriteHeader(http.StatusOK)
		case p == "/v2/"+repository+"/manifests/"+tag:
			w.Header().Set("Content-Type", ocispec.MediaTypeImageManifest)
			w.Header().Set("Docker-Content-Digest", digest.FromBytes(manifest).String())
			_, _ = w.Write(manifest)
		case strings.HasPrefix(p, "/v2/"+repository+"/blobs/"):
			b, ok := blobs[strings.TrimPrefix(p, "/v2/"+repository+"/blobs/")]
			if !ok {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(b)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

// TestPullOCIVerifiesTheLayer: a chart layer whose content is not the one
// its manifest's digest names fails the pull, nothing of it unpacked, no
// directory of the chart created; the layer the digest names is unpacked.
func TestPullOCIVerifiesTheLayer(t *testing.T) {
	good := stored(t, map[string]string{"app/Chart.yaml": "name: app\nversion: 1.0.0\n", "app/values.yaml": "replicaCount: 1\n"})
	forged := stored(t, map[string]string{"app/Chart.yaml": "name: app\nversion: 1.0.0\n", "app/values.yaml": "replicaCount: 9\n"})
	if len(good) != len(forged) || bytes.Equal(good, forged) {
		t.Fatalf("the forged archive: %d bytes, the good one %d", len(forged), len(good))
	}
	for name, served := range map[string][]byte{"forged": forged, "good": good} {
		t.Run(name, func(t *testing.T) {
			s := registry(t, "charts/app", "1.0.0", good, served)
			host := strings.TrimPrefix(s.URL, "http://")
			dir := t.TempDir()
			cache := NewCache(dir, NewRepositoryPuller([]string{host}))
			ref := compiler.ChartRef{Repository: "oci://" + host + "/charts", Chart: "app", Version: "1.0.0"}
			values, err := cache.File(t.Context(), ref, "values.yaml")
			chartDir, _ := ChartDir(ref)
			if name == "good" {
				if err != nil || string(values) != "replicaCount: 1\n" {
					t.Fatalf("values.yaml: %q, %v", values, err)
				}
				info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(chartDir)))
				if err != nil || info.Mode().Perm() != 0o755 {
					t.Errorf("the chart directory, not readable by every user: %v, %v", info, err)
				}
				return
			}
			if err == nil || !errors.Is(err, content.ErrMismatchedDigest) {
				t.Errorf("a forged layer: %q, %v", values, err)
			}
			if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(chartDir), "..")); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("a directory of the chart left: %v", err)
			}
		})
	}
}

// TestPullerDialsPublicAddresses: a chart host at a private, loopback or
// link-local address is refused, its index, an OCI registry's token
// realm of another host alike, but for a host of PrivateHosts or of
// InsecureHosts; an OCI registry's redirect from https to http is
// refused, as an index's.
func TestPullerDialsPublicAddresses(t *testing.T) {
	tgz := stored(t, map[string]string{"app/Chart.yaml": "name: app\nversion: 1.0.0\n"})
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(tgz) }))
	defer plain.Close()
	realm := ""
	var s *httptest.Server
	s = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch p := r.URL.Path; {
		case p == "/index.yaml":
			_, _ = fmt.Fprintf(w, "entries:\n  app:\n    - version: 1.0.0\n      urls: [%q]\n", s.URL+"/app-1.0.0.tgz")
		case p == "/app-1.0.0.tgz":
			_, _ = w.Write(tgz)
		case realm != "":
			w.Header().Set("WWW-Authenticate", `Bearer realm="`+realm+`",service="x"`)
			w.WriteHeader(http.StatusUnauthorized)
		case strings.HasPrefix(p, "/v2/charts/app/manifests/"):
			config := []byte("{}")
			manifest, _ := json.Marshal(ocispec.Manifest{
				SchemaVersion: 2, MediaType: ocispec.MediaTypeImageManifest,
				Config: ocispec.Descriptor{MediaType: "application/vnd.cncf.helm.config.v1+json", Digest: digest.FromBytes(config), Size: int64(len(config))},
				Layers: []ocispec.Descriptor{{MediaType: HelmChartContentMediaType, Digest: digest.FromBytes(tgz), Size: int64(len(tgz))}},
			})
			w.Header().Set("Content-Type", ocispec.MediaTypeImageManifest)
			w.Header().Set("Docker-Content-Digest", digest.FromBytes(manifest).String())
			_, _ = w.Write(manifest)
		case strings.HasPrefix(p, "/v2/charts/app/blobs/"):
			http.Redirect(w, r, plain.URL+"/blob", http.StatusTemporaryRedirect)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer s.Close()
	host := strings.TrimPrefix(s.URL, "https://")
	puller := func(private ...string) *RepositoryPuller {
		p := NewRepositoryPuller(nil)
		p.PrivateHosts = private
		p.base.TLSClientConfig = s.Client().Transport.(*http.Transport).TLSClientConfig
		return p
	}
	pull := func(p *RepositoryPuller, repository string) error {
		return p.Pull(t.Context(), compiler.ChartRef{Repository: repository, Chart: "app", Version: "1.0.0"}, func(io.Reader) error { return nil })
	}
	const private = "a private, loopback or link-local address"
	if err := pull(puller(), s.URL); err == nil || !strings.Contains(err.Error(), private) {
		t.Errorf("an index at a loopback address: %v", err)
	}
	if err := pull(puller("127.0.0.1"), s.URL); err != nil {
		t.Errorf("an index of a host allowed: %v", err)
	}
	if err := pull(puller(), "oci://"+host+"/charts"); err == nil || !strings.Contains(err.Error(), private) {
		t.Errorf("a registry at a loopback address: %v", err)
	}
	if err := pull(puller("*.example.org", host), "oci://"+host+"/charts"); err == nil || !strings.Contains(err.Error(), "from https to http refused") {
		t.Errorf("a blob redirected to http: %v", err)
	}
	realm = "https://10.255.255.1:1/token"
	if err := pull(puller(host), "oci://"+host+"/charts"); err == nil || !strings.Contains(err.Error(), "10.255.255.1:1 is at 10.255.255.1, "+private) {
		t.Errorf("a realm at a private address: %v", err)
	}
	for addr, want := range map[string]bool{"8.8.8.8:443": true, "10.1.2.3:443": false, "[::ffff:192.168.1.1]:443": false, "169.254.169.254:80": false, "[fd00::1]:443": false, "100.64.0.1:443": false, "0.0.0.0:443": false, "[2001:4860::8888]:443": true} {
		if err := publicAddress("h:1", addr); (err == nil) != want {
			t.Errorf("%s: %v, public %v", addr, err, want)
		}
	}
}

// TestPullerRootCAs: a registry and an https chart repository signed by a
// CA of the platform are refused without it (the system's certificates),
// pulled with it given (SetRootCAs), the address allowed by PrivateHosts.
func TestPullerRootCAs(t *testing.T) {
	tgz := stored(t, map[string]string{"app/Chart.yaml": "name: app\nversion: 1.0.0\n"})
	var s *httptest.Server
	s = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch p := r.URL.Path; {
		case p == "/index.yaml":
			_, _ = fmt.Fprintf(w, "entries:\n  app:\n    - version: 1.0.0\n      urls: [%q]\n", s.URL+"/app-1.0.0.tgz")
		case p == "/app-1.0.0.tgz", strings.HasPrefix(p, "/v2/charts/app/blobs/"):
			_, _ = w.Write(tgz)
		case strings.HasPrefix(p, "/v2/charts/app/manifests/"):
			config := []byte("{}")
			manifest, _ := json.Marshal(ocispec.Manifest{
				SchemaVersion: 2, MediaType: ocispec.MediaTypeImageManifest,
				Config: ocispec.Descriptor{MediaType: "application/vnd.cncf.helm.config.v1+json", Digest: digest.FromBytes(config), Size: int64(len(config))},
				Layers: []ocispec.Descriptor{{MediaType: HelmChartContentMediaType, Digest: digest.FromBytes(tgz), Size: int64(len(tgz))}},
			})
			w.Header().Set("Content-Type", ocispec.MediaTypeImageManifest)
			w.Header().Set("Docker-Content-Digest", digest.FromBytes(manifest).String())
			_, _ = w.Write(manifest)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer s.Close()
	host := strings.TrimPrefix(s.URL, "https://")
	roots := x509.NewCertPool()
	roots.AddCert(s.Certificate())
	pull := func(p *RepositoryPuller, repository string) error {
		return p.Pull(t.Context(), compiler.ChartRef{Repository: repository, Chart: "app", Version: "1.0.0"}, func(r io.Reader) error {
			_, err := io.ReadAll(r)
			return err
		})
	}
	for _, repository := range []string{s.URL, "oci://" + host + "/charts"} {
		p := NewRepositoryPuller(nil)
		p.PrivateHosts = []string{"127.0.0.1"}
		var unknown x509.UnknownAuthorityError
		if err := pull(p, repository); !errors.As(err, &unknown) {
			t.Errorf("%s, its CA not trusted: %v", repository, err)
		}
		p.SetRootCAs(roots)
		if err := pull(p, repository); err != nil {
			t.Errorf("%s, its CA trusted: %v", repository, err)
		}
	}
}
