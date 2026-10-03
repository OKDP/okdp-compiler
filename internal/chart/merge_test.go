package chart

import (
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

// TestReadUpstream: the sources of each chart that accepts upstream values,
// its files then its inline values; none for the others.
func TestReadUpstream(t *testing.T) {
	specs := Specs{"app": {Upstream: Upstream{Enabled: true}}, "other": {}}
	instance := fstest.MapFS{
		"upstream.app.yaml":   {Data: []byte("env: [A]\n")},
		"upstream.other.yaml": {Data: []byte("x: 1\n")},
	}
	ups := ReadUpstream(instance, specs, M{"upstream": M{"app": M{"env": []any{"C"}}, "other": M{"y": 2}}})
	if len(ups["app"].layers) != 2 || ups["app"].layers[0].src.file != "upstream.app.yaml" || ups["app"].layers[1].src.chart != "app" {
		t.Errorf("upstream sources of app: %#v", ups["app"])
	}
	if _, ok := ups["other"]; ok {
		t.Error("a chart without upstream values has no upstream sources")
	}
}

// TestFinalValuesShape: the values of a chart of the output must be a map,
// whoever builds the output.
func TestFinalValuesShape(t *testing.T) {
	for _, v := range []any{nil, "x", []any{}} {
		_, _, err := FinalValues(fstest.MapFS{}, fstest.MapFS{}, Specs{"app": {}}, M{"app": v}, M{}, nil)
		if err == nil || !strings.HasPrefix(err.Error(), `mapping output "app": must be a map of the chart's values, got `) {
			t.Errorf("%#v: got %v", v, err)
		}
	}
}
