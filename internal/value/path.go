// Paths: where a value is in values, as messages name it: plain keys joined
// by dots, a list item [i], a key a dot would split in brackets. The same
// form as the protect and append paths of okdp.yaml, which chart's
// parsePath reads back as these literal keys (but a key with a quote, a
// backslash or a star): env[1].name,
// annotations["nginx.ingress.kubernetes.io/auth-type"], ["a.b"].
package value

import (
	"strconv"
	"strings"
)

// specialKey: the characters a plain key of a path cannot contain: a key
// holding one is written in brackets.
const specialKey = `.[]"\*`

// Key: the path p followed by the key k: p.k, k alone when p is empty. A
// key that is empty or holds a special character (specialKey) is written
// in brackets, ["..."], as it is, joined without a dot:
// config["spark.ui.filters"]; an empty key, which no protect path names, is
// still shown.
func Key(p, k string) string {
	switch {
	case k == "" || strings.ContainsAny(k, specialKey):
		return p + `["` + k + `"]`
	case p == "":
		return k
	}
	return p + "." + k
}

// Index: the path p followed by the item i of a list: env[2].
func Index(p string, i int) string {
	return p + "[" + strconv.Itoa(i) + "]"
}

// Split: the path p as Key and Index write it, its keys and indexes in
// order, an index as its digits (a JSON pointer's tokens): env[2].name is
// env, 2, name; config["spark.ui.filters"] is config, spark.ui.filters.
// "" is none.
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

// Join: the path of tokens, a JSON pointer's (Split), as Key and Index
// write it, a token of digits an index.
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

// Pointer: the JSON pointer (RFC 6901) of tokens, each "/" and its "~"
// and "/" escaped: /env/2/name; "" for none, the whole document.
func Pointer(tokens []string) string {
	var b strings.Builder
	for _, t := range tokens {
		b.WriteByte('/')
		b.WriteString(strings.NewReplacer("~", "~0", "/", "~1").Replace(t))
	}
	return b.String()
}
