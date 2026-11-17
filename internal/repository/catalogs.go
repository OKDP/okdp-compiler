package repository

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"

	"github.com/okdp/okdp-compiler/chart"
	"github.com/okdp/okdp-compiler/value"
)

// CatalogsFile is the file that lists the catalogs of a tree, beside
// PlatformFile: the catalog repositories and the console's menu of their
// services. Load checks it when a repository of the tree holds it.
const CatalogsFile = "catalogs.yaml"

// DefaultCatalogRef is the ref of a catalog that names none.
const DefaultCatalogRef = "main"

// Catalogs is the content of CatalogsFile.
type Catalogs struct {
	Repositories []CatalogRepository `json:"repositories,omitempty" yaml:"repositories,omitempty"`
	Categories   []MenuCategory      `json:"categories,omitempty" yaml:"categories,omitempty"`
}

// CatalogRepository is a catalog listed in CatalogsFile. Ref is a branch, a
// tag or a commit: DefaultCatalogRef when the file names none.
type CatalogRepository struct {
	Name string `json:"name" yaml:"name"`
	URL  string `json:"url" yaml:"url"`
	Ref  string `json:"ref,omitempty" yaml:"ref,omitempty"`
}

// MenuCategory is a section of the console's menu. The categories keep the
// order of the file. A service is listed once in the whole menu.
type MenuCategory struct {
	Title    string        `json:"title" yaml:"title"`
	Icon     string        `json:"icon,omitempty" yaml:"icon,omitempty"`
	Services []MenuService `json:"services" yaml:"services"`
}

// MenuService is a menu entry for a service of the catalogs. When Default
// is empty, the console offers the highest version of the catalog.
type MenuService struct {
	Name        string `json:"name" yaml:"name"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
	Default     string `json:"default,omitempty" yaml:"default,omitempty"`
	Label       string `json:"label,omitempty" yaml:"label,omitempty"`
	ExposesUI   *bool  `json:"exposesUI,omitempty" yaml:"exposesUI,omitempty"`
}

// ParseCatalogs reads CatalogsFile strictly. doc is the file as decoded
// (chart.ParseValues):
//
//	repositories: {name, url, ref}
//	categories:   {title, icon, services}
//	  services:   {name, description, default, label, exposesUI}
//
// Any other key, at any level, is refused, and so is a value of the wrong
// type. A service is listed once in the menu. The error lists every problem
// at once, one line each, in the order of the file.
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

// parser collects the problems of a document read strictly, in order.
type parser struct{ errs []error }

func (p *parser) fail(format string, args ...any) {
	p.errs = append(p.errs, fmt.Errorf(format, args...))
}

func (p *parser) keys(at string, m map[string]any, allowed ...string) {
	for _, k := range slices.Sorted(maps.Keys(m)) {
		if !slices.Contains(allowed, k) {
			p.fail("%s: unknown key: %s only", value.Key(at, k), strings.Join(allowed, ", "))
		}
	}
}

// list returns v as a list, nil when it is absent or null.
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

// object returns v as an object, nil with a problem when it is not one.
func (p *parser) object(at string, v any, allowed ...string) map[string]any {
	m, ok := v.(map[string]any)
	if !ok {
		p.fail("%s: an object {%s}, got %s", at, strings.Join(allowed, ", "), value.Short(v))
		return nil
	}
	p.keys(at, m, allowed...)
	return m
}

// str returns the string at key k of m, trimmed, "" when absent or null.
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

// checkCatalogs checks the CatalogsFile of repo, when it holds one.
func (t *parts) checkCatalogs(repo Repository) []error {
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

// menu checks the menu of c: each service must be in a catalog, and its
// default version must be a version of it. at names the file in messages.
func (t *parts) menu(c *Catalogs, at string) []error {
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

// CheckMenu checks the menu of m against the catalog, as Load checks
// CatalogsFile. Each problem names CatalogsFile.
func (c *Catalog) CheckMenu(m *Catalogs) error {
	return errors.Join(c.t.menu(m, CatalogsFile)...)
}
