package compiler

import (
	"github.com/okdp/okdp-compiler/internal/okdp"
	"github.com/okdp/okdp-compiler/internal/repository"
	ruleset "github.com/okdp/okdp-compiler/internal/rules"
	"github.com/okdp/okdp-compiler/internal/tree"
)

// What the programs that write a tree's output and edit its files need of
// its compile and its layout.
type (
	// GlobalLayer: the global a layer of a project sets.
	GlobalLayer = okdp.GlobalLayer
	// GlobalsInput: what Inputs.CheckGlobals checks.
	GlobalsInput = okdp.GlobalsInput
	// KeepSync: what a writer tells Argo CD of the objects Helm keeps.
	KeepSync = ruleset.KeepSync
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
	// SecretStore: the platform's secret store in a namespace.
	SecretStore = okdp.SecretStore
	// NamespaceError: a namespace no project may have.
	NamespaceError = okdp.NamespaceError
)

// The file of a tree's catalogs, and the ref of a catalog that names none.
const (
	CatalogsFile      = repository.CatalogsFile
	DefaultCatalogRef = repository.DefaultCatalogRef
)

// What the host's policy reads of the rule set.
const (
	TableDeniedUpstreamKeys  = ruleset.TableDeniedUpstreamKeys
	TableImageKeySuffixes    = ruleset.TableImageKeySuffixes
	TableHostAnnotations     = ruleset.TableHostAnnotations
	TableIngressValueKeys    = ruleset.TableIngressValueKeys
	TableIngressHostKeys     = ruleset.TableIngressHostKeys
	TableIngressHostListKeys = ruleset.TableIngressHostListKeys
	CheckUpstreamKey         = ruleset.CheckUpstreamKey
	CheckHost                = ruleset.CheckHost
)

// DefaultRules: the standard rule set. Temporary: the hosts will give it.
func DefaultRules() *RuleSet { return ruleset.Default() }

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

// PlatformOf: the platform's settings of the context of in, as a compile
// checks them; an error when the namespace may be no project's.
func PlatformOf(in Inputs) (map[string]any, error) { return okdp.PlatformOf(in) }

// ProjectGroups: the roles of the groups of the project of in.
func ProjectGroups(in Inputs) (map[string]AccessRole, error) { return okdp.ProjectGroups(in) }

// SecretStoreOf: the platform's secret store in settings, nil without one.
func SecretStoreOf(in Inputs, settings map[string]any) (*SecretStore, error) {
	return okdp.SecretStoreOf(in, settings)
}

// Privileged: whether namespace is one of the platform's own projects.
func Privileged(settings map[string]any, namespace string) bool {
	return okdp.Privileged(settings, namespace)
}

// ParseAccess: the roles of the groups of a project, its file's access.
func ParseAccess(v any, namespace string) (map[string]AccessRole, error) {
	return okdp.ParseAccess(v, namespace)
}

// PartialWith: Partial, the instances of also refused too, each for its
// error.
func PartialWith(t *Tree, also map[*TreeInstance]error) (results []*Result, g *Graph, kept []KeptProject) {
	return tree.PartialWith(t, also)
}
