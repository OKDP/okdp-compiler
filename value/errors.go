package value

import "strings"

// FieldError: an error of a compile about one field of the instance's
// values, its parameter path as the messages name it (value.Key,
// value.Index: catalogs[1].storage, claims.analytics): a connection
// reference that does not resolve, a claim that does not bind, a named
// claim of the context. Its message is the error's, the path at its
// head: the tag changes no message.
type FieldError struct {
	Field string
	Err   error
}

func (e *FieldError) Error() string { return e.Err.Error() }
func (e *FieldError) Unwrap() error { return e.Err }

// AtField: err tagged with field, nil for nil.
func AtField(field string, err error) error {
	if err == nil {
		return nil
	}
	return &FieldError{Field: field, Err: err}
}

// CredentialLiteral: v is written as it is, a credential when its key is
// sensitive: a string that is not empty and holds no template ({{ }},
// Helm's or External Secrets', nor ${ }, a reference of an environment
// variable), or a number. A boolean, null, a map or a list is not one.
func CredentialLiteral(v any) bool {
	switch v := v.(type) {
	case string:
		return strings.TrimSpace(v) != "" && !strings.Contains(v, "{{") && !strings.Contains(v, "${")
	case int, int64, uint64, float64:
		return true
	}
	return false
}

// CredentialReference: key is sensitive (value.Sensitive) by its name
// only, and names a credential rather than holds one: an existing
// Secret (existingSecret, existingSecretPasswordKey), a reference
// (passwordSecretRef, tokenRef), or the key of a Secret that holds one
// (adminPasswordKey, tokenKey; not secretKey, accessKey, which hold one).
func CredentialReference(key string) bool {
	lower := strings.ToLower(strings.TrimSuffix(key, "s"))
	if strings.HasPrefix(lower, "existing") || strings.HasSuffix(lower, "ref") {
		return true
	}
	for _, sep := range []string{"key", "_key", "-key"} {
		stem, ok := strings.CutSuffix(lower, sep)
		if !ok || lower == "secretkey" || lower == "secret_key" || lower == "secret-key" {
			continue
		}
		for _, s := range []string{"password", "passwd", "token", "secret"} {
			if strings.HasSuffix(stem, s) {
				return true
			}
		}
	}
	return false
}
