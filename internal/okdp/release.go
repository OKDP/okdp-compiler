package okdp

import (
	"context"

	ruleset "github.com/okdp/okdp-compiler/internal/rules"
)

// PlatformObjectNames lists the names of the objects that the targets
// write in the namespace of every project, besides those of its
// instances: the okdp-project Role and NetworkPolicy, the bindings of
// the control plane and of the groups, the defaults of the secret store.
// The names that the platform's settings give the store are added to
// them. A test checks the targets' constants against this list.
func PlatformObjectNames(rules *RuleSet) []string {
	return rulesOr(rules).List(ruleset.TablePlatformObjectNames)
}

// checkRelease returns nil when release is not the name of one of the
// platform's objects, nor the prefix of one up to a "-".
func checkRelease(ctx context.Context, rules *RuleSet, field, release string, okdp M) error {
	return rules.Refuse(ctx, ruleset.CheckReleaseRule, M{"field": field, "release": release, "settings": okdp})
}
