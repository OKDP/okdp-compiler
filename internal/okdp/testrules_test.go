package okdp

import "github.com/okdp/okdp-compiler/internal/testrules"

// The compiles of this package's tests give no rule set and read the standard
// one.
func init() { defaultRules = testrules.Standard }
