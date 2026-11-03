package chartcache

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	compiler "github.com/okdp/okdp-compiler"
	"sigs.k8s.io/yaml"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/retry"
)

// HelmChartContentMediaType is the media type of the layer holding a Helm
// chart archive in an OCI registry.
const HelmChartContentMediaType = "application/vnd.cncf.helm.chart.content.v1.tar+gzip"

// RepositoryPuller pulls charts from their repositories, anonymously:
// public OCI registries grant pull tokens without credentials, public https
// chart repositories need none. The hosts of InsecureHosts, host or
// host:port, are reached over plain HTTP (development registries and
// chart repositories without TLS); every other host over https only: an
// http:// chart repository, an index.yaml naming an http:// archive, a
// redirect to one are refused, and a redirect from https to http is
// refused whatever the host.
type RepositoryPuller struct {
	client        remote.Client
	http          *http.Client
	InsecureHosts []string
}

// NewRepositoryPuller pulls from OCI registries and https chart
// repositories.
func NewRepositoryPuller(insecureHosts []string) *RepositoryPuller {
	f := &RepositoryPuller{
		client:        &auth.Client{Client: retry.DefaultClient, Cache: auth.NewCache()},
		InsecureHosts: insecureHosts,
	}
	f.http = &http.Client{Timeout: FetchTimeout, CheckRedirect: f.checkRedirect}
	return f
}

// maxRedirects: the redirects a GET follows, as net/http's default.
const maxRedirects = 10

// checkRedirect: the redirect policy of the chart repositories' GETs: at
// most maxRedirects, none from https to http, and none to a URL check
// refuses.
func (f *RepositoryPuller) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return fmt.Errorf("stopped after %d redirects", maxRedirects)
	}
	if prev := via[len(via)-1]; prev.URL.Scheme == "https" && req.URL.Scheme != "https" {
		return fmt.Errorf("a redirect from %s to %s: from https to %s refused", prev.URL.Redacted(), req.URL.Redacted(), req.URL.Scheme)
	}
	return f.check(req.URL)
}

// check: nil when u may be fetched: https, or http when its host is one
// of InsecureHosts.
func (f *RepositoryPuller) check(u *url.URL) error {
	switch {
	case u.Scheme == "https":
		return nil
	case u.Scheme == "http" && PlainHTTP(u.Host, f.InsecureHosts):
		return nil
	case u.Scheme == "http":
		return fmt.Errorf("%s: plain http, and %s is not one of the insecure hosts", u.Redacted(), u.Host)
	}
	return fmt.Errorf("%s: not an https url", u.Redacted())
}

// Pull hands the archive of ref to read: from its OCI registry
// (oci://<registry>/<path>, the chart <path>/<chart>:<version>), or from the
// index.yaml of its https chart repository.
func (f *RepositoryPuller) Pull(ctx context.Context, ref compiler.ChartRef, read func(io.Reader) error) error {
	if repo, ok := strings.CutPrefix(ref.Repository, "oci://"); ok {
		repository := strings.TrimSuffix(repo, "/") + "/" + ref.Chart
		return f.pullOCI(ctx, repository, ref.Version, PlainHTTP(repository, f.InsecureHosts), read)
	}
	return f.pullIndex(ctx, ref, read)
}

