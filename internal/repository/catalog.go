package repository

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/okdp/okdp-compiler/chart"
)

// ErrNotInCatalog reports a service, or a version of a service, that no
// repository's catalog holds.
var ErrNotInCatalog = errors.New("not in the catalog")

// notInCatalog names the missing package. It matches ErrNotInCatalog.
type notInCatalog struct{ msg string }

func (e *notInCatalog) Error() string        { return e.msg }
func (e *notInCatalog) Is(target error) bool { return target == ErrNotInCatalog }

func (t *parts) repo(service string) (*Repository, error) {
	repo, ok := t.services[service]
	if !ok {
		return nil, &notInCatalog{fmt.Sprintf("service %q is in the catalog of no repository", service)}
	}
	return repo, nil
}

// packageDir returns the directory of a package and the service's
// repository, which holds it. The error wraps ErrNotInCatalog when the
// directory does not exist. A directory that cannot be read for another
// reason (a symbolic link the caller's fs.FS refuses) gives its own error.
func (t *parts) packageDir(service, version string) (*Repository, string, error) {
	repo, err := t.repo(service)
	if err != nil {
		return nil, "", err
	}
	dir := path.Join(CatalogDir, service, version)
	info, err := fs.Stat(repo.FS, dir)
	switch {
	case !chart.IsFileName(version) || errors.Is(err, fs.ErrNotExist) || err == nil && !info.IsDir():
		return nil, "", &notInCatalog{fmt.Sprintf("version %q of service %q is not in %s", version, service, where(*repo, path.Join(CatalogDir, service)))}
	case err != nil:
		return nil, "", fmt.Errorf("version %q of service %q: %s: %w", version, service, where(*repo, dir), unwrapPath(err))
	}
	return repo, dir, nil
}

// Catalog holds the packages and the contracts of several repositories,
// indexed as Load indexes them. LoadCatalog builds it.
type Catalog struct{ t *parts }

// Service is a service of a Catalog: catalog/<service>/ in the repository
// named Repository. Versions lists its directories, highest first.
type Service struct {
	Name       string
	Repository string
	Versions   []string
}

// LoadCatalog indexes the catalog that repos make together, the way Load
// does: the order of repos changes nothing, and each repository needs a
// role. The error lists every problem at once.
func LoadCatalog(repos []Repository) (*Catalog, error) {
	t, err := index(repos)
	if err != nil {
		return nil, err
	}
	return &Catalog{t: t}, nil
}

// Services returns every service of the catalog, sorted by name.
func (c *Catalog) Services() ([]Service, error) {
	var out []Service
	var errs []error
	for _, name := range slices.Sorted(maps.Keys(c.t.services)) {
		s, err := c.Service(name)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		out = append(out, *s)
	}
	return out, errors.Join(errs...)
}

// Service returns the named service. The error wraps ErrNotInCatalog when
// no repository holds it.
func (c *Catalog) Service(name string) (*Service, error) {
	repo, err := c.t.repo(name)
	if err != nil {
		return nil, err
	}
	versions, err := dirs(*repo, path.Join(CatalogDir, name))
	if err != nil {
		return nil, err
	}
	versions = slices.DeleteFunc(versions, func(v string) bool { return !chart.IsFileName(v) })
	sortVersions(versions)
	return &Service{Name: name, Repository: repo.Name, Versions: versions}, nil
}

// Package returns the files of a package, as Load reads them for an
// instance of it. The error wraps ErrNotInCatalog when it does not exist.
func (c *Catalog) Package(service, version string) (fs.FS, error) {
	repo, dir, err := c.t.packageDir(service, version)
	if err != nil {
		return nil, err
	}
	return fs.Sub(repo.FS, dir)
}

// Contracts returns the contracts of every repository, as
// <contract>.schema.json files. A compile reads them as Inputs.Contracts.
func (c *Catalog) Contracts() fs.FS { return c.t.contracts }

// sortVersions sorts versions highest first. SemVer versions
// (chart.IsVersion) come first, by precedence; the others follow. Versions
// of equal precedence are sorted by name, highest first.
func sortVersions(versions []string) {
	slices.SortStableFunc(versions, func(a, b string) int {
		va, vb := chart.IsVersion(a), chart.IsVersion(b)
		switch {
		case va != vb && va:
			return -1
		case va != vb:
			return 1
		case va:
			if c := chart.CompareVersions(a, b); c != 0 {
				return -c
			}
		}
		return -strings.Compare(a, b)
	})
}
