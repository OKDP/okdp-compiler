// The names and versions of the sets a rule set is made of, and the
// constraints a tree puts on them (global.okdp.ruleSets).

package rules

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"
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
	if _, err := parseVersion(version); err != nil {
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
	v, err := parseVersion(version)
	if err != nil || !match(v) {
		return fmt.Errorf("the platform requires the rule set %s %s, the compile was given its version %s: give a matching one with okdp-compile --rules (compiler.LoadRules)", name, constraint, version)
	}
	return nil
}

// semver is a version MAJOR.MINOR.PATCH with an optional -prerelease, in
// the precedence of Semantic Versioning 2.0.0.
type semver struct {
	major, minor, patch uint64
	pre                 []string
}

const versionForm = "MAJOR.MINOR.PATCH, with an optional -prerelease"

// parseVersion reads s, a version.
func parseVersion(s string) (semver, error) {
	core, pre, hasPre := strings.Cut(s, "-")
	parts := strings.Split(core, ".")
	var v semver
	ok := len(parts) == 3
	for i, p := range parts {
		if !ok {
			break
		}
		var n uint64
		n, ok = number(p)
		switch i {
		case 0:
			v.major = n
		case 1:
			v.minor = n
		case 2:
			v.patch = n
		}
	}
	if ok && hasPre {
		v.pre = strings.Split(pre, ".")
		for _, id := range v.pre {
			if id == "" || strings.Trim(id, "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz-") != "" {
				ok = false
			} else if _, numeric := number(id); !numeric && isDigits(id) {
				ok = false // a numeric identifier with a leading zero
			}
		}
	}
	if !ok {
		return semver{}, fmt.Errorf("%q is no version %s", s, versionForm)
	}
	return v, nil
}

// number reads s, digits without a leading zero.
func number(s string) (uint64, bool) {
	if !isDigits(s) || len(s) > 1 && s[0] == '0' {
		return 0, false
	}
	n, err := strconv.ParseUint(s, 10, 64)
	return n, err == nil
}

func isDigits(s string) bool {
	return s != "" && strings.Trim(s, "0123456789") == ""
}

// compareVersions returns -1, 0 or 1 as a precedes, equals or follows b.
func compareVersions(a, b semver) int {
	if c := cmp.Or(cmp.Compare(a.major, b.major), cmp.Compare(a.minor, b.minor), cmp.Compare(a.patch, b.patch)); c != 0 {
		return c
	}
	switch {
	case len(a.pre) == 0 && len(b.pre) == 0:
		return 0
	case len(a.pre) == 0:
		return 1
	case len(b.pre) == 0:
		return -1
	}
	for i := range min(len(a.pre), len(b.pre)) {
		x, xNum := number(a.pre[i])
		y, yNum := number(b.pre[i])
		var c int
		switch {
		case xNum && yNum:
			c = cmp.Compare(x, y)
		case xNum:
			c = -1
		case yNum:
			c = 1
		default:
			c = strings.Compare(a.pre[i], b.pre[i])
		}
		if c != 0 {
			return c
		}
	}
	return cmp.Compare(len(a.pre), len(b.pre))
}

// The operators of a comparator, the two-character ones first.
var operators = []string{">=", "<=", "!=", "=", ">", "<", "~", "^"}

const constraintForm = "comparators separated by commas, each =, !=, >, >=, <, <=, ~ or ^ followed by a version " + versionForm + ", or a version alone"

// parseConstraint reads s, comparators separated by commas, and returns
// what reports whether a version meets all of them. An empty s accepts any
// version. A version alone stands for =; ~ accepts the versions from its
// own on, of the same major and minor; ^ those of the same major, or of the
// same minor when the major is 0.
func parseConstraint(s string) (func(semver) bool, error) {
	if strings.TrimSpace(s) == "" {
		return func(semver) bool { return true }, nil
	}
	var all []func(semver) bool
	for c := range strings.SplitSeq(s, ",") {
		c = strings.TrimSpace(c)
		op := "="
		for _, o := range operators {
			if rest, ok := strings.CutPrefix(c, o); ok {
				op, c = o, strings.TrimSpace(rest)
				break
			}
		}
		want, err := parseVersion(c)
		if err != nil {
			return nil, fmt.Errorf("%w: a constraint is %s", err, constraintForm)
		}
		all = append(all, comparator(op, want))
	}
	return func(v semver) bool {
		for _, f := range all {
			if !f(v) {
				return false
			}
		}
		return true
	}, nil
}

// comparator returns the test of op against want.
func comparator(op string, want semver) func(semver) bool {
	return func(v semver) bool {
		c := compareVersions(v, want)
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
			return c >= 0 && v.major == want.major && v.minor == want.minor
		case "^":
			if want.major == 0 {
				return c >= 0 && v.major == 0 && v.minor == want.minor
			}
			return c >= 0 && v.major == want.major
		}
		return c == 0
	}
}
