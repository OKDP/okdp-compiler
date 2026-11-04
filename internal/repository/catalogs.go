package repository

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"

	"github.com/okdp/okdp-compiler/internal/chart"
	"github.com/okdp/okdp-compiler/internal/value"
)

// CatalogsFile: the catalogs of a tree, beside PlatformFile, which the
// program that fetches them reads (okdp-gitops): their repositories
// (CatalogRepository), and the console's menu of their services
// (MenuCategory). Load checks it when a repository of the tree holds it
// (checkCatalogs).
const CatalogsFile = "catalogs.yaml"

// DefaultCatalogRef: the ref of a catalog that names none.
const DefaultCatalogRef = "main"

// Catalogs: CatalogsFile, read (ParseCatalogs).
type Catalogs struct {
	Repositories []CatalogRepository `json:"repositories,omitempty" yaml:"repositories,omitempty"`
	Categories   []MenuCategory      `json:"categories,omitempty" yaml:"categories,omitempty"`
}

// CatalogRepository: a catalog of CatalogsFile, a repository of
// packages (catalog/<service>/<version>/, contracts/), a compile's
// RoleCatalog, at a ref (a branch, a tag or a commit), DefaultCatalogRef
// when it names none.
type CatalogRepository struct {
	Name string `json:"name" yaml:"name"`
	URL  string `json:"url" yaml:"url"`
	Ref  string `json:"ref,omitempty" yaml:"ref,omitempty"`
}

// MenuCategory: a section of the console's menu, in the order of the
// file: its title, its icon, its services, each listed once in the menu.
type MenuCategory struct {
	Title    string        `json:"title" yaml:"title"`
	Icon     string        `json:"icon,omitempty" yaml:"icon,omitempty"`
	Services []MenuService `json:"services" yaml:"services"`
}

