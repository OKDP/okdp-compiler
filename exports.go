package compiler

import (
	"context"

	"github.com/okdp/okdp-compiler/chart"
	"github.com/okdp/okdp-compiler/internal/merge"
	"github.com/okdp/okdp-compiler/internal/okdp"
)

// The types that a program that writes or edits a tree needs.
type (
	// FinalValuesOf is Helm's merge of the final values of a compile
	// (FinalValues): the values, the chart of each key and the lists the
	// instance appended to.
	FinalValuesOf = merge.Final
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

// CheckObjects returns nil when the rule sets' objects hook passes on
// each object of an instance of svc in namespace, the items of a List
// too: with the security set, each is in that namespace or names none,
// none is cluster-scoped or of an engine's API group, but in a privileged
// project, and no Secret holds a credential in clear unless
// allowPlainSecrets. settings are the platform's (Result.Settings), which
// the hook reads. A compile runs it on its objects; a host calls it for
// the objects it adds itself.
func CheckObjects(ctx context.Context, rules *RuleSet, svc, namespace string, settings map[string]any, allowPlainSecrets bool, objects []map[string]any) error {
	return okdp.CheckObjects(ctx, rules, svc, namespace, settings, allowPlainSecrets, objects)
}

// CheckFinalValues returns nil when the rule sets' finalValues hook
// passes on the final values of each chart key of an instance of svc in
// namespace; keys in order, the first message refusing. settings are the platform's (Result.Settings). A
// compile runs it on the values its Finalize returns; a host calls it on
// final values it reads outside a compile.
func CheckFinalValues(ctx context.Context, rules *RuleSet, svc, namespace string, settings map[string]any, values map[string]map[string]any) error {
	return okdp.CheckFinalValues(ctx, rules, svc, namespace, settings, values)
}

// SettingsOf returns the settings (global.okdp) of the values of in,
// with in.Settings, as a compile checks them, with their defaults
// applied. The error is a
// *NamespaceError when the namespace of in is refused.
func SettingsOf(ctx context.Context, in Inputs) (map[string]any, error) {
	return okdp.SettingsOf(ctx, in)
}
