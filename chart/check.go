// Checks the compile applies to values itself: no template code.

package chart

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/okdp/okdp-compiler/value"
)

// UpstreamCheck is the platform's policy on instance upstream values: it
// refuses values of a chart beyond what the package's protect paths
// refuse. user holds the instance's upstream values for the chart, and
// values the computed values they are applied over, which the check does
// not change. at turns a path of user (value.Key, value.Index) into the
// name a message shows: upstream.<key>.<path>.
type UpstreamCheck func(user, values M, at func(p string) string) error

// NoTemplate fails when a key or a string of v contains "{{", at any
// depth. Values reach a chart as data, but some charts pass them to tpl,
// where template code could call lookup and read the cluster's Secrets.
// It names the first occurrence, in path order, with at(p). A value of a
// type that values cannot hold fails too, because it cannot be checked.
func NoTemplate(v any, at func(p string) string) error {
	return noTemplate(v, "", at)
}

func noTemplate(v any, p string, at func(string) string) error {
	switch t := v.(type) {
	case nil, bool, float64:
	case string:
		if strings.Contains(t, "{{") {
			return fmt.Errorf("%s: the value contains \"{{\"", at(p))
		}
	case M:
		for _, k := range slices.Sorted(maps.Keys(t)) {
			kp := value.Key(p, k)
			if strings.Contains(k, "{{") {
				return fmt.Errorf("%s: the key contains \"{{\"", at(kp))
			}
			if err := noTemplate(t[k], kp, at); err != nil {
				return err
			}
		}
	case []any:
		for i, x := range t {
			if err := noTemplate(x, value.Index(p, i), at); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("%s: cannot be checked for \"{{\": %T is not a values type", at(p), v)
	}
	return nil
}
