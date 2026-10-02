// Paths: the protect and append paths of service.yaml, which name keys of a
// chart's values.
//
// A path was a list of keys separated by dots, so it could not name a key
// that contains a dot, and many keys the platform sets do: annotations
// (nginx.ingress.kubernetes.io/auth-url), Spark properties
// (spark.ui.filters), environment maps. A service had to refuse those in
// its mapping's errors section instead, which saw the inline upstream
// values only. A key may now be written in brackets, as one literal key,
// and end with a glob, for a family of keys (every auth-* annotation):
//
//	path    = segment *( "." key / bracket )
//	segment = key / bracket                  the first key: plain or bracketed
//	key     = 1*( any character but . [ ] " \ * )
//	bracket = "[" DQUOTE 1*( char / escape ) [ "*" ] DQUOTE "]"
//	        / "[" DQUOTE "*" DQUOTE "]"
//	char    = any character but " \ *
//	escape  = "\" ( DQUOTE / "\" / "*" )
//
// A plain key is literal and contains no glob. A bracketed key is literal,
// dots included; \" \\ and \* write a quote, a backslash and a star. A
// bracketed key ending with an unescaped * is a glob: it names every key
// that starts with the text before the star (the text itself included;
// ["*"] names every key). Any bracketed key of a path may be a glob, the
// last or one on the way. Examples:
//
//	ingress.hosts
//	filer.ingresses.http.annotations["nginx.ingress.kubernetes.io/auth-*"]
//	config["spark.ui.filters"]
//	config["spark.history.*"]
package chart

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// pathSeg: one key of a path.
type pathSeg struct {
	key  string // the key, or the prefix of a glob
	glob bool   // every key that starts with key
}

// specialKey: the characters a plain key cannot contain, written in
// brackets in a path.
const specialKey = `.[]"\*`

// parsePath: the keys of a protect or append path (see the grammar above).
// service.yaml is checked with it when it is loaded (Service.check), so a
// malformed path fails before any compile, not when an instance first sets
// a value under it.
func parsePath(p string) ([]pathSeg, error) {
	if p == "" {
		return nil, errors.New("empty path")
	}
	var segs []pathSeg
	i := 0
	for {
		if p[i] == '[' {
			s, n, err := parseBracket(p[i:])
			if err != nil {
				return nil, err
			}
			segs = append(segs, s)
			i += n
		} else {
			j := i
			for j < len(p) && p[j] != '.' && p[j] != '[' {
				switch p[j] {
				case '*':
					return nil, errors.New(`a * outside brackets: a glob is a bracketed key, the whole key in the brackets, dots included: ["prefix*"]`)
				case ']', '"', '\\':
					return nil, fmt.Errorf("%q outside brackets: write the key as [\"...\"]", p[j])
				}
				j++
			}
			if j == i {
				return nil, errors.New("an empty key (two dots, or a dot at an end)")
			}
			segs = append(segs, pathSeg{key: p[i:j]})
			i = j
		}
		switch {
		case i == len(p):
			return segs, nil
		case p[i] == '.':
			i++
			if i == len(p) {
				return nil, errors.New("an empty key (two dots, or a dot at an end)")
			}
			if p[i] == '[' {
				return nil, errors.New("no dot before a bracketed key: a[\"b.c\"], not a.[\"b.c\"]")
			}
		case p[i] != '[':
			// Only after a bracketed key: a plain key ends at "." or "[".
			return nil, fmt.Errorf("%q after a bracketed key: a dot or a bracket follows it", p[i])
		}
		// Next: a plain key after a dot, or a bracketed key.
	}
}

// parseBracket: the bracketed key at the start of s, and the length of its
// text.
func parseBracket(s string) (pathSeg, int, error) {
	if len(s) < 2 || s[1] != '"' {
		return pathSeg{}, 0, errors.New("a bracketed key is quoted: [\"key\"]")
	}
	var b strings.Builder
	glob := false
	for i := 2; i < len(s); i++ {
		switch c := s[i]; c {
		case '\\':
			if i+1 == len(s) || !strings.ContainsRune(`"\*`, rune(s[i+1])) {
				return pathSeg{}, 0, errors.New(`in a bracketed key, \ escapes ", \ or * only`)
			}
			b.WriteByte(s[i+1])
			i++
		case '*':
			if i+1 == len(s) || s[i+1] != '"' {
				return pathSeg{}, 0, errors.New(`a * is a glob at the end of a bracketed key only (\* for a literal star)`)
			}
			glob = true
		case '"':
			if i+1 == len(s) || s[i+1] != ']' {
				return pathSeg{}, 0, errors.New(`a bracketed key ends with "]`)
			}
			if b.Len() == 0 && !glob {
				return pathSeg{}, 0, errors.New("an empty key")
			}
			return pathSeg{key: b.String(), glob: glob}, i + 2, nil
		default:
			b.WriteByte(c)
		}
	}
	return pathSeg{}, 0, errors.New(`a bracketed key ends with "]`)
}

// formatPath: the path of these keys, as an error message shows the key a
// value was refused at: plain keys joined by dots, a key with a special
// character in brackets (annotations["nginx.ingress.kubernetes.io/auth-type"]).
// parsePath reads it back as these literal keys.
func formatPath(keys []string) string {
	var b strings.Builder
	for i, k := range keys {
		if k != "" && !strings.ContainsAny(k, specialKey) {
			if i > 0 {
				b.WriteByte('.')
			}
			b.WriteString(k)
			continue
		}
		r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `*`, `\*`)
		b.WriteString(`["` + r.Replace(k) + `"]`)
	}
	return b.String()
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
