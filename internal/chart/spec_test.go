package chart

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// specsCheck: the charts section of service.yaml, decoded and checked.
func specsCheck(yml string) error {
	var s Specs
	if err := yaml.Unmarshal([]byte(yml), &s); err != nil {
		return err
	}
	return s.Check()
}

// TestRefCheck: a source names a repository URL, a chart and a pinned
// version.
func TestRefCheck(t *testing.T) {
	for source, want := range map[string]string{
		"{repository: https://x, chart: app, version: 1.0.0}":            "",
		"{repository: oci://x/charts, chart: app, version: v2.1.0-rc.1}": "",
		"": "charts.app.source is required: the upstream chart (repository, chart, version)",
		"{repository: x.org, chart: app, version: 1.0.0}":          `charts.app.source.repository "x.org": the URL of a chart repository`,
		"{repository: https://x/../y, chart: app, version: 1.0.0}": `charts.app.source.repository "https://x/../y": the URL of a chart repository`,
		"{repository: 'oci://', chart: app, version: 1.0.0}":       `charts.app.source.repository "oci://": the URL of a chart repository`,
		"{repository: https://x, chart: ../app, version: 1}":       `charts.app.source.chart "../app": the name of a chart`,
		"{repository: https://x, chart: a/b, version: 1.0.0}":      `charts.app.source.chart "a/b": the name of a chart`,
		"{repository: https://x, version: 1.0.0}":                  `charts.app.source.chart "": the name of a chart`,
		"{repository: https://x, chart: app, version: ^1.0}":       `charts.app.source.version "^1.0": a pinned chart version (1.2.3), not a range`,
		"{repository: https://x, chart: app}":                      `charts.app.source.version "": a pinned chart version`,
	} {
		yml := "app: {source: " + source + "}\n"
		if source == "" {
			yml = "app: {}\n"
		}
		err := specsCheck(yml)
		if want == "" {
			if err != nil {
				t.Errorf("%s: %v", source, err)
			}
		} else if err == nil || !strings.Contains(err.Error(), "service.yaml: "+want) {
			t.Errorf("%s: got %v, want an error containing %q", source, err, want)
		}
	}
}

// TestSpecKeys: a chart key is a DNS label: it names files and the
// umbrella's dependency alias.
func TestSpecKeys(t *testing.T) {
	src := Ref{Repository: "https://x", Chart: "app", Version: "1.0.0"}
	for key, ok := range map[string]bool{
		"app": true, "a-1": true, "0": true, strings.Repeat("a", 63): true,
		"": false, "../escaped": false, "App": false, "my_app": false, "my.app": false, "-app": false, "app-": false, strings.Repeat("a", 64): false,
	} {
		err := Specs{key: {Source: src}}.Check()
		if want := fmt.Sprintf("service.yaml: charts: key %q: a DNS label", key); ok && err != nil || !ok && (err == nil || !strings.HasPrefix(err.Error(), want)) {
			t.Errorf("%q: got %v", key, err)
		}
	}
}

// TestUpstreamYAML: upstream: in service.yaml, true or false, or {from:
// <chart>}.
func TestUpstreamYAML(t *testing.T) {
	for in, want := range map[string]Upstream{
		"":                    {},
		"upstream: false":     {},
		"upstream: true":      {Enabled: true},
		"upstream: {from: a}": {From: "a"},
	} {
		var spec Spec
		if err := yaml.Unmarshal([]byte(in), &spec); err != nil {
			t.Errorf("%q: %v", in, err)
		} else if !reflect.DeepEqual(spec.Upstream, want) {
			t.Errorf("%q: got %#v, want %#v", in, spec.Upstream, want)
		}
	}
	for _, in := range []string{`upstream: ""`, "upstream: 1", "upstream: {a: b}", "upstream: a.yaml", `upstream: "off"`, "upstream: [a.yaml]", "upstream: []",
		"upstream: {}", `upstream: {from: ""}`, "upstream: {from: a, files: [b]}", "upstream: {from: [a]}", "upstream: {from: true}"} {
		var spec Spec
		if err := yaml.Unmarshal([]byte(in), &spec); err == nil {
			t.Errorf("%q: no error", in)
		}
	}
	// A YAML 1.1 boolean, a string in service.yaml (YAML 1.2): refused with
	// what to write.
	for _, in := range []string{"upstream: off", "upstream: no", "upstream: Yes", "upstream: ON", "upstream: y"} {
		var spec Spec
		err := yaml.Unmarshal([]byte(in), &spec)
		if want := "line 1: upstream " + strings.TrimPrefix(in, "upstream: ") + ": write true or false"; err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: got %v, want an error containing %q", in, err, want)
		}
	}
}

// TestSpecPaths: a malformed protect or append path is refused before any
// compile, not when an instance first sets a value under it.
func TestSpecPaths(t *testing.T) {
	const app = "app:\n  source: {repository: https://x, chart: app, version: 1.0.0}\n"
	for yml, want := range map[string]string{
		app + "  protect: [a.b, 'a[\"b']\n":    `service.yaml: charts.app.protect[1] "a[\"b": a bracketed key ends with "]`,
		app + "  append: ['env.*']\n":          `service.yaml: charts.app.append[0] "env.*": a * outside brackets`,
		app + "  protect: ['a[\"b.c*\"].d']\n": "",
	} {
		err := specsCheck(yml)
		if want == "" {
			if err != nil {
				t.Errorf("%s: %v", yml, err)
			}
		} else if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v, want an error containing %q", yml, err, want)
		}
	}
}
