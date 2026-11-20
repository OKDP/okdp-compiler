// The names and versions of the sets a rule set is made of, and the
// constraints a tree puts on them (global.okdp.ruleSets).

package rules

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/okdp/okdp-compiler/chart"
)

// CoreSet is the name of the compiler's mandatory rule set.
const CoreSet = "core"

// namedSet is a set of a rule set: its name and its version.
type namedSet struct{ name, version string }

// addSet records the name and the version of a set read. The first set,
// the mandatory one, is CoreSet; every set added to it names itself, with a
// name no set read before has. Each version is MAJOR.MINOR.PATCH, with an
// optional -prerelease, so that a tree can put a constraint on it.
func (r *RuleSet) addSet(name, version string) error {
	if err := checkVersion(version); err != nil {
		return fmt.Errorf("%s: version: %w", RulesFile, err)
	}
	switch {
	case len(r.named) == 0 && name != CoreSet:
		return fmt.Errorf("%s: name: the mandatory rule set is %s, not %q", RulesFile, CoreSet, name)
	case name == "":
		return fmt.Errorf("%s: name: required: a rule set added to the mandatory one names itself", RulesFile)
	case slices.ContainsFunc(r.named, func(s namedSet) bool { return s.name == name }):
		return taken("name", name)
	}
	r.named = append(r.named, namedSet{name, version})
	return nil
}

// Names returns the names of the sets the rule set is made of, in the
// order they were read: CoreSet first.
func (r *RuleSet) Names() []string {
	if r == nil {
		return nil
	}
	out := make([]string, 0, len(r.named))
	for _, s := range r.named {
		out = append(out, s.name)
	}
	return out
}

// SetVersion returns the version of the set name, and false when the rule
// set holds no set of that name.
func (r *RuleSet) SetVersion(name string) (string, bool) {
	if r != nil {
		for _, s := range r.named {
			if s.name == name {
				return s.version, true
			}
		}
	}
	return "", false
}

// Require checks that the rule set holds the set name, of a version that
// constraint accepts; an empty constraint accepts any. The error names the
// set, and how a compile is given one (okdp-compile --rules).
func (r *RuleSet) Require(name, constraint string) error {
	match, err := parseConstraint(constraint)
	if err != nil {
		return fmt.Errorf("the rule set %s: version %q: %w", name, constraint, err)
	}
	version, ok := r.SetVersion(name)
	if !ok {
		return fmt.Errorf("the platform requires the rule set %s, which the compile was not given: give it with okdp-compile --rules (compiler.LoadRules)", name)
	}
	if !match(version) {
		return fmt.Errorf("the platform requires the rule set %s %s, the compile was given its version %s: give a matching one with okdp-compile --rules (compiler.LoadRules)", name, constraint, version)
	}
	return nil
}

const versionForm = "MAJOR.MINOR.PATCH, with an optional -prerelease"

// versionPattern matches a version of Semantic Versioning 2.0.0 without build
// metadata: no number has a leading zero, so that two equal numbers are
// the same string.
var versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)(\.(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*)?$`)

// checkVersion refuses s when it is no version.
func checkVersion(s string) error {
	if !versionPattern.MatchString(s) {
		return fmt.Errorf("%q is no version %s", s, versionForm)
	}
	return nil
}

// sameCore reports whether the versions a and b have the same first n
// numbers of MAJOR.MINOR.PATCH.
func sameCore(a, b string, n int) bool {
	return slices.Equal(strings.SplitN(a, ".", 3)[:n], strings.SplitN(b, ".", 3)[:n])
}

// The operators of a comparator, the two-character ones first.
var operators = []string{">=", "<=", "!=", "=", ">", "<", "~", "^"}

const constraintForm = "comparators separated by commas, each =, !=, >, >=, <, <=, ~ or ^ followed by a version " + versionForm + ", or a version alone"

// parseConstraint reads s, comparators separated by commas, and returns
// what reports whether a version meets all of them. An empty s accepts any
// version. A version alone stands for =; ~ accepts the versions from its
// own on, of the same major and minor; ^ those of the same major, or of the
// same minor when the major is 0.
func parseConstraint(s string) (func(string) bool, error) {
	if strings.TrimSpace(s) == "" {
		return func(string) bool { return true }, nil
	}
	var all []func(string) bool
	for c := range strings.SplitSeq(s, ",") {
		c = strings.TrimSpace(c)
		op := "="
		for _, o := range operators {
			if rest, ok := strings.CutPrefix(c, o); ok {
				op, c = o, strings.TrimSpace(rest)
				break
			}
		}
		if err := checkVersion(c); err != nil {
			return nil, fmt.Errorf("%w: a constraint is %s", err, constraintForm)
		}
		all = append(all, comparator(op, c))
	}
	return func(v string) bool {
		for _, f := range all {
			if !f(v) {
				return false
			}
		}
		return true
	}, nil
}

// comparator returns the test of op against want.
func comparator(op, want string) func(string) bool {
	return func(v string) bool {
		c := chart.CompareVersions(v, want)
		switch op {
		case "!=":
			return c != 0
		case ">":
			return c > 0
		case ">=":
			return c >= 0
		case "<":
			return c < 0
		case "<=":
			return c <= 0
		case "~":
			return c >= 0 && sameCore(v, want, 2)
		case "^":
			if strings.HasPrefix(want, "0.") {
				return c >= 0 && sameCore(v, want, 2)
			}
			return c >= 0 && sameCore(v, want, 1)
		}
		return c == 0
	}
}
