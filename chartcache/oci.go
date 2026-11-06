package chartcache

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"slices"
	"strings"
	"syscall"
	"time"

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
// refused whatever the host, of an OCI registry (a blob's) as of an https
// repository.
//
// A chart host is a package's to name, and the puller runs where the
// platform's services are reachable (a CI job, the control plane): every
// connection it opens, to a registry, a token realm, a redirect's target,
// an index or an archive, is refused when its address is private (RFC
// 1918, unique-local, carrier-grade NAT), loopback, link-local (the cloud
// metadata endpoints) or unspecified, the address checked as dialed, but
// for the hosts of PrivateHosts and of InsecureHosts: a registry of the
// platform's own network is allowed by name. Behind a proxy
// ($HTTPS_PROXY), the proxy is dialed, and reaches the hosts: the
// destination is resolved here first and refused at such an address, as
// dialed directly, but for the same allowed hosts (the proxy resolves it
// again; one that answers differently to it is the proxy's to refuse).
// A name this host cannot resolve is refused: its address cannot be
// checked.
//
// Its TLS connections trust the system's certificates, or those
// SetRootCAs gives: a registry of the platform signed by its own CA is
// reached with that CA trusted, never with the certificate check off.
type RepositoryPuller struct {
	http          *http.Client
	oci           *http.Client
	base          *http.Transport // the transport of both, its dialer the address check
	cache         auth.Cache
	proxies       []string                              // the hosts of the proxies of the environment, dialed whatever their address
	proxy         func(*http.Request) (*url.URL, error) // the proxy of a request, http.ProxyFromEnvironment
	InsecureHosts []string
	// PrivateHosts: the chart hosts, host, host:port or *.<domain>,
	// allowed at a private, loopback or link-local address (a registry
	// of the cluster, *.svc.cluster.local), beside InsecureHosts.
	PrivateHosts []string
}

// NewRepositoryPuller pulls from OCI registries and https chart
// repositories.
func NewRepositoryPuller(insecureHosts []string) *RepositoryPuller {
	f := &RepositoryPuller{cache: auth.NewCache(), InsecureHosts: insecureHosts, proxies: proxyHosts(), proxy: http.ProxyFromEnvironment}
	d := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	f.base = http.DefaultTransport.(*http.Transport).Clone()
	f.base.DialContext = f.dial(d)
	f.base.Proxy = f.proxyFor
	f.http = &http.Client{Timeout: FetchTimeout, CheckRedirect: f.checkRedirect, Transport: f.base}
	f.oci = &http.Client{Transport: retry.NewTransport(f.base), CheckRedirect: f.checkRedirect}
	return f
}

// SetRootCAs sets the certificates every TLS connection of the puller
// trusts, an https chart repository's, an OCI registry's, a token realm's
// or a redirect's alike: a platform's CA beside the system's (a pool of
// both, as okdp-gitops' Credentials.RootCAs is); nil, the system's alone.
// Called before the first Pull.
func (f *RepositoryPuller) SetRootCAs(roots *x509.CertPool) {
	f.base.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
}

// proxyHosts: the host names of the proxies of the environment
// (http.ProxyFromEnvironment's variables).
func proxyHosts() []string {
	var out []string
	for _, v := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy"} {
		if u, err := url.Parse(os.Getenv(v)); err == nil && u.Hostname() != "" {
			out = append(out, u.Hostname())
		}
	}
	return out
}

// proxyFor: the proxy of the environment for req, if it has one, once the
// destination of req is checked as a direct connection would be: resolved
// here, every address public (publicAddress), unless the host is allowed
// (PrivateHosts, InsecureHosts). The proxy would connect to whatever it is
// told: the dial check sees the proxy's address only.
func (f *RepositoryPuller) proxyFor(req *http.Request) (*url.URL, error) {
	proxy, err := f.proxy(req)
	if proxy == nil || err != nil {
		return proxy, err
	}
	host, port := req.URL.Hostname(), req.URL.Port()
	switch {
	case port != "":
	case req.URL.Scheme == "http":
		port = "80"
	default:
		port = "443"
	}
	addr := net.JoinHostPort(host, port)
	if f.permitted(addr) {
		return proxy, nil
	}
	ips, err := f.resolve(req.Context(), host)
	if err != nil {
		return nil, fmt.Errorf("%s: cannot be resolved here to check its address before the proxy %s: %w", addr, proxy.Redacted(), err)
	}
	for _, ip := range ips {
		if err := publicAddress(addr, netip.AddrPortFrom(ip, 0).String()); err != nil {
			return nil, fmt.Errorf("%w (through the proxy %s)", err, proxy.Redacted())
		}
	}
	return proxy, nil
}

// resolve: the addresses of host, itself when it is one.
func (f *RepositoryPuller) resolve(ctx context.Context, host string) ([]netip.Addr, error) {
	if ip, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{ip}, nil
	}
	return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
}

// allowed: the host of addr (host:port) may be dialed at any address: a
// host of PrivateHosts or of InsecureHosts (permitted), or a proxy of the
// environment.
func (f *RepositoryPuller) allowed(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	return slices.Contains(f.proxies, host) || f.permitted(addr)
}

// permitted: the chart host of addr (host:port) may be at any address: a
// host of PrivateHosts or of InsecureHosts.
func (f *RepositoryPuller) permitted(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	for _, h := range slices.Concat(f.PrivateHosts, f.InsecureHosts) {
		h = strings.TrimSpace(h)
		if h == addr || h == host || (strings.HasPrefix(h, "*.") && strings.HasSuffix(host, h[1:])) {
			return true
		}
	}
	return false
}

// dial: the DialContext of the puller's transport: d's, an address that
// is not public refused (publicAddress) but for an allowed host, checked
// once resolved, as dialed, so that a name resolved again (DNS
// rebinding) is checked too.
func (f *RepositoryPuller) dial(d *net.Dialer) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		if f.allowed(addr) {
			return d.DialContext(ctx, network, addr)
		}
		guarded := *d
		guarded.Control = func(_, address string, _ syscall.RawConn) error {
			return publicAddress(addr, address)
		}
		return guarded.DialContext(ctx, network, addr)
	}
}

// notPublic: the addresses a chart host is refused at, but when allowed:
// oras's realm denylist, strict (loopback, link-local, unspecified, the
// cloud metadata endpoints; RFC 1918, unique-local, carrier-grade NAT).
var notPublic = slices.Concat(auth.DefaultBlockedRealmPrefixes(), auth.PrivateRealmPrefixes())

// publicAddress: nil when address, the ip:port host:port addr is dialed
// at, is public: not in notPublic, nor loopback, private, link-local,
// multicast or unspecified, an IPv4 address in IPv6 unwrapped.
func publicAddress(addr, address string) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return fmt.Errorf("%s: dialed at %q, not an address", addr, address)
	}
	ip := ap.Addr().Unmap().WithZone("")
	refused := ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified()
	for _, p := range notPublic {
		refused = refused || p.Contains(ip)
	}
	if refused {
		return fmt.Errorf("%s is at %s, a private, loopback or link-local address: a chart host reaches one only when it is allowed (RepositoryPuller.PrivateHosts, InsecureHosts)", addr, ip)
	}
	return nil
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
	// A token realm of another host than the registry's at a private
	// address refused by oras too, unless the puller allows hosts there.
	blocked := notPublic
	if len(f.PrivateHosts) > 0 || len(f.InsecureHosts) > 0 {
		blocked = auth.DefaultBlockedRealmPrefixes()
	}
	repo.Client = &auth.Client{Client: f.oci, Cache: f.cache, BlockedRealmPrefixes: blocked}

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
