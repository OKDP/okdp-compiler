// Package catalog reads the packages and the contracts of one or more
// directories, each laid out as a catalog:
//
//	catalog/<service>/<version>/        a package
//	contracts/<contract>.schema.json    a contract
//
// Nothing else of a directory is read.
package catalog

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/okdp/okdp-compiler/chart"
	"github.com/okdp/okdp-compiler/value"
)

// The directories of a catalog.
const (
	CatalogDir   = "catalog"   // <service>/<version>/, a package
	ContractsDir = "contracts" // <contract>.schema.json
)

// ErrNotInCatalog reports a service, or a version of a service, that no
// directory of the catalog holds.
var ErrNotInCatalog = errors.New("not in the catalog")

// notInCatalog names the missing package. It matches ErrNotInCatalog.
type notInCatalog struct{ msg string }

func (e *notInCatalog) Error() string        { return e.msg }
func (e *notInCatalog) Is(target error) bool { return target == ErrNotInCatalog }

// Files is one directory of a catalog. Name identifies it in messages.
type Files struct {
	Name string
	FS   fs.FS
}

// Catalog holds the packages and the contracts of several directories.
// Load builds it.
type Catalog struct {
	services  map[string]*Files // the owner of catalog/<service>/, by service
	contracts memFS             // every contract file; Load writes it, then it is only read
}

// Service is a service of a Catalog: catalog/<service>/ in the directory
// named Repository. Versions lists its directories, highest first.
type Service struct {
	Name       string
	Repository string
	Versions   []string
}

// Load indexes the catalog that dirs make together. A service is in one
// directory with every version of it; a contract may be in several when
// its bytes are the same in each. The order of dirs changes nothing. The
// error lists every problem at once, the conflicts last, sorted.
func Load(dirs []Files) (*Catalog, error) {
	dirs = slices.SortedFunc(slices.Values(dirs), func(a, b Files) int { return strings.Compare(a.Name, b.Name) })
	c := &Catalog{services: map[string]*Files{}, contracts: memFS{}}
	var errs []error
	owners := map[string][]string{} // a service: the directories that have it
	from := map[string]string{}     // a contract file: the first directory that has it
	for i := range dirs {
		d := &dirs[i]
		services, err := subdirs(*d, CatalogDir)
		errs = append(errs, err)
		for _, s := range services {
			owners[s] = append(owners[s], d.Name)
			c.services[s] = d
		}
		entries, err := fs.ReadDir(d.FS, ContractsDir)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, fmt.Errorf("%s: %w", where(*d, ContractsDir), value.UnwrapPath(err)))
		}
		for _, e := range entries {
			name, ok := strings.CutSuffix(e.Name(), ".schema.json")
			if e.IsDir() || !ok {
				continue
			}
			file := path.Join(ContractsDir, e.Name())
			b, err := fs.ReadFile(d.FS, file)
			switch prev, seen := c.contracts[e.Name()]; {
			case err != nil:
				errs = append(errs, fmt.Errorf("%s: %w", where(*d, file), value.UnwrapPath(err)))
			case !seen:
				c.contracts[e.Name()] = b
				from[e.Name()] = d.Name
			case !bytes.Equal(prev, b):
				errs = append(errs, fmt.Errorf("contract %s differs between the repositories %s and %s", name, from[e.Name()], d.Name))
			}
		}
	}
	for _, s := range slices.Sorted(maps.Keys(owners)) {
		if names := owners[s]; len(names) > 1 {
			errs = append(errs, fmt.Errorf("service %q is in the repositories %s and %s", s, strings.Join(names[:len(names)-1], ", "), names[len(names)-1]))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return c, nil
}

// Services returns every service of the catalog, sorted by name.
func (c *Catalog) Services() ([]Service, error) {
	var out []Service
	var errs []error
	for _, name := range slices.Sorted(maps.Keys(c.services)) {
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
// no directory holds it.
func (c *Catalog) Service(name string) (*Service, error) {
	d, err := c.dir(name)
	if err != nil {
		return nil, err
	}
	versions, err := subdirs(*d, path.Join(CatalogDir, name))
	if err != nil {
		return nil, err
	}
	versions = slices.DeleteFunc(versions, func(v string) bool { return !chart.IsFileName(v) })
	sortVersions(versions)
	return &Service{Name: name, Repository: d.Name, Versions: versions}, nil
}

// Package returns the files of a package. The error wraps ErrNotInCatalog
// when it does not exist. A directory that cannot be read for another
// reason (a permission denied) gives its own error.
func (c *Catalog) Package(service, version string) (fs.FS, error) {
	d, dir, err := c.packageDir(service, version)
	if err != nil {
		return nil, err
	}
	return fs.Sub(d.FS, dir)
}

// PackagePath returns where the package is, <directory>/catalog/<service>/<version>,
// for messages.
func (c *Catalog) PackagePath(service, version string) string {
	if d, ok := c.services[service]; ok {
		return where(*d, path.Join(CatalogDir, service, version))
	}
	return path.Join(CatalogDir, service, version)
}

// Contracts returns the contracts of every directory, as
// <contract>.schema.json files. A compile reads them as Inputs.Contracts.
func (c *Catalog) Contracts() fs.FS { return c.contracts }

func (c *Catalog) dir(service string) (*Files, error) {
	d, ok := c.services[service]
	if !ok {
		return nil, &notInCatalog{fmt.Sprintf("service %q is in the catalog of no repository", service)}
	}
	return d, nil
}

// packageDir returns the directory of a package and the service's
// directory, which holds it.
func (c *Catalog) packageDir(service, version string) (*Files, string, error) {
	d, err := c.dir(service)
	if err != nil {
		return nil, "", err
	}
	dir := path.Join(CatalogDir, service, version)
	info, err := fs.Stat(d.FS, dir)
	switch {
	case !chart.IsFileName(version) || errors.Is(err, fs.ErrNotExist) || err == nil && !info.IsDir():
		return nil, "", &notInCatalog{fmt.Sprintf("version %q of service %q is not in %s", version, service, where(*d, path.Join(CatalogDir, service)))}
	case err != nil:
		return nil, "", fmt.Errorf("version %q of service %q: %s: %w", version, service, where(*d, dir), value.UnwrapPath(err))
	}
	return d, dir, nil
}

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

// subdirs returns the directories of dir, none when dir does not exist.
func subdirs(d Files, dir string) ([]string, error) {
	entries, err := fs.ReadDir(d.FS, dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", where(d, dir), value.UnwrapPath(err))
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

func where(d Files, p string) string { return path.Join(d.Name, p) }
