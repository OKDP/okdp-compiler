package repository

import (
	"errors"
	"slices"
	"testing"
	"testing/fstest"

	"github.com/okdp/okdp-compiler/internal/okdp"
)

// FuzzLoad: a tree whatever its files hold: Load and LoadProjects never
// panic and give the same answer twice; a tree Load accepts is accepted by
// LoadProjects with no project refused, and each of its instances has its
// project's namespace, its directory's name and the release that is the
// project's and its name (the compile checks they are labels); a tree it
// refuses is an *Error
// with a problem at least; and LoadProjects gives no instance to a project
// it refuses, which says why. Seeds: the files of TestLoad, TestLoadErrors
// and TestLoadProjects.
func FuzzLoad(f *testing.F) {
	platform := "global: {okdp: {ingress: {suffix: example.org}}}\n"
	instance := "service: app\nversion: 1.0.0\n"
	for _, s := range [][5]string{
		{platform, "description: The demo\nglobal: {team: demo}\nclaims: {reports: {contract: database}}\n", instance, "replicas: 2\n", "connections: {ext: {contract: database, host: h}}\n"},
		{"{}\n", "{}\n", instance, "", ""},
		{"claims: {reports: {contract: database}}\n", "", instance + "replicas: 2\n", "[a\n", "connections: {}\nglobal: {okdp: {}}\n"},
		{platform, "[a\n", instance, "", ""},
		{platform, "description: {text: d}\n", "service: nope\nversion: 1.0.0\n", "{}\n", ""},
		{platform, "", "service: app\nversion: ../1.0.0\n", "", ""},
		{platform, "", instance + "claimers: [demo, '*']\n", "", ""},
		{platform, "", instance + "claimers: [demo, Demo]\n", "", ""},
		{platform, "access: {analysts: viewer}\n", instance, "global: {okdp: {}}\n", "connections: {a: {contract: x, b: c}}\n"},
		{"", "", "", "", ""},
	} {
		f.Add([]byte(s[0]), []byte(s[1]), []byte(s[2]), []byte(s[3]), []byte(s[4]), "a")
	}
	f.Add([]byte("{}\n"), []byte(""), []byte("service: app\nversion: 1.0.0\n"), []byte(""), []byte(""), "Not_A_Name")
	f.Add([]byte("{}\n"), []byte(""), []byte("service: app\nversion: 1.0.0\n"), []byte(""), []byte(""), "..")
	f.Add([]byte("{}\n"), []byte(""), []byte("service: app\nversion: 1.0.0\n"), []byte(""), []byte(""), "a/b")
	f.Fuzz(func(t *testing.T, platform, project, instance, config, connections []byte, dir string) {
		tree := fstest.MapFS{
			"catalog/app/1.0.0/Chart.yaml":                         {Data: []byte("apiVersion: v2\nname: app\nversion: 1.0.0\n")},
			"contracts/database.schema.json":                       {Data: []byte(`{"properties": {"host": {"type": "string"}}}`)},
			"platform.yaml":                                        {Data: platform},
			"projects/demo/project.yaml":                           {Data: project},
			"projects/demo/instances/" + dir + "/instance.yaml":    {Data: instance},
			"projects/demo/instances/" + dir + "/config.yaml":      {Data: config},
			"projects/demo/instances/" + dir + "/connections.yaml": {Data: connections},
			"projects/other/instances/b/instance.yaml":             {Data: []byte("service: app\nversion: 1.0.0\n")},
		}
		repos := []Repository{{Name: "db", FS: tree, Role: RoleTree}}
		got, err := Load(repos)
		again, err2 := Load(repos)
		if errorString(err) != errorString(err2) || len(got) != len(again) {
			t.Fatalf("Load: not the same twice: %v / %v", err, err2)
		}
		l, lerr := LoadProjects(repos)
		if lerr2 := func() error { _, e := LoadProjects(repos); return e }(); errorString(lerr) != errorString(lerr2) {
			t.Fatalf("LoadProjects: not the same twice: %v / %v", lerr, lerr2)
		}
		if err != nil {
			if e, ok := errors.AsType[*Error](err); !ok || len(e.Problems) == 0 {
				t.Fatalf("Load: %v is no *Error with a problem", err)
			}
			if lerr == nil && len(l.Refused) == 0 {
				t.Fatalf("Load refuses the tree (%v), LoadProjects loads it all", err)
			}
		} else if lerr != nil || len(l.Refused) > 0 || len(l.Instances) != len(got) {
			t.Fatalf("Load accepts the tree, LoadProjects: %v, %d refused, %d instances of %d", lerr, len(l.Refused), len(l.Instances), len(got))
		}
		for _, inst := range got {
			in := inst.Inputs
			if inst.Definition == nil || in.Namespace == "" || inst.Name == "" || in.Release != ReleaseName(in.Namespace, inst.Name) {
				t.Fatalf("an instance %q %q of namespace %q", in.Release, inst.Name, in.Namespace)
			}
		}
		if lerr != nil {
			return
		}
		for _, r := range l.Refused {
			if len(r.Problems) == 0 || len(r.Problems) != len(r.Errs) || r.Inputs.Namespace != r.Project {
				t.Fatalf("project %s refused: %q, %v", r.Project, r.Problems, r.Errs)
			}
			if slices.ContainsFunc(l.Instances, func(i okdp.Instance) bool { return i.Inputs.Namespace == r.Project }) {
				t.Fatalf("project %s refused and loaded", r.Project)
			}
		}
	})
}

// errorString: err's message, "" for none.
func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
