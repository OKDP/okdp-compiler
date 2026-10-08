// Paths: where a value is in values, as messages name it: plain keys joined
// by dots, a list item [i], a key a dot would split in brackets. The same
// form as the protect and append paths of service.yaml, which chart's
// parsePath reads back as these literal keys: env[1].name,
// annotations["nginx.ingress.kubernetes.io/auth-type"], ["a.b"].
package value

import (
	"strconv"
	"strings"
)

// specialKey: the characters a plain key of a path cannot contain: a key
// holding one is written in brackets.
const specialKey = `.[]"\*`

// keyEscape: the characters a bracketed key escapes with a backslash.
var keyEscape = strings.NewReplacer(`\`, `\\`, `"`, `\"`, `*`, `\*`)

// Key: the path p followed by the key k: p.k, k alone when p is empty. A
// key that is empty or holds a special character (specialKey) is written
// in brackets, ["..."], its \ " and * escaped, joined without a dot:
// config["spark.ui.filters"]; an empty key, which no protect path names, is
// still shown.
func Key(p, k string) string {
	switch {
	case k == "" || strings.ContainsAny(k, specialKey):
		return p + `["` + keyEscape.Replace(k) + `"]`
	case p == "":
		return k
	}
	return p + "." + k
}

// Index: the path p followed by the item i of a list: env[2].
func Index(p string, i int) string {
	return p + "[" + strconv.Itoa(i) + "]"
}
