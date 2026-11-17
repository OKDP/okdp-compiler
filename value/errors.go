package value

import "strings"

// FieldError tags an error of a compile with the field of the instance's
// values it is about, as messages name it (Key, Index):
// catalogs[1].storage. The tag changes no message: the wrapped error's
// text already starts with the path.
type FieldError struct {
	Field string
	Err   error
}

func (e *FieldError) Error() string { return e.Err.Error() }
func (e *FieldError) Unwrap() error { return e.Err }

// AtField tags err with field. It returns nil for a nil err.
func AtField(field string, err error) error {
	if err == nil {
		return nil
	}
	return &FieldError{Field: field, Err: err}
}

// CredentialLiteral reports whether v is written in clear: a number, or
// a non-empty string that holds no template, neither {{ }} nor ${ }. Such
// a value is a credential when its key is sensitive.
func CredentialLiteral(v any) bool {
	switch v := v.(type) {
	case string:
		return strings.TrimSpace(v) != "" && !strings.Contains(v, "{{") && !strings.Contains(v, "${")
	case int, int64, uint64, float64:
		return true
	}
	return false
}

// CredentialReference reports whether key, which is sensitive by its name
// only (Sensitive), names a credential rather than holds one:
// existingSecret, passwordSecretRef, tokenKey. secretKey and accessKey
// hold a credential, so they are not references.
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
