package repository

import (
	"errors"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/okdp/okdp-compiler/internal/okdp"
)

// file: a file of a tree.
func file(s string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(s)} }

// whole: a tree in one repository: the platform, a contract, a package
// in two versions, two projects, demo's with its file and its description,
// two instances, one with its connections and values; other's, an
// instance with its claimers.
var whole = fstest.MapFS{
	"platform.yaml":                              file("global: {okdp: {ingress: {suffix: example.org}}}\n"),
	"contracts/database.schema.json":             file(`{"properties": {"host": {"type": "string"}}}`),
	"contracts/README.md":                        file("not a contract\n"),
	"catalog/app/1.0.0/Chart.yaml":               file("apiVersion: v2\nname: app\nversion: 1.0.0\n"),
	"catalog/app/2.0.0/Chart.yaml":               file("apiVersion: v2\nname: app\nversion: 2.0.0\n"),
	"projects/demo/project.yaml":                 file("description: The demo\nglobal: {team: demo}\nclaims: {reports: {contract: database}}\n"),
	"projects/demo/instances/b/instance.yaml":    file("service: app\nversion: 1.0.0\n"),
	"projects/demo/instances/a/instance.yaml":    file("service: app\nversion: 1.0.0\n"),
	"projects/demo/instances/a/connections.yaml": file("connections: {ext: {contract: database, host: h}}\n"),
	"projects/demo/instances/a/config.yaml":      file("replicas: 2\n"),
	"projects/other/instances/c/instance.yaml":   file("service: app\nversion: 2.0.0\nclaimers: [demo, '*']\n"),
}

// split: a part of whole: the files whose path starts with one of
// prefixes.
func split(prefixes ...string) fstest.MapFS {
	part := fstest.MapFS{}
	for p, f := range whole {
		for _, prefix := range prefixes {
			if strings.HasPrefix(p, prefix) {
				part[p] = f
			}
		}
	}
	return part
}

// summary: what Load gives, by instance: its namespace and release, its
// name, its package, its layers, its claimers when it has some.
func summary(t *testing.T, instances []okdp.Instance) []string {
	t.Helper()
	var got []string
	for _, inst := range instances {
		in := inst.Inputs
		s := in.Namespace + "/" + in.Release + " (" + inst.Name + ") " + inst.Definition.Chart.Name + "-" + inst.Definition.Chart.Version
		for _, l := range in.Context {
			s += " " + strings.Join(slices.Sorted(maps.Keys(l.Values)), ",") + "@" + string(rune('0'+l.Level))
		}
		for _, v := range in.Values {
			s += " " + strings.Join(slices.Sorted(maps.Keys(v)), ",")
		}
		if _, err := in.Contracts.Open("database.schema.json"); err != nil {
			s += " no contract"
		}
		if in.Claimers != nil {
			s += " claimers " + strings.Join(in.Claimers, ",")
		}
		got = append(got, s)
	}
	return got
}

// TestLoad: the instances of a tree, by project then instance, each with
// its release, <project>-<instance>, its name, the instance's directory
// name, its package, one Definition per version, its layers by level,
// the project's without its description, and its claimers, of its
// instance file, nil without; the same tree split into
// repositories, in any order.
func TestLoad(t *testing.T) {
	want := []string{
		"demo/demo-a (a) app-1.0.0 global@0 claims,global@1 connections@2 replicas",
		"demo/demo-b (b) app-1.0.0 global@0 claims,global@1",
		"other/other-c (c) app-2.0.0 global@0 claimers demo,*",
	}
	instances, err := Load([]Repository{{"db", whole}})
	if err != nil {
		t.Fatal(err)
	}
	if got := summary(t, instances); !reflect.DeepEqual(got, want) {
		t.Errorf("got  %q\nwant %q", got, want)
	}
	if instances[0].Definition != instances[1].Definition {
		t.Error("two Definitions of app 1.0.0")
	}
	parts := []Repository{
		{"platform", split("platform.yaml")}, {"catalog", split("catalog/", "contracts/")},
		{"demo", split("projects/demo/", "contracts/")}, {"other", split("projects/other/")},
	}
	for _, repos := range [][]Repository{parts, {parts[3], parts[2], parts[1], parts[0]}} {
		instances, err := Load(repos)
		if err != nil {
			t.Fatal(err)
		}
		if got := summary(t, instances); !reflect.DeepEqual(got, want) {
			t.Errorf("split: got  %q\nwant %q", got, want)
		}
	}
}

