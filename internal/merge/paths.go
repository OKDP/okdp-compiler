// This file matches the protect and append paths of the upstream section
// of okdp.yaml against values. A path names keys of a chart's values:
// plain keys joined by dots, a literal key in brackets when it contains a
// dot, and a bracketed key that ends with * (a glob) for every key with
// that prefix:
//
//	service.ports
//	filer.servers.http.annotations["example.org/auth-*"]
//	config["spark.ui.filters"]
//	config["spark.history.*"]
//
// The grammar, and the reason for the brackets, are in chart/paths.go
// (chart.ParsePath).
package merge

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/okdp/okdp-compiler/chart"
)

// matchKeys returns the keys of m that the segment s names, sorted: its
// key when m has it, or every key that starts with a glob's prefix.
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

// protectedAt returns the keys of the first value of m that a protected
// path reaches, nil when there is none. That is a value at the path
// itself, or a non-map on the way to it, which would replace what the
// platform set under it.
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

// appendLists appends every list of user at the path segs to the list of
// computed at the same keys. When computed holds no list there (nothing,
// or any other value), the user's list is kept as is.
//
// at holds the keys of user walked so far, for the errors. done holds the
// keys of the lists already appended, in order, so that two paths matching
// the same key (a glob and a literal key) append it once.
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
