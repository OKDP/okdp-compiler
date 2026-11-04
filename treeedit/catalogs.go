package treeedit

import (
	"bytes"
	"errors"
	"fmt"

	compiler "github.com/okdp/okdp-compiler"
	"github.com/okdp/okdp-compiler/internal/repository"
)

// CatalogRepository is a catalog of CatalogsFile: a repository of
// packages (catalog/<service>/<version>/, contracts/), a compile's
// compiler.RoleCatalog, at a ref (a branch, a tag or a commit).
type CatalogRepository = repository.CatalogRepository

// Catalogs is CatalogsFile, read (ParseCatalogs): its Repositories and
// the console's menu, its Categories (MenuCategory: Title, Icon,
// Services), each service a MenuService (Name, Description, Default,
// Label, ExposesUI).
type (
	Catalogs     = repository.Catalogs
	MenuCategory = repository.MenuCategory
	MenuService  = repository.MenuService
)

// DefaultCatalogRef is the ref of a catalog that names none.
const DefaultCatalogRef = repository.DefaultCatalogRef

// ParseCatalogs reads CatalogsFile (doc, as decoded) strictly, as the
// compile reads it (compiler.LoadRepository, a repository of the tree
// that holds it): repositories, each a name and a url, the names unique,
// a ref DefaultCatalogRef by default; categories, each a title, the
// titles unique, its services, each a name, listed once; any other key,
// at any level, or a value of another type, refused. Every problem at
// once.
func ParseCatalogs(doc map[string]any) (*Catalogs, error) { return repository.ParseCatalogs(doc) }

// CatalogRepositories reads the repositories of CatalogsFile (doc, as
// decoded), the whole file checked (ParseCatalogs).
func CatalogRepositories(doc map[string]any) ([]CatalogRepository, error) {
	c, err := ParseCatalogs(doc)
	if err != nil {
		return nil, err
	}
	return c.Repositories, nil
}

// CheckCatalogs refuses CatalogsFile (doc, as decoded) as the compile of
// a tree that holds it does: read strictly (ParseCatalogs), each service
// of its menu one of catalog's (compiler.LoadCatalog of its
// repositories), its default version one of the service's.
func CheckCatalogs(doc map[string]any, catalog *compiler.Catalog) error {
	c, err := ParseCatalogs(doc)
	if err != nil {
		return fmt.Errorf("%s: %w", CatalogsFile, err)
	}
	return catalog.CheckMenu(c)
}

// ReadCatalogRepositories reads the catalogs of the CatalogsFile r holds
// (CatalogRepositories), none without the file.
func ReadCatalogRepositories(r Reader) ([]CatalogRepository, error) {
	doc, _, err := readValues(r, CatalogsFile)
	if err != nil || doc == nil {
		return nil, err
	}
	repos, err := CatalogRepositories(doc)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", CatalogsFile, err)
	}
	return repos, nil
}

// ReadCatalog returns CatalogsFile as JSON-compatible data
// (DecodeValues), or an error wrapping ErrNotFound without the file.
func ReadCatalog(r Reader) (map[string]any, error) {
	raw, err := r.ReadFile(CatalogsFile)
	if err != nil {
		return nil, err
	}
	return DecodeValues(raw)
}

// UpdateCatalog applies change to CatalogsFile as the tree holds it, a
// missing file empty, and writes it back merged key by key
// (MergeDocument), its comments and the keys change leaves alone kept;
// a file ParseCatalogs refuses is not written. target names the edit (Edit.Target). change may run more than once
// (Edit.Change).
func UpdateCatalog(target string, change func(catalog map[string]any) error) Edit {
	return Edit{Action: "update catalog", Target: target, change: func(r Reader) (*Change, error) {
		doc := map[string]any{}
		raw, err := r.ReadFile(CatalogsFile)
		switch {
		case err == nil:
			if doc, err = DecodeValues(raw); err != nil {
				return nil, err
			}
		case errors.Is(err, ErrNotFound):
		default:
			return nil, err
		}
		if err := change(doc); err != nil {
			return nil, err
		}
		if _, err := ParseCatalogs(RoundTrip(doc)); err != nil {
			return nil, fmt.Errorf("%s: %w", CatalogsFile, err)
		}
		data, err := MergeDocument(raw, doc)
		if err != nil {
			return nil, err
		}
		c := &Change{}
		if !bytes.Equal(data, raw) {
			c.write(CatalogsFile, data)
		}
		return c, nil
	}}
}

// ReadPlatformValues returns compiler.PlatformFile as written, or an
// error wrapping ErrNotFound without it.
func ReadPlatformValues(r Reader) ([]byte, error) { return r.ReadFile(compiler.PlatformFile) }