// TestLoadErrors: every problem at once, each naming its repository and
// path: the parts in two repositories, a contract that differs; an
// instance file that is not one, a service no catalog has, a version its
// catalog lacks, a package that fails, once, a values file that is not,
// an instance's connections file that holds more than connections, the
// platform's file that declares claims, a project's description that is
// not a string, an instance's claimer that is no namespace name nor "*",
// or listed twice.
func TestLoadErrors(t *testing.T) {
	_, err := Load([]Repository{
		{"b", fstest.MapFS{
			"platform.yaml":                  file("{}\n"),
			"catalog/app/1.0.0/Chart.yaml":   file("apiVersion: v2\nname: app\nversion: 1.0.0\n"),
			"projects/demo/project.yaml":     file("{}\n"),
			"contracts/database.schema.json": file("{}"),
		}},
		{"a", fstest.MapFS{
			"platform.yaml":                  file("{}\n"),
			"catalog/app/1.0.0/Chart.yaml":   file("apiVersion: v2\nname: app\nversion: 1.0.0\n"),
			"projects/demo/project.yaml":     file("{}\n"),
			"contracts/database.schema.json": file(`{"type": "object"}`),
		}},
	})
	want := `contract database differs between the repositories a and b
platform.yaml is in the repositories a and b
service "app" is in the repositories a and b
project "demo" is in the repositories a and b`
	if err == nil || err.Error() != want {
		t.Errorf("got %v\nwant %s", err, want)
	}
	_, err = Load([]Repository{{"db", fstest.MapFS{
		"platform.yaml":                              file("claims: {reports: {contract: database}}\n"),
		"catalog/app/1.0.0/Chart.yaml":               file("apiVersion: v2\nname: app\nversion: 1.0.0\n"),
		"catalog/bad/1.0.0/Chart.yaml":               file("apiVersion: v2\nname: other\nversion: 1.0.0\n"),
		"projects/demo/instances/a/instance.yaml":    file("service: app\nversion: 1.0.0\nreplicas: 2\n"),
		"projects/demo/instances/b/instance.yaml":    file("service: nope\nversion: 1.0.0\n"),
		"projects/demo/instances/c/instance.yaml":    file("service: app\nversion: 9.0.0\n"),
		"projects/demo/instances/d/instance.yaml":    file("service: bad\nversion: 1.0.0\n"),
		"projects/demo/instances/e/instance.yaml":    file("service: bad\nversion: 1.0.0\n"),
		"projects/demo/instances/f/instance.yaml":    file("service: app\nversion: 1.0.0\n"),
		"projects/demo/instances/f/config.yaml":      file("[a\n"),
		"projects/demo/instances/g/config.yaml":      file("{}\n"),
		"projects/demo/instances/h/instance.yaml":    file("service: app\nversion: ../1.0.0\n"),
		"projects/demo/instances/i/instance.yaml":    file("service: app\nversion: 1.0.0\n"),
		"projects/demo/instances/i/connections.yaml": file("connections: {}\nglobal: {okdp: {}}\n"),
		"projects/demo/instances/j/instance.yaml":    file("service: app\nversion: 1.0.0\nclaimers: [demo, Demo]\n"),
		"projects/demo/instances/k/instance.yaml":    file("service: app\nversion: 1.0.0\nclaimers: ['*', '*']\n"),
		"projects/bad/project.yaml":                  file("description: {text: d}\n"),
	}}})
	for _, w := range []string{
		"db/platform.yaml: claims: a project's (project.yaml)",
		"db/projects/demo/instances/a/instance.yaml: yaml: unmarshal errors:\n  line 3: field replicas not found in type struct",
		`db/projects/demo/instances/b/instance.yaml: service "nope" is in the catalog of no repository`,
		`db/projects/demo/instances/c/instance.yaml: version "9.0.0" of service "app" is not in db/catalog/app`,
		"db/projects/demo/instances/d/instance.yaml: the package db/catalog/bad/1.0.0: its Chart.yaml is other 1.0.0\ndb/projects/demo/instances/f/config.yaml: ",
		"db/projects/demo/instances/g/instance.yaml: file does not exist",
		`db/projects/demo/instances/h/instance.yaml: version "../1.0.0" of service "app" is not in db/catalog/app`,
		"db/projects/demo/instances/i/connections.yaml: global: the instance's own connections only (connections): the rest of the context is the platform's or the project's",
		"db/projects/bad/project.yaml: description: a string, the project's description",
		`db/projects/demo/instances/j/instance.yaml: claimers[1]: "Demo" is not a namespace name, nor "*"`,
		"db/projects/demo/instances/k/instance.yaml: claimers: * is listed twice",
	} {
		if err == nil || !strings.Contains(err.Error(), w) {
			t.Errorf("got %v\nwant it to hold %s", err, w)
		}
	}
	if err != nil && strings.Count(err.Error(), "its Chart.yaml is other") != 1 {
		t.Errorf("the package's error more than once: %v", err)
	}
}