// pullOCI resolves repository:tag and hands the chart archive (.tgz) to read,
// bounded to MaxChartSize, its size and digest those of its manifest's
// layer: the manifest and the layer each verified before they are read.
func (f *RepositoryPuller) pullOCI(ctx context.Context, repository, tag string, plainHTTP bool, read func(io.Reader) error) error {
	repo, err := remote.NewRepository(strings.TrimPrefix(repository, "oci://"))
	if err != nil {
		return err
	}
	repo.PlainHTTP = plainHTTP
	repo.Client = f.client

	manifestDesc, rc, err := repo.FetchReference(ctx, tag)
	if err != nil {
		return fmt.Errorf("resolving the chart manifest: %w", err)
	}
	manifestBytes, err := content.ReadAll(rc, manifestDesc)
	_ = rc.Close()
	if err != nil {
		return fmt.Errorf("reading the chart manifest: %w", err)
	}
	var manifest ocispec.Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return fmt.Errorf("parsing the chart manifest: %w", err)
	}

	for _, layer := range manifest.Layers {
		if layer.MediaType != HelmChartContentMediaType {
			continue
		}
		if layer.Size > MaxChartSize {
			return fmt.Errorf("the chart layer is %d bytes, more than the %d a chart may weigh", layer.Size, MaxChartSize)
		}
		blob, err := repo.Fetch(ctx, layer)
		if err != nil {
			return fmt.Errorf("pulling the chart: %w", err)
		}
		defer func() { _ = blob.Close() }()
		// Read whole and verified against the layer's size and digest
		// before anything of it is unpacked: the registry, a mirror or a
		// proxy serving another archive under the manifest's digest fails
		// the pull.
		archive, err := content.ReadAll(blob, layer)
		if err != nil {
			return fmt.Errorf("pulling the chart: %w", err)
		}
		return read(bytes.NewReader(archive))
	}
	return errors.New("the artifact holds no Helm chart layer")
}

// PlainHTTP reports whether the registry of an OCI reference (with or
// without oci://) is one of the hosts declared reachable over plain HTTP.
func PlainHTTP(ref string, insecureHosts []string) bool {
	host := strings.TrimPrefix(ref, "oci://")
	if i := strings.IndexByte(host, '/'); i >= 0 {
		host = host[:i]
	}
	for _, candidate := range insecureHosts {
		if strings.TrimSpace(candidate) == host {
			return true
		}
	}
	return false
}

// maxIndexSize bounds the index.yaml of a chart repository.
const maxIndexSize = 64 << 20

// pullIndex finds the chart version in the index.yaml of an https chart
// repository, and hands its archive to read: the repository, the archive
// and every redirect https, or http on one of InsecureHosts (check).
func (f *RepositoryPuller) pullIndex(ctx context.Context, ref compiler.ChartRef, read func(io.Reader) error) error {
	base, err := url.Parse(strings.TrimSuffix(ref.Repository, "/") + "/")
	if err != nil || (base.Scheme != "https" && base.Scheme != "http") {
		return fmt.Errorf("repository %q: an oci://, https:// or http:// url", ref.Repository)
	}
	if err := f.check(base); err != nil {
		return fmt.Errorf("repository %q: %w", ref.Repository, err)
	}
	index, err := f.get(ctx, base.ResolveReference(&url.URL{Path: "index.yaml"}).String(), maxIndexSize)
	if err != nil {
		return err
	}
	var doc struct {
		Entries map[string][]struct {
			Version string   `json:"version"`
			URLs    []string `json:"urls"`
		} `json:"entries"`
	}
	if err := yaml.Unmarshal(index, &doc); err != nil {
		return fmt.Errorf("the index.yaml of %s: %w", ref.Repository, err)
	}
	for _, entry := range doc.Entries[ref.Chart] {
		if entry.Version != ref.Version || len(entry.URLs) == 0 {
			continue
		}
		archiveURL, err := base.Parse(entry.URLs[0])
		if err != nil {
			return err
		}
		if err := f.check(archiveURL); err != nil {
			return fmt.Errorf("the archive of %s %s in the index.yaml of %s: %w", ref.Chart, ref.Version, ref.Repository, err)
		}
		archive, err := f.get(ctx, archiveURL.String(), MaxChartSize)
		if err != nil {
			return err
		}
		return read(strings.NewReader(string(archive)))
	}
	return fmt.Errorf("the index.yaml of %s has no %s %s", ref.Repository, ref.Chart, ref.Version)
}

func (f *RepositoryPuller) get(ctx context.Context, target string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	resp, err := f.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", target, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("GET %s: more than %d bytes", target, limit)
	}
	return data, nil
}
