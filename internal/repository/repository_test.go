package repository

import (
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
// in two versions, two projects, demo's with its file, two instances, one
// with its connections and values.
var whole = fstest.MapFS{
	"platform.yaml":                              file("global: {okdp: {ingress: {suffix: example.org}}}\n"),
	"contracts/database.schema.json":             file(`{"properties": {"host": {"type": "string"}}}`),
	"contracts/README.md":                        file("not a contract\n"),
	"catalog/app/1.0.0/Chart.yaml":               file("apiVersion: v2\nname: app\nversion: 1.0.0\n"),
	"catalog/app/2.0.0/Chart.yaml":               file("apiVersion: v2\nname: app\nversion: 2.0.0\n"),
	"projects/demo/project.yaml":                 file("global: {team: demo}\n"),
	"projects/demo/instances/b/instance.yaml":    file("service: app\nversion: 1.0.0\n"),
	"projects/demo/instances/a/instance.yaml":    file("service: app\nversion: 1.0.0\n"),
	"projects/demo/instances/a/connections.yaml": file("connections: {ext: {contract: database, host: h}}\n"),
	"projects/demo/instances/a/config.yaml":      file("replicas: 2\n"),
	"projects/other/instances/c/instance.yaml":   file("service: app\nversion: 2.0.0\n"),
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
// package, its layers.
func summary(t *testing.T, instances []okdp.Instance) []string {
	t.Helper()
	var got []string
	for _, inst := range instances {
		in := inst.Inputs
		s := in.Namespace + "/" + in.Release + " " + inst.Definition.Chart.Name + "-" + inst.Definition.Chart.Version
		for _, l := range in.Context {
			s += " " + strings.Join(slices.Sorted(maps.Keys(l.Values)), ",") + "@" + string(rune('0'+l.Level))
		}
		for _, v := range in.Values {
			s += " " + strings.Join(slices.Sorted(maps.Keys(v)), ",")
		}
		if _, err := in.Contracts.Open("database.schema.json"); err != nil {
			s += " no contract"
		}
		got = append(got, s)
	}
	return got
}

// TestLoad: the instances of a tree, by project then instance, each with
// its package, one Definition per version, and its layers by level; the
// same tree split into repositories, in any order.
func TestLoad(t *testing.T) {
	want := []string{
		"demo/a app-1.0.0 global@0 global@1 connections@2 replicas",
		"demo/b app-1.0.0 global@0 global@1",
		"other/c app-2.0.0 global@0",
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
// catalog lacks, a package that fails, once, a values file that is not.
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
		"catalog/app/1.0.0/Chart.yaml":            file("apiVersion: v2\nname: app\nversion: 1.0.0\n"),
		"catalog/bad/1.0.0/Chart.yaml":            file("apiVersion: v2\nname: other\nversion: 1.0.0\n"),
		"projects/demo/instances/a/instance.yaml": file("service: app\nversion: 1.0.0\nreplicas: 2\n"),
		"projects/demo/instances/b/instance.yaml": file("service: nope\nversion: 1.0.0\n"),
		"projects/demo/instances/c/instance.yaml": file("service: app\nversion: 9.0.0\n"),
		"projects/demo/instances/d/instance.yaml": file("service: bad\nversion: 1.0.0\n"),
		"projects/demo/instances/e/instance.yaml": file("service: bad\nversion: 1.0.0\n"),
		"projects/demo/instances/f/instance.yaml": file("service: app\nversion: 1.0.0\n"),
		"projects/demo/instances/f/config.yaml":   file("[a\n"),
		"projects/demo/instances/g/config.yaml":   file("{}\n"),
		"projects/demo/instances/h/instance.yaml": file("service: app\nversion: ../1.0.0\n"),
	}}})
	for _, w := range []string{
		"db/projects/demo/instances/a/instance.yaml: yaml: unmarshal errors:\n  line 3: field replicas not found in type struct",
		`db/projects/demo/instances/b/instance.yaml: service "nope" is in the catalog of no repository`,
		`db/projects/demo/instances/c/instance.yaml: version "9.0.0" of service "app" is not in db/catalog/app`,
		"db/projects/demo/instances/d/instance.yaml: the package db/catalog/bad/1.0.0: its Chart.yaml is other 1.0.0\ndb/projects/demo/instances/f/config.yaml: ",
		"db/projects/demo/instances/g/instance.yaml: file does not exist",
		`db/projects/demo/instances/h/instance.yaml: version "../1.0.0" of service "app" is not in db/catalog/app`,
	} {
		if err == nil || !strings.Contains(err.Error(), w) {
			t.Errorf("got %v\nwant it to hold %s", err, w)
		}
	}
	if err != nil && strings.Count(err.Error(), "its Chart.yaml is other") != 1 {
		t.Errorf("the package's error more than once: %v", err)
	}
}
