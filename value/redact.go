package value

import (
	"slices"
	"strings"
)

// Redacted: what a message prints in place of a sensitive value
// (Sensitive): an error is shown to whoever runs the compile, logged by
// CI and by a server, and copied into issues.
const Redacted = "<redacted>"

// sensitiveKeys: the keys whose values are credentials, lowercase.
var sensitiveKeys = []string{"password", "passwd", "secret", "token", "apikey", "api_key", "accesskey", "secretkey", "credential", "privatekey", "clientsecret"}

// sensitiveSuffixes: the endings of the keys whose values are
// credentials, lowercase: dbPassword, db_password, clientSecret, apiToken.
var sensitiveSuffixes = []string{"password", "passwd", "secret", "token"}

// Sensitive: the value of key may be a credential, and a message prints
// Redacted for it: key, a plural's s left out, is one of sensitiveKeys,
// case-insensitive, or ends with one of sensitiveSuffixes,
// case-insensitive, or with Key, _key or -key (accessKey, private_key,
// apiKeys; not monkey). A list item's key is its list's: SensitivePath
// reads it.
func Sensitive(key string) bool {
	key = strings.TrimSuffix(key, "s")
	lower := strings.ToLower(key)
	if slices.Contains(sensitiveKeys, lower) {
		return true
	}
	for _, s := range sensitiveSuffixes {
		if strings.HasSuffix(lower, s) {
			return true
		}
	}
	return strings.HasSuffix(key, "Key") || strings.HasSuffix(lower, "_key") || strings.HasSuffix(lower, "-key")
}

// SensitivePath: the value at path, its elements from the root, is
// Sensitive: its last element that is not a list index (digits), the key
// of the map that holds it or of the list it is in.
func SensitivePath(path []string) bool {
	for _, elem := range slices.Backward(path) {
		if elem != "" && strings.Trim(elem, "0123456789") == "" {
			continue
		}
		return Sensitive(elem)
	}
	return false
}

// Redact: v for a message, in a copy: at any depth, the value of a
// Sensitive key of a map, whatever it is, Redacted; v itself when it is
// neither a map nor a list.
func Redact(v any) any {
	switch v := v.(type) {
	case M:
		out := make(M, len(v))
		for k, x := range v {
			if Sensitive(k) {
				out[k] = Redacted
			} else {
				out[k] = Redact(x)
			}
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, x := range v {
			out[i] = Redact(x)
		}
		return out
	}
	return v
}

// ShortAt: the value v of key for a message (Short), Redacted when key is
// Sensitive, else v with its own sensitive keys redacted.
func ShortAt(key string, v any) string {
	if Sensitive(key) {
		return Redacted
	}
	return Short(v)
}
