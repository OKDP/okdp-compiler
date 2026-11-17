// Package testrules gives the tests of this module the rule set they
// compile with: the compiler's mandatory one.
package testrules

import ruleset "github.com/okdp/okdp-compiler/internal/rules"

// Standard returns the compiler's mandatory rule set.
func Standard() *ruleset.RuleSet { return ruleset.Mandatory() }
