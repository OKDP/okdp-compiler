// Paths: the protect and append paths of service.yaml, which name keys of a
// chart's values.
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
// A bracketed key ending with * is a glob: it names every key that starts
// with the text before the star (the text itself included; ["*"] names
// every key). Any bracketed key of a path may be a glob, the last or one on
// the way. Examples:
//
//	ingress.hosts
//	filer.ingresses.http.annotations["nginx.ingress.kubernetes.io/auth-*"]
//	config["spark.ui.filters"]
//	config["spark.history.*"]
package chart

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/okdp/okdp-compiler/internal/value"
)

// pathSeg: one key of a path.
type pathSeg struct {
	key  string // the key, or the prefix of a glob
	glob bool   // every key that starts with key
}

// pathKey: the key at the start of a path: bracketed (its text, its glob)
// or plain.
var pathKey = regexp.MustCompile(`^(?:\["([^"\\*]*)(\*?)"\]|([^.\[\]"\\*]+))`)

// errPath: the error of any malformed path.
var errPath = errors.New(`not a path: keys joined by dots, a key with a dot in brackets (a["b.c"]), a family of keys with a final * (a["prefix*"])`)

// parsePath: the keys of a protect or append path (see the grammar above).
// service.yaml is checked with it when it is loaded (Service.check), so a
// malformed path fails before any compile, not when an instance first sets
// a value under it.
func parsePath(p string) ([]pathSeg, error) {
	var segs []pathSeg
	for rest := p; ; {
		m := pathKey.FindStringSubmatch(rest)
		if m == nil || m[1]+m[2]+m[3] == "" {
			return nil, errPath
		}
		segs = append(segs, pathSeg{key: m[1] + m[3], glob: m[2] == "*"})
		switch rest = rest[len(m[0]):]; {
		case rest == "":
			return segs, nil
		case rest[0] == '.' && len(rest) > 1 && rest[1] != '[':
			rest = rest[1:]
		case rest[0] != '[':
			return nil, errPath
		}
	}
}

// formatPath: the path of these keys, as an error message shows the key a
// value was refused at (value.Key): plain keys joined by dots, a key with a
// special character in brackets
// (annotations["nginx.ingress.kubernetes.io/auth-type"]). parsePath reads
// it back as these literal keys, unless one holds a quote, a backslash or a
// star.
func formatPath(keys []string) string {
	p := ""
	for _, k := range keys {
		p = value.Key(p, k)
	}
	return p
}

// matchKeys: the keys of m a segment names, sorted: its key when m has it,
// or every key that starts with a glob's prefix.
func matchKeys(m M, s pathSeg) []string {
	if !s.glob {
		if _, ok := m[s.key]; ok {
			return []string{s.key}
		}
		return nil
	}
	var keys []string
	for k := range m {
		if strings.HasPrefix(k, s.key) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

// protectedAt: the keys of the first value of m a protected path reaches:
// a value at the path, or a non-map on the way to it, which would replace
// what the platform set under it. nil when there is none.
func protectedAt(m M, segs []pathSeg) []string {
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
// errors; done: the lists already appended, so that two paths matching the
// same key (a glob and a key) append it once.
func appendLists(user M, computed any, segs []pathSeg, at []string, src source, done map[string]bool) error {
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
		p := formatPath(keys)
		if done[p] {
			continue
		}
		add, ok := user[k].([]any)
		if !ok {
			return fmt.Errorf("%s must be a list", src.at(p))
		}
		base, _ := cm[k].([]any)
		user[k] = append(append([]any{}, base...), add...)
		done[p] = true
	}
	return nil
}
