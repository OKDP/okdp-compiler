package chartcache

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
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
	specs "github.com/opencontainers/image-spec/specs-go"
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
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispec.MediaTypeImageManifest,
		Config:    ocispec.Descriptor{MediaType: "application/vnd.cncf.helm.config.v1+json", Digest: digest.FromBytes(config), Size: int64(len(config))},
		Layers:    []ocispec.Descriptor{{MediaType: HelmChartContentMediaType, Digest: digest.FromBytes(layer), Size: int64(len(layer))}},
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
