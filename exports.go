package compiler

import (
	"context"

	"github.com/okdp/okdp-compiler/chart"
	"github.com/okdp/okdp-compiler/internal/merge"
	"github.com/okdp/okdp-compiler/internal/okdp"
	ruleset "github.com/okdp/okdp-compiler/internal/rules"
)

// The types that a program that writes or edits a tree needs.
type (
	// GlobalLayer is the global that a layer of a project sets.
	GlobalLayer = okdp.GlobalLayer
	// GlobalsInput is what Inputs.CheckGlobals checks.
	GlobalsInput = okdp.GlobalsInput
	// FinalValuesOf is Helm's merge of the final values of a compile
	// (FinalValues): the values, the chart of each key and the lists the
	// instance appended to.
	FinalValuesOf = merge.Final
	// KeepSync tells Argo CD's sync options for the objects Helm keeps.
	KeepSync = ruleset.KeepSync
	// SecretStore is the platform's secret store in a namespace.
	SecretStore = okdp.SecretStore
	// NamespaceError reports a namespace that no project may have.
	NamespaceError = okdp.NamespaceError
)

// FinalValues returns Helm's merge of the final values of in, as a
// compile does without a Finalize; a Finalize calls it to add a policy and
// the checks against the upstream charts, which the compiler does not
// read (FinalValuesOf.Appended). check tells what to refuse of the
// upstream values; nil refuses nothing.
func FinalValues(in FinalInput, check chart.UpstreamCheck) (FinalValuesOf, error) {
	return merge.FinalValues(in.Files, in.Specs, in.ValuesFile, in.Output, in.Values, check)
}

// CheckObjects returns nil when the objects of an instance of svc in
// namespace are allowed to a project that is not privileged: each is in
// that namespace or names none, none is cluster-scoped or of an engine's
// API group, and the rule set's objects hook passes on each. settings are
// the platform's (Result.Settings), which the hook reads. A host calls it
// for the objects it adds itself.
func CheckObjects(ctx context.Context, rules *RuleSet, svc, namespace string, settings map[string]any, objects []map[string]any) error {
	return okdp.CheckObjects(ctx, rules, svc, namespace, settings, objects)
}

// CheckFinalValues returns nil when the rule set's finalValues hook
// passes on the final values of each chart key of an instance of svc in
// namespace, a project that is not privileged; keys in order, the first
// message refusing. settings are the platform's (Result.Settings). A
// compile runs it on the values its Finalize returns; a host calls it on
// final values it reads outside a compile.
func CheckFinalValues(ctx context.Context, rules *RuleSet, svc, namespace string, settings map[string]any, values map[string]map[string]any) error {
	return okdp.CheckFinalValues(ctx, rules, svc, namespace, settings, values)
}

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
