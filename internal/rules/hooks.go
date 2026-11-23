// Hooks are the points where the compiler calls the documents the rule
// sets list (hooks in rules.yaml): each gives errors, the first refusing.
// They are how a set refuses anything: a project's namespace and release,
// each object of an instance, and the final values of each chart. The
// compiler calls them for every project, and gives each document what it
// needs to decide alone: the settings among them.

package rules

import (
	"context"
	"fmt"
	"maps"
	"slices"
)

// The hooks of a rule set. settings is global.okdp, checked against the
// platform schema, its defaults applied; a host that calls a hook outside
// a compile may give it as written.
const (
	// HookNamespace is called on the namespace of every instance, once the
	// settings are read, and by a host on a namespace it creates. Input:
	// field (where the namespace is given, for the message), ns, settings.
	HookNamespace = "namespace"
	// HookRelease is called on the release of every instance, once the
	// settings are read. Input: field, release, settings.
	HookRelease = "release"
	// HookObjects is called on each object of an instance, the items of a
	// List too. Input: svc, ns, object (its id, kind, name, apiVersion,
	// group and ns as text, raw the object itself), settings,
	// allowPlainSecrets (Inputs.AllowPlainSecrets).
	HookObjects = "objects"
	// HookFinalValues is called on the final values of each chart key of an
	// instance, keys in order. Input: svc, ns, key, value (the key's values),
	// settings.
	HookFinalValues = "finalValues"
)

// loadHooks reads the hooks of a set. Each names documents of the sets
// read so far, this one's among them. A document is listed once per hook.
// rules.schema.json allows the hooks above, no other.
func (r *RuleSet) loadHooks(hooks M) error {
	for _, hook := range slices.Sorted(maps.Keys(hooks)) {
		for _, name := range stringList(hooks[hook]) {
			if _, ok := r.sources[name]; !ok {
				return fmt.Errorf("%s: hooks: %s: %s is none of documents", RulesFile, hook, name)
			}
			if slices.Contains(r.hooks[hook], name) {
				return r.taken("hooks", hook+": "+name)
			}
			r.hooks[hook] = append(r.hooks[hook], name)
		}
	}
	return nil
}

// RunHook evaluates each document of the hook on input, in order. It
// returns the first message as a *Failure, as Refuse does, and nil when
// every document passes. The message is the document's own: the caller
// adds nothing to it. A document that fails to evaluate gives another
// error, and the zero RuleSet fails (ErrNotLoaded).
func (r *RuleSet) RunHook(ctx context.Context, hook string, input M) error {
	if !r.Loaded() {
		return fmt.Errorf("the rule set: %w", ErrNotLoaded)
	}
	for _, name := range r.hooks[hook] {
		if err := r.Refuse(ctx, name, input); err != nil {
			return err
		}
	}
	return nil
}

// Hooks returns the documents of the hook, in the order the sets list
// them. The caller must not change the list. A rule set's own tests read
// it to check what the set registers.
func (r *RuleSet) Hooks(hook string) []string {
	if r == nil {
		return nil
	}
	return r.hooks[hook]
}
