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
	instances, err := Load([]Repository{{"db", whole, RoleTree}})
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
		{"platform", split("platform.yaml"), RoleTree}, {"catalog", split("catalog/", "contracts/"), RoleCatalog},
		{"demo", split("projects/demo/", "contracts/"), RoleTree}, {"other", split("projects/other/"), RoleTree},
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
		}, RoleTree},
		{"a", fstest.MapFS{
			"platform.yaml":                  file("{}\n"),
			"catalog/app/1.0.0/Chart.yaml":   file("apiVersion: v2\nname: app\nversion: 1.0.0\n"),
			"projects/demo/project.yaml":     file("{}\n"),
			"contracts/database.schema.json": file(`{"type": "object"}`),
		}, RoleTree},
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
	}, RoleTree}})
	for _, w := range []string{
		"db/platform.yaml: claims: a project's (project.yaml)",
		"db/projects/demo/instances/a/instance.yaml: yaml: unmarshal errors:\n  line 3: field replicas not found: service, version and claimers only",
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
	}, RoleTree}})
	le, ok := errors.AsType[*Error](err)
	if !ok {
		t.Fatalf("%v: not an *Error", err)
	}
	if len(le.Problems) != 2 || !strings.HasPrefix(le.Problems[0], "db/projects/demo/instances/a/instance.yaml: yaml: unmarshal errors:\n  line 3: field replicas not found: service, version and claimers only") ||
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
	// Every problem at once, by line, no Go type in a message.
	for raw, want := range map[string]string{
		"service: app\nname: x\nclaimers: a\nversion: [1]\n": "yaml: unmarshal errors:\n  line 2: field name not found: service, version and claimers only\n  line 3: claimers: a list of namespace names expected\n  line 4: version: a string expected",
		"service: a\nservice: b\n":                           "yaml: unmarshal errors:\n  line 2: mapping key \"service\" already defined at line 1",
		"claimers: [a, {b: c}]\n":                            "yaml: unmarshal errors:\n  line 1: claimers: a list of namespace names expected",
		"- service\n":                                        "yaml: unmarshal errors:\n  line 1: a mapping of service, version and claimers expected",
		"service: {a: b}\n":                                  "yaml: unmarshal errors:\n  line 1: service: a string expected",
	} {
		_, err := ParseInstance([]byte(raw))
		if err == nil || err.Error() != want || strings.Contains(err.Error(), "struct") || strings.Contains(err.Error(), "[]string") {
			t.Errorf("%q: got %v, want %q", raw, err, want)
		}
	}
	// Null stands for none, as it did; a number is the string of its digits.
	if got, err := ParseInstance([]byte("service: app\nversion: 1.0\nclaimers:\n")); err != nil || got.Version != "1.0" || got.Claimers != nil {
		t.Errorf("null claimers, a number version: %+v %v", got, err)
	}
	if got, err := ParseInstance([]byte("---\n")); err != nil || got.Service != "" {
		t.Errorf("an empty document: %+v %v", got, err)
	}
}

// TestLoadRoles: a repository has a role, the zero value refused; a
// catalog's platform.yaml or projects/, there at all, refused, naming the
// repository and the path, the project of a privileged name it would add
// among them; its catalog/ and contracts/ read.
func TestLoadRoles(t *testing.T) {
	tree := fstest.MapFS{
		"platform.yaml":              file("global: {okdp: {policy: {privileged: [strimzi]}}}\n"),
		"projects/demo/project.yaml": file("{}\n"),
	}
	catalog := fstest.MapFS{
		"catalog/app/1.0.0/Chart.yaml":                   file("apiVersion: v2\nname: app\nversion: 1.0.0\n"),
		"contracts/database.schema.json":                 file("{}"),
		"projects/strimzi/instances/x/instance.yaml":     file("service: app\nversion: 1.0.0\n"),
		"projects/kube-system/instances/y/instance.yaml": file("service: app\nversion: 1.0.0\n"),
		"platform.yaml": file("{}\n"),
	}
	_, err := Load([]Repository{{"tree", tree, RoleTree}, {"evil", catalog, RoleCatalog}})
	for _, w := range []string{
		"evil/platform.yaml: a catalog repository holds catalog/ and contracts/ only: platform.yaml is the deployment's (a repository of role tree)",
		"evil/projects: a catalog repository holds catalog/ and contracts/ only: projects is the deployment's (a repository of role tree)",
	} {
		if err == nil || !strings.Contains(err.Error(), w) {
			t.Errorf("got %v\nwant it to hold %s", err, w)
		}
	}
	if err != nil && strings.Contains(err.Error(), "is in the repositories") {
		t.Errorf("a catalog's platform.yaml owned: %v", err)
	}
	for _, repos := range [][]Repository{{{"tree", tree, ""}}, {{"tree", tree, "platform"}}} {
		for name, load := range map[string]func([]Repository) error{
			"Load":        func(r []Repository) error { _, err := Load(r); return err },
			"LoadCatalog": func(r []Repository) error { _, err := LoadCatalog(r); return err },
		} {
			if err := load(repos); err == nil || !strings.Contains(err.Error(), "repository tree: ") || !strings.Contains(err.Error(), "tree, a part of the deployment, or catalog, packages and contracts only") {
				t.Errorf("%s, role %q: got %v", name, repos[0].Role, err)
			}
		}
	}
	// A catalog of catalog/ and contracts/ only: its packages and
	// contracts serve the tree's instances.
	delete(catalog, "platform.yaml")
	delete(catalog, "projects/strimzi/instances/x/instance.yaml")
	delete(catalog, "projects/kube-system/instances/y/instance.yaml")
	tree["projects/demo/instances/web/instance.yaml"] = file("service: app\nversion: 1.0.0\n")
	instances, err := Load([]Repository{{"tree", tree, RoleTree}, {"charts", catalog, RoleCatalog}})
	if err != nil || len(instances) != 1 || instances[0].Inputs.Namespace != "demo" || instances[0].Inputs.Contracts == nil {
		t.Errorf("got %v, %v", instances, err)
	}
}

