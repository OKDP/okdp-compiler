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
package chart

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/okdp/okdp-compiler/value"
)

// PathSeg: one key of a path.
type PathSeg struct {
	Key  string // the key, or the prefix of a glob
	Glob bool   // every key that starts with Key
}

// pathKey: the key at the start of a path: bracketed (its text, its glob)
// or plain.
var pathKey = regexp.MustCompile(`^(?:\["([^"\\*]*)(\*?)"\]|([^.\[\]"\\*]+))`)

// errPath: the error of any malformed path.
var errPath = errors.New(`not a path: keys joined by dots, a key with a dot in brackets (a["b.c"]), a family of keys with a final * (a["prefix*"])`)

// ParsePath: the keys of a protect or append path (see the grammar above).
// okdp.yaml is checked with it when it is loaded (Specs.Check), so a
// malformed path fails before any compile, not when an instance first sets
// a value under it.
func ParsePath(p string) ([]PathSeg, error) {
	var segs []PathSeg
	for rest := p; ; {
		m := pathKey.FindStringSubmatch(rest)
		if m == nil || m[1]+m[2]+m[3] == "" {
			return nil, errPath
		}
		segs = append(segs, PathSeg{Key: m[1] + m[3], Glob: m[2] == "*"})
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

// FormatPath: the path of these keys, as an error message shows the key a
// value was refused at (value.Key): plain keys joined by dots, a key with a
// special character in brackets
// (annotations["nginx.ingress.kubernetes.io/auth-type"]). ParsePath reads
// it back as these literal keys, unless one holds a quote, a backslash or a
// star.
func FormatPath(keys []string) string {
	p := ""
	for _, k := range keys {
		p = value.Key(p, k)
	}
	return p
}

// matchKeys: the keys of m a segment names, sorted: its key when m has it,
// or every key that starts with a glob's prefix.
func matchKeys(m M, s PathSeg) []string {
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
func protectedAt(m M, segs []PathSeg) []string {
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
func appendLists(user M, computed any, segs []PathSeg, at []string, src source, done *[][]string) error {
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
			return fmt.Errorf("%s must be a list", src.at(FormatPath(keys)))
		}
		base, _ := cm[k].([]any)
		user[k] = append(append([]any{}, base...), add...)
		*done = append(*done, keys)
	}
	return nil
}
