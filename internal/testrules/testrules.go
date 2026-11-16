// Package testrules gives this module's tests a rule set to compile with:
// testdata/rules, a copy of okdp-gitops' standard set (the compiler holds
// none). Only tests import it.
package testrules

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"

	ruleset "github.com/okdp/okdp-compiler/internal/rules"
)

// Dir: the directory of the rule set.
func Dir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "testdata", "rules")
}

// Standard: the rule set of testdata/rules, read once.
func Standard() *ruleset.RuleSet { return standard() }

// standard: Standard's rule set, read once.
var standard = sync.OnceValue(func() *ruleset.RuleSet {
	r, err := ruleset.LoadRules(os.DirFS(Dir()))
	if err != nil {
		panic(err)
	}
	return r
})
