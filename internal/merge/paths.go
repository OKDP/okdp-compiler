// Paths: the protect and append paths of okdp.yaml's upstream, which name
// keys of a chart's values.
//
// A path was a list of keys separated by dots, so it could not name a key
// that contains a dot, and many keys the platform sets do: annotations
// (nginx.ingress.kubernetes.io/auth-url), Spark properties
// (spark.ui.filters), environment maps. A service had to refuse those in
// its mapping's errors section instead. A key may now be written in
// brackets, as one literal key, and end with a glob, for a family of keys
// (every auth-* annotation):
//
//	path    = segment *( "." key / bracket )
//	segment = key / bracket                  the first key: plain or bracketed
//	key     = 1*( any character but . [ ] " \ * )
//	bracket = "[" DQUOTE 1*char [ "*" ] DQUOTE "]"
//	        / "[" DQUOTE "*" DQUOTE "]"
//	char    = any character but " \ *
//
// A plain key is literal and contains no glob. A bracketed key is literal,
// dots included; no key holds a quote, a backslash or a star but a glob's.
// A bracketed key ending with * is a Glob: it names every key that starts
// with the text before the star (the text itself included; ["*"] names
// every key). Any bracketed key of a path may be a glob, the last or one on
// the way. Examples:
//
//	ingress.hosts
//	filer.ingresses.http.annotations["nginx.ingress.kubernetes.io/auth-*"]
//	config["spark.ui.filters"]
//	config["spark.history.*"]
package merge

import (
	"fmt"
	"github.com/okdp/okdp-compiler/chart"
	"slices"
	"sort"
	"strings"
)

// matchKeys: the keys of m a segment names, sorted: its key when m has it,
// or every key that starts with a glob's prefix.
func matchKeys(m M, s chart.PathSeg) []string {
	if !s.Glob {
		if _, ok := m[s.Key]; ok {
			return []string{s.Key}
		}
		return nil
	}
	var keys []string
	for k := range m {
		if strings.HasPrefix(k, s.Key) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

// protectedAt: the keys of the first value of m a protected path reaches:
// a value at the path, or a non-map on the way to it, which would replace
// what the platform set under it. nil when there is none.
func protectedAt(m M, segs []chart.PathSeg) []string {
	for _, k := range matchKeys(m, segs[0]) {
		sub, isMap := m[k].(M)
		if len(segs) == 1 || !isMap {
			return []string{k}
		}
		if at := protectedAt(sub, segs[1:]); at != nil {
			return append([]string{k}, at...)
		}
	}
	return nil
}

// appendLists: every list of user at the path segs, appended to the list of
// computed at the same keys (computed may hold anything there, or nothing:
// the user's list is then kept as is). at: the keys of user, for the
// errors; done: the keys of the lists already appended, in order, so that
// two paths matching the same key (a glob and a key) append it once.
func appendLists(user M, computed any, segs []chart.PathSeg, at []string, src source, done *[][]string) error {
	cm, _ := computed.(M)
	for _, k := range matchKeys(user, segs[0]) {
		keys := append(append([]string{}, at...), k)
		if len(segs) > 1 {
			if sub, ok := user[k].(M); ok {
				if err := appendLists(sub, cm[k], segs[1:], keys, src, done); err != nil {
					return err
				}
			}
			continue
		}
		if slices.ContainsFunc(*done, func(d []string) bool { return slices.Equal(d, keys) }) {
			continue
		}
		add, ok := user[k].([]any)
		if !ok {
			return fmt.Errorf("%s must be a list", src.at(chart.FormatPath(keys)))
		}
		base, _ := cm[k].([]any)
		user[k] = append(append([]any{}, base...), add...)
		*done = append(*done, keys)
	}
	return nil
}
