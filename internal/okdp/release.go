package okdp

import (
	"context"

	ruleset "github.com/okdp/okdp-compiler/internal/rules"
)

// checkRelease runs the rule sets' release hook on release, with the
// platform's settings: a host's set refuses the names of the objects it
// writes in every namespace; the mandatory set refuses none.
func checkRelease(ctx context.Context, rules *RuleSet, field, release string, okdp M) error {
	return rules.RunHook(ctx, ruleset.HookRelease, M{"field": field, "release": release, "settings": settingsOr(okdp)})
}
