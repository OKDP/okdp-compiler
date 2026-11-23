package value

import (
	"slices"
	"strings"
)

// Redacted is what a message prints in place of a sensitive value. An
// error is logged and copied into issues, so it must hold no credential.
const Redacted = "<redacted>"

// sensitiveKeys lists the keys whose values are credentials, in lowercase.
var sensitiveKeys = []string{"password", "passwd", "secret", "token", "apikey", "api_key", "accesskey", "secretkey", "credential", "privatekey", "clientsecret"}

// sensitiveSuffixes lists the endings of such keys: dbPassword, apiToken.
var sensitiveSuffixes = []string{"password", "passwd", "secret", "token"}

// Sensitive reports whether the value of key may be a credential, for
// which a message prints Redacted. Ignoring case and a plural's final s,
// the key is one of sensitiveKeys, ends with one of sensitiveSuffixes, or
// ends with Key, _key or -key (accessKey, but not monkey).
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

// SensitivePath reports whether the value at path is sensitive. The key
// checked is the last element that is not a list index: a list item has
// no key of its own, so it takes its list's.
func SensitivePath(path []string) bool {
	for _, elem := range slices.Backward(path) {
		if elem != "" && strings.Trim(elem, "0123456789") == "" {
			continue
		}
		return Sensitive(elem)
	}
	return false
}

// redact returns a copy of v for a message. At any depth, the value of a
// Sensitive key of a map becomes Redacted, whatever it is.
func redact(v any) any {
	switch v := v.(type) {
	case M:
		out := make(M, len(v))
		for k, x := range v {
			if Sensitive(k) {
				out[k] = Redacted
			} else {
				out[k] = redact(x)
			}
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, x := range v {
			out[i] = redact(x)
		}
		return out
	}
	return v
}

// ShortAt formats the value v of key for a message: Redacted when key is
// Sensitive, else Short(v).
func ShortAt(key string, v any) string {
	if Sensitive(key) {
		return Redacted
	}
	return Short(v)
}
