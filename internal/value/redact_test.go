package value

import (
	"reflect"
	"testing"
)

// TestSensitive: the keys whose values a message redacts, whatever their
// case, a plural's s left out: the names of credentials, and the keys
// ending with one, or with Key; not the others.
func TestSensitive(t *testing.T) {
	for _, k := range []string{"password", "Password", "PASSWD", "secret", "token", "tokens", "apiKey", "api_key", "accessKey", "secretKey", "credential", "credentials", "privateKey", "clientSecret", "dbPassword", "db_password", "admin-password", "githubToken", "client_secret", "sshKey", "tls-key", "private_key", "apiKeys"} {
		if !Sensitive(k) {
			t.Errorf("%s: not sensitive", k)
		}
	}
	for _, k := range []string{"realm", "host", "user", "username", "monkey", "keyboard", "tokenizer", "secretRef", "passwordPolicy", "keys", "", "0"} {
		if Sensitive(k) {
			t.Errorf("%s: sensitive", k)
		}
	}
}

// TestSensitivePath: the key of a path is its last element that is not a
// list index.
func TestSensitivePath(t *testing.T) {
	for _, tc := range []struct {
		path []string
		want bool
	}{
		{[]string{"db", "password"}, true},
		{[]string{"tokens", "1"}, true},
		{[]string{"tokens", "1", "name"}, false},
		{[]string{"db", "host"}, false},
		{[]string{"0", "12"}, false},
		{nil, false},
	} {
		if got := SensitivePath(tc.path); got != tc.want {
			t.Errorf("%v: got %v", tc.path, got)
		}
	}
}

// TestRedact: the values of sensitive keys at any depth redacted, in a
// copy, whatever they are; the rest as given; Short and ShortAt print
// the result.
func TestRedact(t *testing.T) {
	in := M{"host": "db", "password": "hunter2", "auth": M{"tokens": []any{"a", "b"}, "user": "u"}, "list": []any{M{"apiKey": 1.0, "name": "n"}}}
	want := M{"host": "db", "password": Redacted, "auth": M{"tokens": Redacted, "user": "u"}, "list": []any{M{"apiKey": Redacted, "name": "n"}}}
	if got := Redact(in); !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v", got)
	}
	if in["password"] != "hunter2" {
		t.Error("the value given changed")
	}
	if got := Short(M{"password": "hunter2"}); got != `map[string]interface {}{"password":"<redacted>"}` {
		t.Errorf("Short: %s", got)
	}
	if got := Short("hunter2"); got != `"hunter2"` {
		t.Errorf("Short of a string: %s", got)
	}
	if got := ShortAt("clientSecret", "hunter2"); got != Redacted {
		t.Errorf("ShortAt a sensitive key: %s", got)
	}
	if got := ShortAt("realm", M{"token": "x"}); got != `map[string]interface {}{"token":"<redacted>"}` {
		t.Errorf("ShortAt another key: %s", got)
	}
}
