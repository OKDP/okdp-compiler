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
		user     M
		want     M          // the changed keys of computed
		appended [][]string // the paths of the lists appended to
	}{
		"nothing":                 {M{}, M{}, nil},
		"a free key":              {M{"replicas": float64(2)}, M{"replicas": float64(2)}, nil},
		"beside a protected path": {M{"ingress": M{"className": "other"}}, M{"ingress": M{"hosts": []any{"h"}, "className": "other"}}, nil},
		"appended list":           {M{"env": []any{"B"}}, M{"env": []any{"A", "B"}}, [][]string{{"env"}}},
		"nested appended list":    {M{"pod": M{"volumes": []any{"v2"}}}, M{"pod": M{"volumes": []any{"v1", "v2"}}}, [][]string{{"pod", "volumes"}}},
		"other list replaced":     {M{"args": []any{"b"}}, M{"args": []any{"b"}}, nil},
	} {
		t.Run(name, func(t *testing.T) {
			got := computed()
			appended, err := applyUpstream(got, tc.user, source{chart: "c"}, protect, appendPaths)
			if err != nil {
				t.Fatal(err)
			}
			want := computed()
			maps.Copy(want, tc.want)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("got  %#v\nwant %#v", got, want)
			}
			if !reflect.DeepEqual(appended, tc.appended) {
				t.Errorf("appended: got %q, want %q", appended, tc.appended)
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
			_, err := applyUpstream(M{}, tc.user, source{chart: "c"}, []string{"ingress.hosts", "fullnameOverride"}, []string{"env"})
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
	out, _, _, err := FinalValues(cache, fstest.MapFS{}, specs, "values.cel.yaml", M{"app": M{}}, values)
	if err != nil || out["app"]["a"] != nil {
		t.Errorf("app: %v %#v", err, out["app"])
	}
}

// TestFinalValuesCheck: the final values hold no chart default, an empty
// global left out, and are the same with the chart cache; with it, the
// schema of each chart is checked over its defaults, and an instance list
// appended to that leaves out items of the chart's default list is warned
// of, once per path.
func TestFinalValuesCheck(t *testing.T) {
	src := Ref{Repository: "https://x", Chart: "app", Version: "1.0.0"}
	specs := Specs{"app": {Source: src, Upstream: true, Append: []string{"env", "args", "extra"}}}
	cache := fstest.MapFS{
		"x/app-1.0.0/app/values.yaml":        {Data: []byte("replicas: 1\nenv: [A, B]\nargs: [a]\nextra: []\n")},
		"x/app-1.0.0/app/values.schema.json": {Data: []byte(`{"required": ["replicas"], "properties": {"replicas": {"maximum": 3}}}`)},
	}
	for name, tc := range map[string]struct {
		computed, user M
		global         M
		want           M        // app's final values
		warnings       []string // with the cache
	}{
		"no default":       {M{"image": "x"}, nil, nil, M{"image": "x"}, nil},
		"a global":         {M{}, nil, M{"g": float64(1)}, M{"global": M{"g": float64(1)}}, nil},
		"a list kept":      {M{"env": []any{"A", "B"}}, M{"env": []any{"C"}}, nil, M{"env": []any{"A", "B", "C"}}, nil},
		"an item lost":     {M{"env": []any{"B"}}, M{"env": []any{"C"}}, nil, M{"env": []any{"B", "C"}}, []string{"upstream.app.env: appended to the package's list, not to the default list of app-1.0.0, which Helm replaces: the final list lacks 1 of its 2 items; restate them in the package's values of app (its defaults or values file) to keep them"}},
		"all items lost":   {M{}, M{"env": []any{"C"}, "args": []any{"b"}}, nil, M{"env": []any{"C"}, "args": []any{"b"}}, []string{"upstream.app.env: appended to the package's list, not to the default list of app-1.0.0, which Helm replaces: the final list lacks all its items; restate them in the package's values of app (its defaults or values file) to keep them", "upstream.app.args: appended to the package's list, not to the default list of app-1.0.0, which Helm replaces: the final list lacks all its items; restate them in the package's values of app (its defaults or values file) to keep them"}},
		"not appended":     {M{"env": []any{"C"}}, nil, nil, M{"env": []any{"C"}}, nil},
		"an empty default": {M{}, M{"extra": []any{"x"}}, nil, M{"extra": []any{"x"}}, nil},
	} {
		t.Run(name, func(t *testing.T) {
			values := M{"upstream": M{"app": tc.user}, "global": tc.global}
			out, refs, warnings, err := FinalValues(nil, fstest.MapFS{}, specs, "values.yaml", M{"app": tc.computed}, values)
			if err != nil || warnings != nil || refs["app"] != src || !reflect.DeepEqual(out["app"], tc.want) {
				t.Fatalf("without the cache: %v %q %#v", err, warnings, out["app"])
			}
			checked, _, warnings, err := FinalValues(cache, fstest.MapFS{}, specs, "values.yaml", M{"app": tc.computed}, values)
			if err != nil || !reflect.DeepEqual(checked, out) {
				t.Errorf("with the cache: %v %#v", err, checked["app"])
			}
			if !reflect.DeepEqual(warnings, tc.warnings) {
				t.Errorf("warnings:\n got %q\nwant %q", warnings, tc.warnings)
			}
		})
	}
	// The schema, over the chart's defaults (replicas required, set by
	// them); a chart missing from the cache.
	_, _, _, err := FinalValues(cache, fstest.MapFS{}, specs, "values.yaml", M{"app": M{"replicas": float64(5)}}, M{})
	if want := "compiled values don't meet the specifications of the schema(s) in the following chart(s):\napp (app-1.0.0):\n- at '/replicas': maximum: got 5, want 3"; err == nil || err.Error() != want {
		t.Errorf("schema: got %v, want %q", err, want)
	}
	_, _, _, err = FinalValues(fstest.MapFS{}, fstest.MapFS{}, specs, "values.yaml", M{"app": M{}}, M{})
	if want := "app: chart app-1.0.0 is not in the chart cache: "; err == nil || !strings.HasPrefix(err.Error(), want) {
		t.Errorf("no chart: got %v, want an error starting with %q", err, want)
	}
}

// TestFinalValuesCheckGlobal: values without a global are checked with an
// empty one, as Helm gives every subchart: a schema that refuses it fails.
func TestFinalValuesCheckGlobal(t *testing.T) {
	src := Ref{Repository: "https://x", Chart: "app", Version: "1.0.0"}
	cache := fstest.MapFS{"x/app-1.0.0/app/values.schema.json": {Data: []byte(`{"additionalProperties": false, "properties": {"a": {}}}`)}}
	out, _, _, err := FinalValues(cache, fstest.MapFS{}, Specs{"app": {Source: src}}, "values.yaml", M{"app": M{"a": 1.0}}, M{})
	if want := "- at '': additional properties 'global' not allowed"; err == nil || !strings.HasSuffix(err.Error(), want) || out != nil {
		t.Errorf("got %v, want an error ending with %q", err, want)
	}
}
