package merge

import (
	"errors"
	"maps"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/okdp/okdp-compiler/value"
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
			appended, err := applyUpstream(got, tc.user, source{chart: "c"}, protect, appendPaths, nil)
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
			_, err := applyUpstream(M{}, tc.user, source{chart: "c"}, []string{"ingress.hosts", "fullnameOverride"}, []string{"env"}, nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

// TestApplyUpstreamCheck: the caller's check (UpstreamCheck) sees the
// instance's upstream values and the computed ones, after the protected
// paths; at names a path of the values; its error fails, the values not
// changed.
func TestApplyUpstreamCheck(t *testing.T) {
	var seen []M
	check := func(user, values M, at func(string) string) error {
		seen = append(seen, maps.Clone(user), maps.Clone(values))
		if _, ok := user["hostNetwork"]; ok {
			return errors.New(at("hostNetwork") + ": denied")
		}
		return nil
	}
	values := M{"replicas": float64(1)}
	if _, err := applyUpstream(values, M{"replicas": float64(2)}, source{chart: "c"}, nil, nil, check); err != nil || values["replicas"] != float64(2) || len(seen) != 2 || seen[0]["replicas"] != float64(2) || seen[1]["replicas"] != float64(1) {
		t.Errorf("accepted: %v %#v %#v", err, values, seen)
	}
	_, err := applyUpstream(values, M{"hostNetwork": true}, source{chart: "c"}, nil, nil, check)
	if want := "upstream.c.hostNetwork: denied"; err == nil || err.Error() != want || values["hostNetwork"] != nil {
		t.Errorf("refused: got %v, want %q", err, want)
	}
	_, err = applyUpstream(values, M{"hostNetwork": true}, source{chart: "c"}, []string{"hostNetwork"}, nil, check)
	if want := "upstream.c.hostNetwork: hostNetwork is set by the platform and cannot be changed"; err == nil || err.Error() != want {
		t.Errorf("protect first: got %v, want %q", err, want)
	}
}

// TestNoTemplate: the first "{{" in path order, in a key or a string, named
// by its path; a value of a type values do not hold fails, where marshalling
// it to look for "{{" would fail and let it pass.
func TestFinalValuesNoUpstream(t *testing.T) {
	src := Ref{Repository: "https://x", Chart: "app", Version: "1.0.0"}
	specs := Specs{"": {Source: src, Upstream: true}, "app": {Source: src}}
	values := M{"upstream": M{"": M{"a": 1.0}}}
	f, err := FinalValues(fstest.MapFS{}, specs, "values.cel.yaml", M{"app": M{}}, values, nil)
	if err != nil || f.Values["app"]["a"] != nil {
		t.Errorf("app: %v %#v", err, f.Values["app"])
	}
}

// TestFinalValues: the final values hold no chart default, an empty
// global left out; the lists an instance appended to are reported by key,
// each path once.
func TestFinalValues(t *testing.T) {
	src := Ref{Repository: "https://x", Chart: "app", Version: "1.0.0"}
	specs := Specs{"app": {Source: src, Upstream: true, Append: []string{"env", "args", "extra"}}}
	for name, tc := range map[string]struct {
		computed, user M
		global         M
		want           M          // app's final values
		appended       [][]string // app's appended paths
	}{
		"no default":     {M{"image": "x"}, nil, nil, M{"image": "x"}, nil},
		"a global":       {M{}, nil, M{"g": float64(1)}, M{"global": M{"g": float64(1)}}, nil},
		"a list":         {M{"env": []any{"B"}}, M{"env": []any{"C"}}, nil, M{"env": []any{"B", "C"}}, [][]string{{"env"}}},
		"two lists":      {M{}, M{"env": []any{"C"}, "args": []any{"b"}}, nil, M{"env": []any{"C"}, "args": []any{"b"}}, [][]string{{"env"}, {"args"}}},
		"not appended":   {M{"env": []any{"C"}}, nil, nil, M{"env": []any{"C"}}, nil},
		"a replaced key": {M{"image": "x"}, M{"image": "y"}, nil, M{"image": "y"}, nil},
	} {
		t.Run(name, func(t *testing.T) {
			values := M{"upstream": M{"app": tc.user}, "global": tc.global}
			f, err := FinalValues(fstest.MapFS{}, specs, "values.yaml", M{"app": tc.computed}, values, nil)
			if err != nil || f.Charts["app"] != src || !reflect.DeepEqual(f.Values["app"], tc.want) {
				t.Fatalf("%v %#v", err, f.Values["app"])
			}
			if got := f.Appended["app"]; !reflect.DeepEqual(got, tc.appended) {
				t.Errorf("appended: got %q, want %q", got, tc.appended)
			}
		})
	}
	if _, err := FinalValues(fstest.MapFS{}, specs, "values.yaml", M{"other": M{}}, M{}, nil); err == nil || err.Error() != "values.yaml: other: not a dependency of Chart.yaml" {
		t.Errorf("a key of no dependency: %v", err)
	}
}

// TestUpstreamBrackets: protect and append paths with bracketed literal
// keys (dots in them) and globs: the keys a plain dotted path cannot name.
func TestUpstreamBrackets(t *testing.T) {
	protect := []string{
		`annotations["nginx.ingress.kubernetes.io/auth-*"]`,
		`config["spark.ui.filters"]`,
		`config["spark.history.ui.acls.*"]`,
		`ingresses["*"].tls`,
	}
	appendPaths := []string{`config["spark.extra.list"]`, `envs["*"]`, `envs["main"]`}
	computed := func() M {
		return M{
			"annotations": M{"nginx.ingress.kubernetes.io/auth-type": "basic", "nginx.ingress.kubernetes.io/auth-url": "u"},
			"config":      M{"spark.ui.filters": "f", "spark.extra.list": []any{"a"}},
			"envs":        M{"main": []any{"A"}},
			"ingresses":   M{"http": M{"tls": []any{"t"}, "host": "h"}},
		}
	}
	for name, tc := range map[string]struct {
		user M
		want string // the error, or "" for none
		path string // the key changed, when no error
		v    any    // its value
	}{
		"other annotation":           {M{"annotations": M{"nginx.ingress.kubernetes.io/proxy-body-size": "1m"}}, "", "annotations", M{"nginx.ingress.kubernetes.io/auth-type": "basic", "nginx.ingress.kubernetes.io/auth-url": "u", "nginx.ingress.kubernetes.io/proxy-body-size": "1m"}},
		"a glob's annotation":        {M{"annotations": M{"nginx.ingress.kubernetes.io/auth-type": nil}}, `upstream.c.annotations["nginx.ingress.kubernetes.io/auth-type"]: annotations["nginx.ingress.kubernetes.io/auth-*"] is set by the platform and cannot be changed`, "", nil},
		"a new key of the glob":      {M{"annotations": M{"nginx.ingress.kubernetes.io/auth-signin": "x"}}, `upstream.c.annotations["nginx.ingress.kubernetes.io/auth-signin"]: annotations["nginx.ingress.kubernetes.io/auth-*"]`, "", nil},
		"the glob's parent":          {M{"annotations": nil}, `upstream.c.annotations: annotations["nginx.ingress.kubernetes.io/auth-*"] is set by the platform`, "", nil},
		"a literal dotted key":       {M{"config": M{"spark.ui.filters": "other"}}, `upstream.c.config["spark.ui.filters"]: config["spark.ui.filters"] is set by the platform`, "", nil},
		"a key the literal is not":   {M{"config": M{"spark.ui.filters.x": "1"}}, "", "config", M{"spark.ui.filters": "f", "spark.ui.filters.x": "1", "spark.extra.list": []any{"a"}}},
		"a glob's prefix itself":     {M{"config": M{"spark.history.ui.acls.": "1"}}, `upstream.c.config["spark.history.ui.acls."]`, "", nil},
		"a glob on the way":          {M{"ingresses": M{"grpc": M{"tls": []any{}}}}, `upstream.c.ingresses.grpc.tls: ingresses["*"].tls is set by the platform`, "", nil},
		"beside a glob on the way":   {M{"ingresses": M{"http": M{"host": "other"}}}, "", "ingresses", M{"http": M{"tls": []any{"t"}, "host": "other"}}},
		"a non-map on the way":       {M{"ingresses": M{"http": "off"}}, `upstream.c.ingresses.http: ingresses["*"].tls`, "", nil},
		"appended dotted key":        {M{"config": M{"spark.extra.list": []any{"b"}}}, "", "config", M{"spark.ui.filters": "f", "spark.extra.list": []any{"a", "b"}}},
		"appended once per key":      {M{"envs": M{"main": []any{"B"}, "side": []any{"S"}}}, "", "envs", M{"main": []any{"A", "B"}, "side": []any{"S"}}},
		"appended a non-list":        {M{"envs": M{"side": "S"}}, "upstream.c.envs.side must be a list", "", nil},
		"appended a dotted non-list": {M{"config": M{"spark.extra.list": "b"}}, `upstream.c.config["spark.extra.list"] must be a list`, "", nil},
	} {
		t.Run(name, func(t *testing.T) {
			got := computed()
			_, err := applyUpstream(got, tc.user, source{chart: "c"}, protect, appendPaths, nil)
			if tc.want != "" {
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Errorf("got %v, want an error containing %q", err, tc.want)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := computed()
			want[tc.path] = tc.v
			if !reflect.DeepEqual(got, want) {
				t.Errorf("got  %#v\nwant %#v", got, want)
			}
		})
	}
	// The lists appended to, once each, in the order of the append paths:
	// envs.main matched by the glob first.
	appended, err := applyUpstream(computed(), M{"envs": M{"side": []any{"S"}, "main": []any{"B"}}, "config": M{"spark.extra.list": []any{"b"}}}, source{chart: "c"}, protect, appendPaths, nil)
	if want := [][]string{{"config", "spark.extra.list"}, {"envs", "main"}, {"envs", "side"}}; err != nil || !reflect.DeepEqual(appended, want) {
		t.Errorf("appended: got %q (%v), want %q", appended, err, want)
	}
	// A key in brackets first: no dot after the chart.
	_, err = applyUpstream(M{}, M{"a.b": float64(1)}, source{chart: "c"}, []string{`["a.b"]`}, nil, nil)
	if want := `upstream.c["a.b"]: ["a.b"] is set by the platform`; err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("got %v, want %q", err, want)
	}
}

func TestMerged(t *testing.T) {
	first, second := M{"c": nil, "b": M{"c": float64(1)}}, M{"b": M{"c": nil}, "d": float64(1)}
	got := merged(first, second)
	want := M{"b": M{"c": nil}, "c": nil, "d": float64(1)}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
	if value.AsMap(first["b"])["c"] != float64(1) {
		t.Errorf("a layer changed: %#v", first)
	}
}
