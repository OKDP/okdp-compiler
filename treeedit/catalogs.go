package treeedit

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	compiler "github.com/okdp/okdp-compiler"
)

// CatalogRepository is a catalog of CatalogsFile: a repository of
// packages (catalog/<service>/<version>/, contracts/), a compile's
// compiler.RoleCatalog, at a ref (a branch, a tag or a commit).
type CatalogRepository struct {
	Name string `json:"name" yaml:"name"`
	URL  string `json:"url" yaml:"url"`
	Ref  string `json:"ref,omitempty" yaml:"ref,omitempty"`
}

// DefaultCatalogRef is the ref of a catalog that names none.
const DefaultCatalogRef = "main"

// CatalogRepositories reads the repositories key of CatalogsFile (doc,
// as decoded): each needs a name and a url, and the names are unique; a
// ref defaults to DefaultCatalogRef.
func CatalogRepositories(doc map[string]any) ([]CatalogRepository, error) {
	raw, ok := doc["repositories"]
	if !ok || raw == nil {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, errors.New("repositories: a list of {name, url, ref}")
	}
	var out []CatalogRepository
	seen := map[string]bool{}
	for i, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("repositories[%d]: a map {name, url, ref}", i)
		}
		r := CatalogRepository{}
		r.Name, _ = m["name"].(string)
		r.URL, _ = m["url"].(string)
		r.Ref, _ = m["ref"].(string)
		r.Name, r.URL, r.Ref = strings.TrimSpace(r.Name), strings.TrimSpace(r.URL), strings.TrimSpace(r.Ref)
		switch {
		case r.Name == "":
			return nil, fmt.Errorf("repositories[%d]: name is required", i)
		case r.URL == "":
			return nil, fmt.Errorf("repositories[%d] (%s): url is required", i, r.Name)
		case seen[r.Name]:
			return nil, fmt.Errorf("repositories[%d]: two catalogs are named %q", i, r.Name)
		}
		if r.Ref == "" {
			r.Ref = DefaultCatalogRef
		}
		seen[r.Name] = true
		out = append(out, r)
	}
	return out, nil
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
// (MergeDocument), its comments and the keys change leaves alone kept.
// target names the edit (Edit.Target). change may run more than once
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
