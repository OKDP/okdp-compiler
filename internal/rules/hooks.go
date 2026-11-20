// Hooks are the points where the compiler calls the documents a rule set
// lists (hooks in rules.yaml): each gives errors, the first refusing. They
// let a set refuse what the compiler's own checks do not, on what the
// compiler walks: each object of an instance, and the final values of each
// chart.

package rules

import (
	"context"
	"fmt"
	"maps"
	"slices"
)

// The hooks of a rule set.
const (
	// HookObjects is called on each object of an instance of a project that
	// is not privileged, the items of a List too, right after check object.
	// Input: svc, ns, object (as check object reads it), settings.
	HookObjects = "objects"
	// HookFinalValues is called on the final values of each chart key of an
	// instance of a project that is not privileged, keys in order. Input:
	// svc, ns, key, value (the key's values), settings.
	HookFinalValues = "finalValues"
)

// ruleHooks lists the names of the hooks, sorted.
var ruleHooks = []string{HookFinalValues, HookObjects}

// loadHooks reads the hooks of a set. Each names documents of the sets
// read so far, this one's among them. A document is listed once per hook.
func (r *RuleSet) loadHooks(hooks M) error {
	if err := knownKeys("hooks", hooks, ruleHooks); err != nil {
		return err
	}
	for _, hook := range slices.Sorted(maps.Keys(hooks)) {
		for _, name := range stringList(hooks[hook]) {
			if _, ok := r.sources[name]; !ok || slices.Contains(ruleChecks, name) {
				return fmt.Errorf("%s: hooks: %s: %s is none of documents", RulesFile, hook, name)
			}
			if slices.Contains(r.hooks[hook], name) {
				return taken("hooks", hook+": "+name)
			}
			if r.hooks == nil {
				r.hooks = map[string][]string{}
			}
			r.hooks[hook] = append(r.hooks[hook], name)
		}
	}
	return nil
}

// Hooks returns the documents of the hook, in the order the sets list
// them. The caller must not change the list.
func (r *RuleSet) Hooks(hook string) []string {
	if r == nil {
		return nil
	}
	return r.hooks[hook]
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
