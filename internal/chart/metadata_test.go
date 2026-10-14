package chart

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// TestReadMetadata: a Chart.yaml of every field Helm defines, read as
// written; one YAML document, of known fields only.
func TestReadMetadata(t *testing.T) {
	m, err := ReadMetadata([]byte(`apiVersion: v2
name: demo
version: 1.2.3-1.0.0
kubeVersion: ">=1.28.0"
description: A demo.
type: application
keywords: [demo]
home: https://example.org
sources: [https://example.org/src]
dependencies:
  - {name: app, repository: https://x, version: 1.0.0}
  - {name: app, alias: canary, repository: oci://x/charts, version: v2.1.0-rc.1}
maintainers:
  - {name: OKDP, email: okdp@example.org, url: https://okdp.io}
icon: https://example.org/icon.svg
appVersion: "1.16"
deprecated: true
annotations: {example.org/a: b}
`))
	want := Metadata{
		APIVersion: "v2", Name: "demo", Version: "1.2.3-1.0.0", KubeVersion: ">=1.28.0", Description: "A demo.", Type: "application",
		Keywords: []string{"demo"}, Home: "https://example.org", Sources: []string{"https://example.org/src"},
		Dependencies: []Dependency{
			{Name: "app", Repository: "https://x", Version: "1.0.0"},
			{Name: "app", Alias: "canary", Repository: "oci://x/charts", Version: "v2.1.0-rc.1"},
		},
		Maintainers: []Maintainer{{Name: "OKDP", Email: "okdp@example.org", URL: "https://okdp.io"}},
		Icon:        "https://example.org/icon.svg", AppVersion: "1.16", Deprecated: true, Annotations: map[string]string{"example.org/a": "b"},
	}
	if err != nil || !reflect.DeepEqual(m, want) {
		t.Errorf("got %#v (%v)", m, err)
	}
	if k, r := m.Dependencies[1].Key(), m.Dependencies[1].Ref(); k != "canary" || r != (Ref{Repository: "oci://x/charts", Chart: "app", Version: "v2.1.0-rc.1"}) {
		t.Errorf("key %q, ref %#v", k, r)
	}
	const ok = "apiVersion: v2\nname: demo\nversion: 1.0.0\n"
	for src, want := range map[string]string{
		ok + "---\nname: other\n": "Chart.yaml: one YAML document",
		ok + "---\n":              "Chart.yaml: one YAML document",
		ok + "title: x\n":         "Chart.yaml: yaml: unmarshal errors:\n  line 4: field title not found in type chart.Metadata",
		ok + "dependencies: [{name: a, alais: b}]\n": "Chart.yaml: yaml: unmarshal errors:\n  line 4: field alais not found in type chart.Dependency",
		ok + "maintainers: [{name: a, mail: b}]\n":   "Chart.yaml: yaml: unmarshal errors:\n  line 4: field mail not found in type chart.Maintainer",
		ok + "dependencies: {app: {}}\n":             "Chart.yaml: yaml: unmarshal errors:\n  line 4: cannot unmarshal !!map into []chart.Dependency",
		ok + "annotations: {a: [b]}\n":               "Chart.yaml: yaml: unmarshal errors:\n  line 4: cannot unmarshal !!seq into string",
		"dependencies: [\n":                          "Chart.yaml: yaml: line 1: did not find expected node content",
		"":                                           `Chart.yaml: apiVersion "": v2, the only one the compiler reads`,
		"# nothing\n":                                `Chart.yaml: apiVersion "": v2, the only one the compiler reads`,
		ok + "dependencies: [{name: a, version: 1.x}]\n": `Chart.yaml: dependencies[0].repository "": the URL of a chart repository`,
	} {
		if _, err := ReadMetadata([]byte(src)); err == nil || !strings.HasPrefix(err.Error(), want) {
			t.Errorf("%q: got %v, want %q", src, err, want)
		}
	}
}

