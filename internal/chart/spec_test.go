package chart

import (
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// TestUpstreamYAML: okdp.yaml's upstream, a boolean (a YAML 1.1 one read
// as the one it spells), or a map by dependency key of booleans, {} and
// {protect, append}; another form fails.
func TestUpstreamYAML(t *testing.T) {
	type pkg struct {
		Upstream Upstream `yaml:"upstream"`
	}
	for in, want := range map[string]Upstream{
		"":                {},
		"upstream: ~":     {},
		"upstream: false": {},
		"upstream: true":  {All: true},
		"upstream: off":   {},
		"upstream: Yes":   {All: true},
		"upstream: {}":    {Keys: map[string]UpstreamKey{}},
		"upstream: {api: true, web: false, db: yes, x: {}, y: {protect: [a, 'b[\"c.d\"]'], append: [env]}, z: {append: []}}": {Keys: map[string]UpstreamKey{
			"api": {Accept: true}, "web": {}, "db": {Accept: true}, "x": {Accept: true},
			"y": {Accept: true, Protect: []string{"a", `b["c.d"]`}, Append: []string{"env"}}, "z": {Accept: true, Append: []string{}},
		}},
	} {
		var p pkg
		if err := yaml.Unmarshal([]byte(in), &p); err != nil || !reflect.DeepEqual(p.Upstream, want) {
			t.Errorf("%q: got %#v (%v), want %#v", in, p.Upstream, err, want)
		}
	}
	for in, want := range map[string]string{
		`upstream: ""`:                               "upstream: true, false or a map by dependency",
		"upstream: 1":                                "upstream: true, false or a map by dependency",
		"upstream: [api]":                            "upstream: true, false or a map by dependency",
		"upstream: {api: 1}":                         "upstream.api: true, false or {protect, append}",
		"upstream: {api: ~}":                         "upstream.api: true, false or {protect, append}",
		"upstream: {api: [a]}":                       "upstream.api: true, false or {protect, append}",
		"upstream: {api: {protcet: [a]}}":            "upstream.api: true, false or {protect, append}",
		"upstream: {api: {upstream: true}}":          "upstream.api: true, false or {protect, append}",
		"upstream: {api: {protect: a}}":              "yaml: unmarshal errors:\n  line 1: cannot unmarshal !!str `a` into []string",
		"upstream: {api: {protect: [[a]]}}":          "yaml: unmarshal errors:\n  line 1: cannot unmarshal !!seq into string",
		"upstream: {api: true, api: false}":          "yaml: unmarshal errors:\n  line 1: mapping key \"api\" already defined at line 1",
		"upstream: {api: {append: [], append: [a]}}": "yaml: unmarshal errors:\n  line 1: mapping key \"append\" already defined at line 1",
	} {
		var p pkg
		if err := yaml.Unmarshal([]byte(in), &p); err == nil || err.Error() != want {
			t.Errorf("%q: got %v, want %q", in, err, want)
		}
	}
}

// TestNewSpecs: the dependencies of Chart.yaml by key, each rendered by its
// chart, with okdp.yaml's defaults and upstream; a key of either that is
// not a dependency fails.
func TestNewSpecs(t *testing.T) {
	workspace := Ref{Repository: "https://x", Chart: "workspace", Version: "1.0.0"}
	gateway := Ref{Repository: "oci://x/charts", Chart: "gateway", Version: "2.0.0"}
	meta := Metadata{APIVersion: "v2", Name: "demo", Version: "1.0.0", Dependencies: []Dependency{
		{Name: "workspace", Alias: "api", Repository: workspace.Repository, Version: workspace.Version},
		{Name: "workspace", Alias: "scheduler", Repository: workspace.Repository, Version: workspace.Version},
		{Name: "gateway", Repository: gateway.Repository, Version: gateway.Version},
	}}
	defaults := map[string]string{"api": "api.defaults.yaml"}
	s, err := NewSpecs(meta, defaults, Upstream{Keys: map[string]UpstreamKey{"api": {Accept: true, Protect: []string{"mode"}, Append: []string{"env"}}, "gateway": {}}})
	want := Specs{
		"api":       {Source: workspace, Defaults: "api.defaults.yaml", Upstream: true, Protect: []string{"mode"}, Append: []string{"env"}},
		"scheduler": {Source: workspace},
		"gateway":   {Source: gateway},
	}
	if err != nil || !reflect.DeepEqual(s, want) {
		t.Errorf("got %#v (%v)", s, err)
	}
	if s, err := NewSpecs(meta, nil, Upstream{All: true}); err != nil || !s["api"].Upstream || !s["scheduler"].Upstream || !s["gateway"].Upstream || s["api"].Protect != nil {
		t.Errorf("upstream: true: got %#v (%v)", s, err)
	}
	if s, err := NewSpecs(Metadata{}, nil, Upstream{All: true}); err != nil || len(s) != 0 {
		t.Errorf("no dependency: got %#v (%v)", s, err)
	}
	for name, tc := range map[string]struct {
		defaults map[string]string
		upstream Upstream
		want     string
	}{
		"defaults":         {map[string]string{"api": "a.yaml", "web": "web.defaults.yaml"}, Upstream{}, "okdp.yaml: defaults.web: not a dependency of Chart.yaml"},
		"upstream":         {nil, Upstream{Keys: map[string]UpstreamKey{"api": {Accept: true}, "workspace": {}}}, "okdp.yaml: upstream.workspace: not a dependency of Chart.yaml"},
		"defaults first":   {map[string]string{"x": "x.yaml"}, Upstream{Keys: map[string]UpstreamKey{"a": {}}}, "okdp.yaml: defaults.x: not a dependency of Chart.yaml"},
		"a protect path":   {nil, Upstream{Keys: map[string]UpstreamKey{"api": {Accept: true, Protect: []string{"a.b", `a["b`}}}}, `okdp.yaml: upstream.api.protect[1] "a[\"b": not a path: keys joined by dots`},
		"an append path":   {nil, Upstream{Keys: map[string]UpstreamKey{"api": {Accept: true, Append: []string{"env.*"}}}}, `okdp.yaml: upstream.api.append[0] "env.*": not a path`},
		"not accepted yet": {nil, Upstream{Keys: map[string]UpstreamKey{"api": {Protect: []string{"a..b"}}}}, `okdp.yaml: upstream.api.protect[0] "a..b": not a path`},
	} {
		if _, err := NewSpecs(meta, tc.defaults, tc.upstream); err == nil || !strings.HasPrefix(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want %q", name, err, tc.want)
		}
	}
	if _, err := NewSpecs(meta, nil, Upstream{Keys: map[string]UpstreamKey{"api": {Accept: true, Protect: []string{`a["b.c*"].d`}}}}); err != nil {
		t.Errorf("a bracketed glob: %v", err)
	}
}
