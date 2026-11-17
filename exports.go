package compiler

import (
	"context"

	"github.com/okdp/okdp-compiler/chart"
	"github.com/okdp/okdp-compiler/internal/merge"
	"github.com/okdp/okdp-compiler/internal/okdp"
	"github.com/okdp/okdp-compiler/internal/repository"
	ruleset "github.com/okdp/okdp-compiler/internal/rules"
	"github.com/okdp/okdp-compiler/internal/tree"
)

// The types that a program that writes or edits a tree needs.
type (
	// GlobalLayer is the global that a layer of a project sets.
	GlobalLayer = okdp.GlobalLayer
	// GlobalsInput is what Inputs.CheckGlobals checks.
	GlobalsInput = okdp.GlobalsInput
	// ChartCaches holds the chart caches of a set of compiles.
	ChartCaches = merge.Caches
	// KeepSync tells Argo CD's sync options for the objects Helm keeps.
	KeepSync = ruleset.KeepSync
	// InstanceSpec is an instance.yaml, as ParseInstance reads it.
	InstanceSpec = repository.InstanceSpec
	// CatalogRepository is a repository of catalogs.yaml.
	CatalogRepository = repository.CatalogRepository
	// Catalogs is catalogs.yaml, as ParseCatalogs reads it.
	Catalogs = repository.Catalogs
	// MenuCategory is a category of the console's menu.
	MenuCategory = repository.MenuCategory
	// MenuService is a service of the console's menu.
	MenuService = repository.MenuService
	// SecretStore is the platform's secret store in a namespace.
	SecretStore = okdp.SecretStore
	// NamespaceError reports a namespace that no project may have.
	NamespaceError = okdp.NamespaceError
)

// The file of a tree's catalogs, and the ref of a catalog that names none.
const (
	CatalogsFile      = repository.CatalogsFile
	DefaultCatalogRef = repository.DefaultCatalogRef
)

// The tables and checks of the rule set that a host's policy reads.
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

// FinalValues returns Helm's merge of the final values of in, as a
// compile does without a Finalize; a Finalize calls it to add a policy.
// check tells what to refuse of the upstream values; nil refuses nothing.
func FinalValues(caches *ChartCaches, in FinalInput, check chart.UpstreamCheck) (map[string]map[string]any, map[string]ChartRef, []string, error) {
	return merge.FinalValues(caches.Get(in.Charts), in.ChartFailures, in.Files, in.Specs, in.ValuesFile, in.Output, in.Values, check)
}

// CheckObjects returns nil when the objects of an instance of svc in
// namespace are allowed to a project that is not privileged: each is in
// that namespace or names none, and none is cluster-scoped or of an
// engine's API group. A host calls it for the objects it adds itself.
func CheckObjects(ctx context.Context, rules *RuleSet, svc, namespace string, objects []map[string]any) error {
	return okdp.CheckObjects(ctx, rules, svc, namespace, objects)
}

// CheckIngressHosts returns nil when the objects of an instance of svc in
// namespace serve only the hosts that a project that is not privileged
// may serve. settings are the platform's (Result.Settings). It reads the
// hosts of Ingresses, Certificates, Gateway API routes and listeners, and
// of host annotations. It refuses an Ingress that serves every host.
func CheckIngressHosts(ctx context.Context, rules *RuleSet, svc, namespace string, settings map[string]any, objects []map[string]any) error {
	return okdp.CheckIngressHosts(ctx, rules, svc, namespace, settings, objects)
}

// ParseInstance reads an instance.yaml.
func ParseInstance(raw []byte) (InstanceSpec, error) { return repository.ParseInstance(raw) }

// ParseCatalogs reads catalogs.yaml from its decoded document.
func ParseCatalogs(doc map[string]any) (*Catalogs, error) { return repository.ParseCatalogs(doc) }

// Roles returns the roles of a project's groups, least first.
func Roles(rules *RuleSet) []AccessRole { return okdp.Roles(rules) }

// EngineNamespaces returns the engines' namespaces, reserved to them.
func EngineNamespaces(rules *RuleSet) []string { return okdp.EngineNamespaces(rules) }

// PlatformObjectNames returns the names of the objects that the platform
// writes in the namespace of every project.
func PlatformObjectNames(rules *RuleSet) []string { return okdp.PlatformObjectNames(rules) }

// CheckProject returns nil when the namespace of in may be a project's.
func CheckProject(ctx context.Context, in Inputs) error { return okdp.CheckProject(ctx, in) }

// PlatformOf returns the platform's settings of the context of in, as a
// compile checks them. It fails when the namespace of in is no project's.
func PlatformOf(ctx context.Context, in Inputs) (map[string]any, error) {
	return okdp.PlatformOf(ctx, in)
}

// ProjectGroups returns the roles of the groups of the project of in.
func ProjectGroups(in Inputs) (map[string]AccessRole, error) { return okdp.ProjectGroups(in) }

// SecretStoreOf returns the secret store that settings declare, or nil.
func SecretStoreOf(ctx context.Context, in Inputs, settings map[string]any) (*SecretStore, error) {
	return okdp.SecretStoreOf(ctx, in, settings)
}

// Privileged reports whether namespace is a project of the platform's own.
func Privileged(settings map[string]any, namespace string) bool {
	return okdp.Privileged(settings, namespace)
}

// ParseAccess reads v, a project's access, as the roles of its groups.
func ParseAccess(rules *RuleSet, v any, namespace string) (map[string]AccessRole, error) {
	return okdp.ParseAccess(rules, v, namespace)
}

// PartialWith works as Partial, and also treats each instance of also as
// refused, for the error it maps to.
func PartialWith(t *Tree, also map[*TreeInstance]error) (results []*Result, g *Graph, kept []KeptProject) {
	return tree.PartialWith(t, also)
}
