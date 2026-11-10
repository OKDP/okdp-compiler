package compiler

import (
	"github.com/okdp/okdp-compiler/internal/okdp"
	"github.com/okdp/okdp-compiler/internal/repository"
	"github.com/okdp/okdp-compiler/internal/tree"
)

// What the programs that write a tree's output and edit its files need of
// its compile and its layout.
type (
	// InstanceSpec: an instance.yaml, as ParseInstance reads it.
	InstanceSpec = repository.InstanceSpec
	// CatalogRepository: a repository of catalogs.yaml.
	CatalogRepository = repository.CatalogRepository
	// Catalogs: catalogs.yaml, as ParseCatalogs reads it.
	Catalogs = repository.Catalogs
	// MenuCategory: a category of the console's menu.
	MenuCategory = repository.MenuCategory
	// MenuService: a service of the console's menu.
	MenuService = repository.MenuService
	// Project: what the platform decides of a project (ProjectOf).
	Project = okdp.Project
	// SecretStore: the platform's secret store in a namespace.
	SecretStore = okdp.SecretStore
	// AccessError: the roles of a project's groups cannot be read.
	AccessError = okdp.AccessError
	// NamespaceError: a namespace no project may have.
	NamespaceError = okdp.NamespaceError
)

// The file of a tree's catalogs, and the ref of a catalog that names none.
const (
	CatalogsFile      = repository.CatalogsFile
	DefaultCatalogRef = repository.DefaultCatalogRef
)

// ParseInstance: an instance.yaml.
func ParseInstance(raw []byte) (InstanceSpec, error) { return repository.ParseInstance(raw) }

// ParseCatalogs: catalogs.yaml, as read.
func ParseCatalogs(doc map[string]any) (*Catalogs, error) { return repository.ParseCatalogs(doc) }

// Roles: the roles of a project's groups, least first.
func Roles() []AccessRole { return okdp.Roles() }

// EngineNamespaces: the namespaces of the deployer engines, which no
// project may have.
func EngineNamespaces() []string { return okdp.EngineNamespaces() }

// PlatformObjectNames: the names of the objects the platform writes in the
// namespace of every project.
func PlatformObjectNames() []string { return okdp.PlatformObjectNames() }

// CheckProject: nil when the instance's namespace may be a project's.
func CheckProject(in Inputs) error { return okdp.CheckProject(in) }

// ProjectOf: what the platform decides of the project of an instance.
func ProjectOf(in Inputs) (Project, error) { return okdp.ProjectOf(in) }

// PartialWith: Partial, the instances of also refused too, each for its
// error.
func PartialWith(t *Tree, also map[*TreeInstance]error) (results []*Result, g *Graph, kept []KeptProject) {
	return tree.PartialWith(t, also)
}
