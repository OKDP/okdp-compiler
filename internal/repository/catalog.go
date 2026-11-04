package repository

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/okdp/okdp-compiler/internal/chart"
)

// ErrNotInCatalog: a service, or a version of a service, that the catalog
// of no repository has.
var ErrNotInCatalog = errors.New("not in the catalog")

// notInCatalog: the error of a package the catalog lacks, its message
// naming it, ErrNotInCatalog to errors.Is.
type notInCatalog struct{ msg string }

func (e *notInCatalog) Error() string        { return e.msg }
func (e *notInCatalog) Is(target error) bool { return target == ErrNotInCatalog }

// repo: the repository of the service, an error wrapping ErrNotInCatalog
// when none has it.
func (t *tree) repo(service string) (*Repository, error) {
	repo, ok := t.services[service]
	if !ok {
		return nil, &notInCatalog{fmt.Sprintf("service %q is in the catalog of no repository", service)}
	}
	return repo, nil
}

// packageDir: the package of version of service,
// catalog/<service>/<version>/, and its repository, the service's; an
// error wrapping ErrNotInCatalog when that repository has no such
// directory. The directory that cannot be read for another reason (a
// symbolic link the caller's fs.FS refuses) gives its own error, naming
// it.
func (t *tree) packageDir(service, version string) (*Repository, string, error) {
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

// Catalog: the packages and the contracts of repositories together, as
// Load indexes them (LoadCatalog).
type Catalog struct{ t *tree }

// Service: a service of a Catalog, catalog/<service>/ of one repository:
// its name, its repository's, and its versions, the directories of
// catalog/<service>/, highest first (sortVersions).
type Service struct {
	Name       string
	Repository string
	Versions   []string
}

// LoadCatalog: the catalog that repos make together, as Load reads it:
// each service in one repository, a contract in several with the same
// bytes, so that the order of repos changes nothing; each repository of
// its role, as Load requires (Repository.Role). Every problem at once,
// each naming its repositories.
func LoadCatalog(repos []Repository) (*Catalog, error) {
	t, err := index(repos)
	if err != nil {
		return nil, err
	}
	return &Catalog{t: t}, nil
}

// Services: every service of the catalog, by name.
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

// Service: the service name, an error wrapping ErrNotInCatalog when no
// repository has it.
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

// Package: the files of version of service, catalog/<service>/<version>/
// of the service's repository, the package Load reads for an instance of
// it; an error wrapping ErrNotInCatalog when that repository has no such
// directory.
func (c *Catalog) Package(service, version string) (fs.FS, error) {
	repo, dir, err := c.t.packageDir(service, version)
	if err != nil {
		return nil, err
	}
	return fs.Sub(repo.FS, dir)
}

// Contracts: the contracts of every repository, <contract>.schema.json,
// as each instance's compile reads them (Inputs.Contracts).
func (c *Catalog) Contracts() fs.FS { return c.t.contracts }

// sortVersions: versions highest first: those that are SemVer
// (chart.IsVersion) by precedence, then the others; those of one
// precedence by name, the highest first.
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
