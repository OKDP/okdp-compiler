package okdp

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"
)

// TestRequiredRuleSets: a platform states the rule sets it requires
// (global.okdp.ruleSets); every compile, and PlatformOf, refuse it when the
// compile was not given one of them, or one of a version its constraint
// refuses.
func TestRequiredRuleSets(t *testing.T) {
	ctx := context.Background()
	extra, err := LoadRules(fstest.MapFS{"rules.yaml": &fstest.MapFile{Data: []byte("apiVersion: okdp-compiler/rules.v1\nname: extra\nversion: 1.2.0\n")}})
	if err != nil {
		t.Fatal(err)
	}
	requiring := func(rules *RuleSet, sets ...any) Inputs {
		in := inputs(map[string]any{"global": M{"okdp": M{"ruleSets": sets}}})
		in.Rules = rules
		return in
	}
	for name, tc := range map[string]struct {
		in   Inputs
		want string // the error, none when empty
	}{
		"given":          {requiring(extra, M{"name": "extra", "version": "^1.0.0"}, M{"name": "core"}), ""},
		"any version":    {requiring(extra, M{"name": "extra"}), ""},
		"the core alone": {requiring(nil, M{"name": "core", "version": ">=1.0.0, <2.0.0"}), ""},
		"none":           {requiring(nil), ""},
		"not given": {requiring(nil, M{"name": "extra"}),
			"global.okdp.ruleSets: the platform requires the rule set extra, which the compile was not given: give it with okdp-compile --rules (compiler.LoadRules)"},
		"another version": {requiring(extra, M{"name": "extra", "version": "~1.1.0"}),
			"global.okdp.ruleSets: the platform requires the rule set extra ~1.1.0, the compile was given its version 1.2.0: give a matching one with okdp-compile --rules (compiler.LoadRules)"},
		"no constraint": {requiring(extra, M{"name": "extra", "version": "latest"}),
			`global.okdp.ruleSets: the rule set extra: version "latest": "latest" is no version MAJOR.MINOR.PATCH, with an optional -prerelease`},
		"no name":     {requiring(extra, M{"version": "1.2.0"}), "at '/global/okdp/ruleSets/0': missing property 'name'"},
		"another key": {requiring(extra, M{"name": "extra", "other": 1}), "at '/global/okdp/ruleSets/0': additional properties 'other' not allowed"},
	} {
		t.Run(name, func(t *testing.T) {
			_, perr := PlatformOf(ctx, tc.in)
			_, cerr := Compile(ctx, def, tc.in)
			for what, err := range map[string]error{"PlatformOf": perr, "Compile": cerr} {
				if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
					t.Errorf("%s: got %v, want %q", what, err, tc.want)
				}
			}
		})
	}
}