// TestMetadataCheck: what a Chart.yaml may not hold, its first error in the
// order of Metadata.Check.
func TestMetadataCheck(t *testing.T) {
	app := Dependency{Name: "app", Repository: "https://x", Version: "1.0.0"}
	with := func(f func(d *Dependency)) Dependency {
		d := app
		f(&d)
		return d
	}
	chart := func(deps ...Dependency) Metadata {
		return Metadata{APIVersion: "v2", Name: "demo", Version: "1.0.0", Dependencies: deps}
	}
	for name, tc := range map[string]struct {
		m    Metadata
		want string // "": no error
	}{
		"ok":             {chart(app, with(func(d *Dependency) { d.Alias = "canary" })), ""},
		"no dependency":  {chart(), ""},
		"application":    {Metadata{APIVersion: "v2", Name: "demo", Version: "v1.0.0+build", Type: "application"}, ""},
		"apiVersion v1":  {Metadata{APIVersion: "v1", Name: "demo", Version: "1.0.0"}, `Chart.yaml: apiVersion "v1": v2, the only one the compiler reads`},
		"no name":        {Metadata{APIVersion: "v2", Version: "1.0.0"}, "Chart.yaml: name is required"},
		"no version":     {Metadata{APIVersion: "v2", Name: "demo"}, "Chart.yaml: version is required"},
		"not SemVer":     {Metadata{APIVersion: "v2", Name: "demo", Version: "1.0"}, `Chart.yaml: version "1.0": SemVer (1.2.3), the version of the umbrella chart`},
		"library":        {Metadata{APIVersion: "v2", Name: "demo", Version: "1.0.0", Type: "library"}, `Chart.yaml: type "library": a package is an application chart`},
		"no chart name":  {chart(app, with(func(d *Dependency) { d.Name, d.Alias = "", "api" })), "Chart.yaml: dependencies[1].name is required"},
		"a path":         {chart(with(func(d *Dependency) { d.Name = "a/b" })), `Chart.yaml: dependencies[0].name "a/b": the name of a chart of the repository`},
		"..":             {chart(with(func(d *Dependency) { d.Name, d.Alias = "..", "app" })), `Chart.yaml: dependencies[0].name "..": the name of a chart of the repository`},
		"condition":      {chart(app, with(func(d *Dependency) { d.Alias, d.Condition = "api", "api.enabled" })), "Chart.yaml: dependencies[1] (api): condition: the values decide what is rendered ($if on api in values.cel.yaml)"},
		"tags":           {chart(with(func(d *Dependency) { d.Tags = []any{"a"} })), "Chart.yaml: dependencies[0] (app): tags: the values decide what is rendered ($if on app in values.cel.yaml)"},
		"enabled false":  {chart(with(func(d *Dependency) { d.Enabled = false })), "Chart.yaml: dependencies[0] (app): enabled: the values decide what is rendered"},
		"import-values":  {chart(with(func(d *Dependency) { d.ImportValues = []any{"data"} })), "Chart.yaml: dependencies[0] (app): import-values: the values decide what is rendered"},
		"key":            {chart(app, with(func(d *Dependency) { d.Alias = "Api" })), `Chart.yaml: dependencies[1] (Api): key "Api": a DNS label (lowercase letters, digits and -, a letter or digit at each end, 63 characters at most): the key names the umbrella's dependency alias`},
		"name key":       {chart(with(func(d *Dependency) { d.Name = "my_app" })), `Chart.yaml: dependencies[0] (my_app): key "my_app": a DNS label`},
		"long key":       {chart(with(func(d *Dependency) { d.Alias = strings.Repeat("a", 64) })), `Chart.yaml: dependencies[0] (aaaa`},
		"duplicate":      {chart(app, with(func(d *Dependency) { d.Name = "other" }), with(func(d *Dependency) { d.Name, d.Alias = "workspace", "app" })), "Chart.yaml: dependencies[2] (app): the key of dependencies[0] too: give one of them an alias"},
		"file://":        {chart(with(func(d *Dependency) { d.Repository = "file://../x" })), `Chart.yaml: dependencies[0].repository "file://../x": the URL of a chart repository (https://... or oci://...)`},
		"@repo":          {chart(with(func(d *Dependency) { d.Repository = "@stable" })), `Chart.yaml: dependencies[0].repository "@stable": the URL of a chart repository`},
		"no host":        {chart(with(func(d *Dependency) { d.Repository = "oci://" })), `Chart.yaml: dependencies[0].repository "oci://": the URL of a chart repository`},
		"out of a cache": {chart(with(func(d *Dependency) { d.Repository = "https://x/../y" })), `Chart.yaml: dependencies[0].repository "https://x/../y": the URL of a chart repository`},
		"range":          {chart(with(func(d *Dependency) { d.Version = "~1.2" })), `Chart.yaml: dependencies[0].version "~1.2": a pinned chart version (1.2.3), not a range`},
		"no version pin": {chart(with(func(d *Dependency) { d.Version = "" })), `Chart.yaml: dependencies[0].version "": a pinned chart version`},
	} {
		err := tc.m.Check()
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: %v", name, err)
		case tc.want != "" && (err == nil || !strings.HasPrefix(err.Error(), tc.want)):
			t.Errorf("%s: got %v, want %q", name, err, tc.want)
		}
	}
}

// TestDependencyKeys: a dependency's key, its alias or else its name, is a
// DNS label: it names the umbrella's dependency alias.
func TestDependencyKeys(t *testing.T) {
	for key, ok := range map[string]bool{
		"app": true, "a-1": true, "0": true, strings.Repeat("a", 63): true,
		"../escaped": false, "App": false, "my_app": false, "my.app": false, "-app": false, "app-": false, strings.Repeat("a", 64): false,
	} {
		m := Metadata{APIVersion: "v2", Name: "demo", Version: "1.0.0", Dependencies: []Dependency{{Name: "app", Alias: key, Repository: "https://x", Version: "1.0.0"}}}
		err := m.Check()
		if want := fmt.Sprintf("Chart.yaml: dependencies[0] (%s): key %q: a DNS label", key, key); ok && err != nil || !ok && (err == nil || !strings.HasPrefix(err.Error(), want)) {
			t.Errorf("%q: got %v", key, err)
		}
	}
}
