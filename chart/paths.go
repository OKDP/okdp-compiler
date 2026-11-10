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
	"regexp"

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