// TestLoadProjects: a project's own problem (a stray file in an instance's
// directory, a key instance.yaml does not know, a project.yaml that does
// not parse, a version its catalog lacks) refuses that project as a
// whole, its other instances too, the others loaded; the context of a
// project whose project.yaml reads is kept for what the platform decides
// of it. A problem of the platform's parts (platform.yaml, a package that
// fails, a contract that differs) is an error, the projects' problems
// among them; Load lists every problem, as before.
func TestLoadProjects(t *testing.T) {
	tree := fstest.MapFS{
		"platform.yaml":                              file("global: {okdp: {ingress: {suffix: example.org}}}\n"),
		"catalog/app/1.0.0/Chart.yaml":               file("apiVersion: v2\nname: app\nversion: 1.0.0\n"),
		"projects/good/instances/a/instance.yaml":    file("service: app\nversion: 1.0.0\n"),
		"projects/stray/project.yaml":                file("global: {team: stray}\n"),
		"projects/stray/instances/a/instance.yaml":   file("service: app\nversion: 1.0.0\n"),
		"projects/stray/instances/b/instance.yaml":   file("service: app\nversion: 1.0.0\n"),
		"projects/stray/instances/b/values.yaml":     file("replicas: 2\n"),
		"projects/unknown/instances/a/instance.yaml": file("service: app\nversion: 1.0.0\nreplicas: 2\n"),
		"projects/broken/project.yaml":               file("[a\n"),
		"projects/broken/instances/a/instance.yaml":  file("service: app\nversion: 1.0.0\n"),
		"projects/old/instances/a/instance.yaml":     file("service: app\nversion: 9.0.0\n"),
	}
	l, err := LoadProjects([]Repository{{"db", tree, RoleTree}})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := summary(t, l.Instances), []string{"good/good-a (a) app-1.0.0 global@0 no contract"}; !reflect.DeepEqual(got, want) {
		t.Errorf("instances: got %q\nwant %q", got, want)
	}
	var names []string
	for _, r := range l.Refused {
		names = append(names, r.Project)
		if len(r.Problems) == 0 || len(r.Problems) != len(r.Errs) || r.Problems[0] != r.Errs[0].Error() {
			t.Errorf("%s: problems %q, errors %v", r.Project, r.Problems, r.Errs)
		}
		if r.Inputs.Namespace != r.Project || r.Own != (r.Project != "broken") {
			t.Errorf("%s: inputs %+v, own %v", r.Project, r.Inputs, r.Own)
		}
	}
	if want := []string{"broken", "old", "stray", "unknown"}; !reflect.DeepEqual(names, want) {
		t.Errorf("refused: got %q, want %q", names, want)
	}
	in := l.Refused[2].Inputs
	if l.Refused[0].Inputs.Context[0].Level != 0 || len(l.Refused[0].Inputs.Context) != 1 {
		t.Errorf("broken's inputs: the platform's layer alone: %+v", l.Refused[0].Inputs)
	}
	if in.Namespace != "stray" || len(in.Context) != 2 || in.Context[1].Values["global"] == nil {
		t.Errorf("stray's inputs: %+v", in)
	}
	if got := l.Refused[2].Problems; len(got) != 1 || !strings.HasPrefix(got[0], "db/projects/stray/instances/b/values.yaml: not a file of an instance") {
		t.Errorf("stray's problems: %q", got)
	}
	if !errors.Is(l.Refused[1].Errs[0], ErrNotInCatalog) {
		t.Errorf("old: %v", l.Refused[1].Errs[0])
	}
	// Load: any problem fails the tree, every one listed, in name order.
	_, err = Load([]Repository{{"db", tree, RoleTree}})
	le, ok := errors.AsType[*Error](err)
	if !ok || len(le.Problems) != 4 || !strings.HasPrefix(le.Problems[0], "db/projects/broken/project.yaml: ") || !errors.Is(err, ErrNotInCatalog) {
		t.Errorf("Load: %v", err)
	}
	// The platform's parts fail the tree, the projects' problems listed
	// with theirs.
	tree["platform.yaml"] = file("[a\n")
	tree["catalog/bad/1.0.0/Chart.yaml"] = file("apiVersion: v2\nname: other\nversion: 1.0.0\n")
	tree["projects/good/instances/b/instance.yaml"] = file("service: bad\nversion: 1.0.0\n")
	tree["projects/good2/instances/c/instance.yaml"] = file("service: bad\nversion: 1.0.0\n")
	l, err = LoadProjects([]Repository{{"db", tree, RoleTree}})
	if l != nil || err == nil {
		t.Fatalf("a platform that does not parse: %v %v", l, err)
	}
	for _, w := range []string{"db/platform.yaml: ", "db/projects/stray/instances/b/values.yaml: not a file", "the package db/catalog/bad/1.0.0: its Chart.yaml is other 1.0.0"} {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("got %v\nwant it to hold %s", err, w)
		}
	}
	// A package that fails is the catalog's, not a project's: the tree
	// fails, whatever project names it first.
	delete(tree, "platform.yaml")
	l, err = LoadProjects([]Repository{{"db", tree, RoleTree}})
	if l != nil || err == nil || strings.Count(err.Error(), "its Chart.yaml is other") != 1 {
		t.Errorf("a package that fails: %v %v", l, err)
	}
}