// TestError: the problems of a tree that does not load, an *Error, one
// per problem, a report of several lines one of them; the errors they are
// of unwrapped; those a change adds.
func TestError(t *testing.T) {
	_, err := Load([]Repository{{"db", fstest.MapFS{
		"catalog/app/1.0.0/Chart.yaml":            file("apiVersion: v2\nname: app\nversion: 1.0.0\n"),
		"projects/demo/instances/a/instance.yaml": file("service: app\nversion: 1.0.0\nreplicas: 2\n"),
		"projects/demo/instances/b/instance.yaml": file("service: nope\nversion: 1.0.0\n"),
	}}})
	le, ok := errors.AsType[*Error](err)
	if !ok {
		t.Fatalf("%v: not an *Error", err)
	}
	if len(le.Problems) != 2 || !strings.HasPrefix(le.Problems[0], "db/projects/demo/instances/a/instance.yaml: yaml: unmarshal errors:\n  line 3: field replicas not found in type struct") ||
		le.Problems[1] != `db/projects/demo/instances/b/instance.yaml: service "nope" is in the catalog of no repository` || err.Error() != strings.Join(le.Problems, "\n") {
		t.Errorf("problems: %q", le.Problems)
	}
	want := le.Problems
	if !errors.Is(err, ErrNotInCatalog) {
		t.Error("a service the catalog lacks: not ErrNotInCatalog")
	}
	before := &Error{Problems: want[:1]}
	if got := Added(before, le); !reflect.DeepEqual(got, want[1:]) {
		t.Errorf("added: %q", got)
	}
	if got := Added(nil, le); !reflect.DeepEqual(got, want) {
		t.Errorf("added to none: %q", got)
	}
	if got := Added(le, le); got != nil {
		t.Errorf("added nothing: %q", got)
	}
}

// TestParseInstance: an instance's file as written, its claimers absent
// (nil), none (empty) or listed; a key it does not know refused.
func TestParseInstance(t *testing.T) {
	for raw, want := range map[string]InstanceSpec{
		"service: app\nversion: 1.0.0\n":                        {Service: "app", Version: "1.0.0"},
		"service: app\nversion: 1.0.0\nclaimers: []\n":          {Service: "app", Version: "1.0.0", Claimers: []string{}},
		"service: app\nversion: 1.0.0\nclaimers: [demo, '*']\n": {Service: "app", Version: "1.0.0", Claimers: []string{"demo", "*"}},
		"": {},
	} {
		got, err := ParseInstance([]byte(raw))
		if err != nil || !reflect.DeepEqual(got, want) || (got.Claimers == nil) != (want.Claimers == nil) {
			t.Errorf("%q: %#v %v, want %#v", raw, got, err, want)
		}
	}
	if _, err := ParseInstance([]byte("service: app\nname: x\n")); err == nil || !strings.Contains(err.Error(), "field name not found") {
		t.Errorf("an unknown key: %v", err)
	}
}