// MenuService: a service of the menu, a service of the catalogs: its
// description, the version the console offers (Default, the highest of
// its catalog when empty), its label, whether it exposes a UI. Its
// versions are the directories of its catalog.
type MenuService struct {
	Name        string `json:"name" yaml:"name"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
	Default     string `json:"default,omitempty" yaml:"default,omitempty"`
	Label       string `json:"label,omitempty" yaml:"label,omitempty"`
	ExposesUI   *bool  `json:"exposesUI,omitempty" yaml:"exposesUI,omitempty"`
}

// ParseCatalogs: CatalogsFile, doc as decoded (chart.ParseValues),
// strictly: repositories, a list of {name, url, ref}, each a name and a
// url, the names unique, the ref DefaultCatalogRef when it names none;
// categories, a list of {title, icon, services}, each a title, the titles
// unique, its services a list of {name, description, default, label,
// exposesUI}, each a name, listed once in the menu; any other key, at any
// level, refused, and a value of another type. Every problem at once, one
// line each, in the order of the file.
func ParseCatalogs(doc map[string]any) (*Catalogs, error) {
	p := &parser{}
	out := &Catalogs{}
	p.keys("", doc, "repositories", "categories")
	repos := p.list("repositories", doc["repositories"])
	names := map[string]bool{}
	for i, item := range repos {
		at := value.Index("repositories", i)
		m := p.object(at, item, "name", "url", "ref")
		if m == nil {
			continue
		}
		r := CatalogRepository{Name: p.str(at, m, "name", true), URL: p.str(at, m, "url", true), Ref: p.str(at, m, "ref", false)}
		if r.Name != "" && names[r.Name] {
			p.fail("%s: two catalogs are named %q", at, r.Name)
		}
		names[r.Name] = true
		if r.Ref == "" {
			r.Ref = DefaultCatalogRef
		}
		out.Repositories = append(out.Repositories, r)
	}
	titles, services := map[string]bool{}, map[string]string{}
	for i, item := range p.list("categories", doc["categories"]) {
		at := value.Index("categories", i)
		m := p.object(at, item, "title", "icon", "services")
		if m == nil {
			continue
		}
		c := MenuCategory{Title: p.str(at, m, "title", true), Icon: p.str(at, m, "icon", false), Services: []MenuService{}}
		if c.Title != "" && titles[c.Title] {
			p.fail("%s: two categories are titled %q", at, c.Title)
		}
		titles[c.Title] = true
		if _, ok := m["services"]; !ok {
			p.fail("%s: services is required, a list of {name, description, default, label, exposesUI}", at)
		}
		for j, item := range p.list(at+".services", m["services"]) {
			sat := value.Index(at+".services", j)
			sm := p.object(sat, item, "name", "description", "default", "label", "exposesUI")
			if sm == nil {
				continue
			}
			s := MenuService{Name: p.str(sat, sm, "name", true), Description: p.str(sat, sm, "description", false), Default: p.str(sat, sm, "default", false), Label: p.str(sat, sm, "label", false)}
			if v, set := sm["exposesUI"]; set && v != nil {
				if b, ok := v.(bool); ok {
					s.ExposesUI = &b
				} else {
					p.fail("%s.exposesUI: a boolean, got %s", sat, value.Short(v))
				}
			}
			if first, twice := services[s.Name]; s.Name != "" && twice {
				p.fail("%s: service %q is listed twice in the menu, at %s too: a service is in one category", sat, s.Name, first)
			} else if s.Name != "" {
				services[s.Name] = sat
			}
			c.Services = append(c.Services, s)
		}
		out.Categories = append(out.Categories, c)
	}
	if err := errors.Join(p.errs...); err != nil {
		return nil, err
	}
	return out, nil
}

// parser: the problems of a document read strictly, in order.
type parser struct{ errs []error }

func (p *parser) fail(format string, args ...any) {
	p.errs = append(p.errs, fmt.Errorf(format, args...))
}

// keys: the keys of m at at, each one of allowed, in key order.
func (p *parser) keys(at string, m map[string]any, allowed ...string) {
	for _, k := range slices.Sorted(maps.Keys(m)) {
		if !slices.Contains(allowed, k) {
			p.fail("%s: unknown key: %s only", value.Key(at, k), strings.Join(allowed, ", "))
		}
	}
}

// list: v at at, a list, nil when absent or null.
func (p *parser) list(at string, v any) []any {
	if v == nil {
		return nil
	}
	l, ok := v.([]any)
	if !ok {
		p.fail("%s: a list, got %s", at, value.Short(v))
	}
	return l
}

// object: v at at, an object of the keys allowed, nil when it is none.
func (p *parser) object(at string, v any, allowed ...string) map[string]any {
	m, ok := v.(map[string]any)
	if !ok {
		p.fail("%s: an object {%s}, got %s", at, strings.Join(allowed, ", "), value.Short(v))
		return nil
	}
	p.keys(at, m, allowed...)
	return m
}

// str: the key k of m at at, a string, trimmed, "" when absent or null;
// required, a problem when empty.
func (p *parser) str(at string, m map[string]any, k string, required bool) string {
	v, set := m[k]
	s, ok := v.(string)
	switch {
	case set && v != nil && !ok:
		p.fail("%s: a string, got %s", value.Key(at, k), value.Short(v))
	case required && strings.TrimSpace(s) == "":
		p.fail("%s: %s is required", at, k)
	}
	return strings.TrimSpace(s)
}

// checkCatalogs: CatalogsFile of repo, when it holds one: read strictly
// (ParseCatalogs), each service of its menu one of the catalogs of the
// tree, its default version one of the service's; every problem, the
// file named.
func (t *tree) checkCatalogs(repo Repository) []error {
	b, err := fs.ReadFile(repo.FS, CatalogsFile)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	at := where(repo, CatalogsFile)
	if err != nil {
		return []error{fmt.Errorf("%s: %w", at, unwrapPath(err))}
	}
	doc, err := chart.ParseValues(b)
	if err != nil {
		return []error{fmt.Errorf("%s: %w", at, err)}
	}
	c, err := ParseCatalogs(doc)
	if err != nil {
		return []error{fmt.Errorf("%s: %w", at, err)}
	}
	return t.menu(c, at)
}

// menu: the problems of the menu of c, of the file at: each service one
// of the catalogs of t, its default version one of the service's.
func (t *tree) menu(c *Catalogs, at string) []error {
	var errs []error
	for i, cat := range c.Categories {
		for j, s := range cat.Services {
			sat := value.Index(value.Index("categories", i)+".services", j)
			if _, ok := t.services[s.Name]; !ok {
				errs = append(errs, fmt.Errorf("%s: %s: service %q is in no catalog (catalog/%s/ of no repository)", at, sat, s.Name, s.Name))
				continue
			}
			if s.Default == "" {
				continue
			}
			if _, _, err := t.packageDir(s.Name, s.Default); err != nil {
				errs = append(errs, fmt.Errorf("%s: %s.default: %w", at, sat, err))
			}
		}
	}
	return errs
}

// CheckMenu: nil when every service of the menu of c is one of the
// catalog's, its default version one of the service's (as Load checks
// CatalogsFile); else every problem, each naming CatalogsFile.
func (c *Catalog) CheckMenu(m *Catalogs) error {
	return errors.Join(c.t.menu(m, CatalogsFile)...)
}
