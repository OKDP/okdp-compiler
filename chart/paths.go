// This file parses the protect and append paths of the upstream section of
// okdp.yaml. A path names keys of a chart's values.
//
// Keys joined by dots cannot name a key that itself contains a dot, and
// many keys the platform sets do: annotations, Spark properties,
// environment maps. So a key may be written in brackets, as one literal
// key, and may end with a glob to name a family of keys:
//
//	path    = segment *( "." key / bracket )
//	segment = key / bracket                  the first key: plain or bracketed
//	key     = 1*( any character but . [ ] " \ * )
//	bracket = "[" DQUOTE 1*char [ "*" ] DQUOTE "]"
//	        / "[" DQUOTE "*" DQUOTE "]"
//	char    = any character but " \ *
//
// No key may hold a quote, a backslash or a star, except the star of a
// glob. A glob names every key that starts with the text before the star,
// that text itself included, and ["*"] names every key. Any bracketed key
// of a path may be a glob, not only the last. Examples:
//
//	ingress.hosts
//	filer.ingresses.http.annotations["nginx.ingress.kubernetes.io/auth-*"]
//	config["spark.ui.filters"]
//	config["spark.history.*"]
package chart

import (
	"errors"
	"regexp"

	"github.com/okdp/okdp-compiler/value"
)

// PathSeg is one key of a path.
type PathSeg struct {
	Key  string // the key, or the prefix of a glob
	Glob bool   // every key that starts with Key
}

// pathKey matches the key at the start of a path. A bracketed key captures
// its text and its glob star; a plain key captures itself.
var pathKey = regexp.MustCompile(`^(?:\["([^"\\*]*)(\*?)"\]|([^.\[\]"\\*]+))`)

var errPath = errors.New(`not a path: keys joined by dots, a key with a dot in brackets (a["b.c"]), a family of keys with a final * (a["prefix*"])`)

// ParsePath returns the keys of a protect or append path (see the grammar
// above).
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

// FormatPath joins keys into a path, as an error message shows it: plain
// keys joined by dots, a key with a special character in brackets.
// ParsePath reads the path back as the same literal keys, unless a key
// holds a quote, a backslash or a star.
func FormatPath(keys []string) string {
	p := ""
	for _, k := range keys {
		p = value.Key(p, k)
	}
	return p
}
