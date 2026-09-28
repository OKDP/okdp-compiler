package compiler

import (
	"reflect"
	"strings"
	"testing"
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
			for k, v := range tc.want {
				want[k] = v
			}
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
		"global is protected":     {M{"global": M{"a": 1}}, "upstream.c.global: global is set by the platform"},
		"a template":              {M{"name": "{{ .Release.Name }}"}, `contains "{{"`},
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
