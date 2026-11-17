// Package testrules gives the tests of this module the rule set of
// testdata/rules, a copy of okdp-gitops' standard set.
package testrules

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"

	ruleset "github.com/okdp/okdp-compiler/internal/rules"
)

// Dir returns the directory of the rule set.
func Dir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "testdata", "rules")
}

// Standard returns the rule set, read once. It panics when it does not load.
func Standard() *ruleset.RuleSet { return standard() }

var standard = sync.OnceValue(func() *ruleset.RuleSet {
	r, err := ruleset.LoadRules(os.DirFS(Dir()))
	if err != nil {
		panic(err)
	}
	return r
})
