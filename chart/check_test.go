package chart

import (
	"testing"
)

// TestNoTemplate: the first "{{" in path order, in a key or a string, named
// by its path; a value of a type values do not hold fails, where marshalling
// it to look for "{{" would fail and let it pass.
func TestNoTemplate(t *testing.T) {
	for name, tc := range map[string]struct {
		v    M
		want string // the error, none when empty
	}{
		"none":               {M{"a": "{ {", "b": []any{"}}", true, nil, float64(1)}, "c": M{}}, ""},
		"a nested value":     {M{"a": M{"b": "x {{ .Values.x }}"}}, `upstream.c.a.b: the value contains "{{"`},
		"a key":              {M{"a": M{"{{ x }}": "y"}}, `upstream.c.a.{{ x }}: the key contains "{{"`},
		"an item":            {M{"env": []any{"A", M{"value": "{{"}}}, `upstream.c.env[1].value: the value contains "{{"`},
		"a key in brackets":  {M{"annotations": M{"example.org/x": "{{"}}, `upstream.c.annotations["example.org/x"]: the value contains "{{"`},
		"the first in order": {M{"b": "{{", "a": M{"z": "{{"}}, `upstream.c.a.z: the value contains "{{"`},
		"not a values type":  {M{"a": M{"b": make(chan int)}}, `upstream.c.a.b: cannot be checked for "{{": chan int is not a values type`},
		"a Go list":          {M{"a": []string{"{{"}}, `upstream.c.a: cannot be checked for "{{": []string is not a values type`},
	} {
		t.Run(name, func(t *testing.T) {
			err := NoTemplate(tc.v, func(p string) string { return "upstream.c." + p })
			if tc.want == "" && err != nil || tc.want != "" && (err == nil || err.Error() != tc.want) {
				t.Errorf("got %v, want %q", err, tc.want)
			}
		})
	}
}
