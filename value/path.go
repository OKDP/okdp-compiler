// This file writes and reads paths, as messages name them. Keys are joined
// by dots, a list item is [i], and a key with a dot is in brackets:
//
//	env[1].name
//	annotations["example.org/auth-type"]
//	["a.b"]
//
// The protect and append paths of okdp.yaml have the same form.

package value

import (
	"strconv"
	"strings"
)

// specialKey lists the characters that put a key in brackets.
const specialKey = `.[]"\*`

// Key appends the key k to the path p: p.k, or p["k"] when k is empty or
// holds a character of specialKey.
func Key(p, k string) string {
	switch {
	case k == "" || strings.ContainsAny(k, specialKey):
		return p + `["` + k + `"]`
	case p == "":
		return k
	}
	return p + "." + k
}

// Index appends the list index i to the path p: env[2].
func Index(p string, i int) string {
	return p + "[" + strconv.Itoa(i) + "]"
}

// Split returns the keys and indexes of a path written by Key and Index,
// as the tokens of a JSON pointer: an index is its digits.
func Split(p string) []string {
	var out []string
	for p != "" {
		switch {
		case strings.HasPrefix(p, `["`):
			k, rest, ok := strings.Cut(p[2:], `"]`)
			if !ok {
				return append(out, p)
			}
			out, p = append(out, k), rest
		case strings.HasPrefix(p, "["):
			i, rest, ok := strings.Cut(p[1:], "]")
			if !ok {
				return append(out, p)
			}
			out, p = append(out, i), rest
		default:
			p = strings.TrimPrefix(p, ".")
			end := strings.IndexAny(p, ".[")
			if end < 0 {
				end = len(p)
			}
			out, p = append(out, p[:end]), p[end:]
		}
	}
	return out
}

// Join is the reverse of Split. A token made of digits is an index.
func Join(tokens []string) string {
	p := ""
	for _, t := range tokens {
		if i, err := strconv.Atoi(t); err == nil && i >= 0 && strconv.Itoa(i) == t {
			p = Index(p, i)
		} else {
			p = Key(p, t)
		}
	}
	return p
}

// Pointer writes tokens as a JSON pointer (RFC 6901): /env/2/name. No
// token gives "", the pointer to the whole document.
func Pointer(tokens []string) string {
	var b strings.Builder
	for _, t := range tokens {
		b.WriteByte('/')
		b.WriteString(strings.NewReplacer("~", "~0", "/", "~1").Replace(t))
	}
	return b.String()
}
