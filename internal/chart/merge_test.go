package chart

import (
	"maps"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

// TestApplyUpstream: instance upstream values over the computed ones, with
// the protected and appended paths of chart c.
func TestApplyUpstream(t *testing.T) {
	protect := []string{"ingress.hosts", "fullnameOverride"}
	appendPaths := []string{"env", "pod.volumes"}
	computed := func() M {
		return M{
			"fullnameOverride": "x",
			"ingress":          M{"hosts": []any{"h"}, "className": "nginx"},
			"env":              []any{"A"},
			"pod":              M{"volumes": []any{"v1"}},
			"args":             []any{"a"},
		}
	}
	for name, tc := range map[string]struct {
		user M
		want M // the changed keys of computed
	}{
		"nothing":                 {M{}, M{}},
		"a free key":              {M{"replicas": float64(2)}, M{"replicas": float64(2)}},
		"beside a protected path": {M{"ingress": M{"className": "other"}}, M{"ingress": M{"hosts": []any{"h"}, "className": "other"}}},
		"appended list":           {M{"env": []any{"B"}}, M{"env": []any{"A", "B"}}},
		"nested appended list":    {M{"pod": M{"volumes": []any{"v2"}}}, M{"pod": M{"volumes": []any{"v1", "v2"}}}},
		"other list replaced":     {M{"args": []any{"b"}}, M{"args": []any{"b"}}},
	} {
		t.Run(name, func(t *testing.T) {
			got := computed()
			if err := applyUpstream(got, tc.user, source{chart: "c"}, protect, appendPaths); err != nil {
				t.Fatal(err)
			}
			want := computed()
			maps.Copy(want, tc.want)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("got  %#v\nwant %#v", got, want)
			}
		})
	}
}

// TestApplyUpstreamErrors: protected paths, templates and appended non-lists.
func TestApplyUpstreamErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		user M
		want string
	}{
		"protected leaf":          {M{"ingress": M{"hosts": []any{}}}, "upstream.c.ingress.hosts: ingress.hosts is set by the platform"},
		"protected as a non-map":  {M{"ingress": "off"}, "upstream.c.ingress: ingress.hosts is set by the platform"},
		"protected top-level key": {M{"fullnameOverride": "y"}, "upstream.c.fullnameOverride"},
		"global is protected":     {M{"global": M{"a": float64(1)}}, "upstream.c.global: global is set by the platform"},
		"a template":              {M{"name": "{{ .Release.Name }}"}, `upstream.c.name: the value contains "{{"`},
		"append to a non-list":    {M{"env": "A"}, "upstream.c.env must be a list"},
	} {
		t.Run(name, func(t *testing.T) {
			err := applyUpstream(M{}, tc.user, source{chart: "c"}, []string{"ingress.hosts", "fullnameOverride"}, []string{"env"})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

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
			err := NoTemplate(tc.v, source{chart: "c"}.at)
			if tc.want == "" && err != nil || tc.want != "" && (err == nil || err.Error() != tc.want) {
				t.Errorf("got %v, want %q", err, tc.want)
			}
		})
	}
}

// TestFinalValuesNoUpstream: a chart that accepts no upstream values
// receives none, also beside a chart keyed "" (Specs built in memory,
// unchecked): no upstream values is not the chart "".
func TestFinalValuesNoUpstream(t *testing.T) {
	src := Ref{Repository: "https://x", Chart: "app", Version: "1.0.0"}
	specs := Specs{"": {Source: src, Upstream: true}, "app": {Source: src}}
	values := M{"upstream": M{"": M{"a": 1.0}}}
	cache := fstest.MapFS{"x/app-1.0.0/app/values.yaml": {Data: []byte("b: 2\n")}}
	out, _, err := FinalValues(cache, fstest.MapFS{}, specs, M{"app": M{}}, values)
	if err != nil || out["app"]["a"] != nil {
		t.Errorf("app: %v %#v", err, out["app"])
	}
}
