package treeedit

import (
	"context"
	"maps"
	"os"
	"path"
	"strings"
	"testing"
	"testing/fstest"

	compiler "github.com/okdp/okdp-compiler"
)

// compileReader compiles the tree r holds through the Reader alone, its
// platform.yaml and projects/, with the test catalog: compile for a
// Reader that is no memTree.
func compileReader(r Reader) (*compiler.Tree, error) {
	tree := fstest.MapFS{}
	var walk func(dir string) error
	walk = func(dir string) error {
		names, err := r.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, n := range names {
			p := path.Join(dir, n)
			if data, err := r.ReadFile(p); err == nil {
				tree[p] = &fstest.MapFile{Data: data}
			} else if err := walk(p); err != nil {
				return err
			}
		}
		return nil
	}
	if data, err := r.ReadFile(compiler.PlatformFile); err == nil {
		tree[compiler.PlatformFile] = &fstest.MapFile{Data: data}
	}
	if err := walk(compiler.ProjectsDir); err != nil {
		return nil, err
	}
	return compiler.Build(context.Background(), []compiler.Repository{
		{Name: ".", FS: tree, Role: compiler.RoleTree},
		{Name: "test-charts", FS: os.DirFS(catalogFixture), Role: compiler.RoleCatalog},
	}, compiler.BuildOptions{})
}

// The field errors of an instance are its compile's, each at its
// parameter path, and the tree is left as it is.
func TestValidateInstance(t *testing.T) {
	s := newStore(t)
	before := s.tree.files()
	web := Instance{Project: "demo", Name: "web", Service: "app", Version: "1.0.0"}
	for _, c := range []struct {
		name    string
		values  map[string]any
		path    string
		pointer string
		message string
	}{
		{"a valid one", map[string]any{"database": "analytics"}, "", "", ""},
		{"a schema violation", map[string]any{"database": 5.0}, "database", "/database", "at '/database': got number, want string or object"},
		{"an unknown connection", map[string]any{"database": "nowhere"}, "database", "/database", `database: "nowhere" is not a connection`},
		{"a class that is none", map[string]any{"database": map[string]any{"class": "huge"}}, "database", "/database", `class "huge" is not a class of database-server connections`},
	} {
		t.Run(c.name, func(t *testing.T) {
			inst := web
			inst.Values = c.values
			fields, _, err := ValidateInstance(s.tree, compileReader, inst)
			if err != nil {
				t.Fatal(err)
			}
			if c.message == "" {
				if fields != nil {
					t.Fatalf("field errors of a valid instance: %+v", fields)
				}
				return
			}
			if len(fields) != 1 || fields[0].Path != c.path || fields[0].Pointer != c.pointer || !strings.Contains(fields[0].Message, c.message) {
				t.Fatalf("field errors = %+v, want %s (%s) %q", fields, c.path, c.pointer, c.message)
			}
		})
	}
	// An update: the instance's directory is there.
	fields, _, err := ValidateInstance(s.tree, compileReader, Instance{Project: "demo", Name: "legacy", Service: "app", Version: "1.0.0", Values: map[string]any{"database": "gone"}})
	if err != nil || len(fields) != 1 || fields[0].Path != "database" {
		t.Fatalf("an update: %+v %v", fields, err)
	}
	if !maps.Equal(before, s.tree.files()) {
		t.Error("a validation changed the tree")
	}
}

// A named claim is checked as its project's instances bind it, at
// claims.<name>.
func TestValidateClaim(t *testing.T) {
	s := newStore(t)
	fields, _, err := ValidateClaim(s.tree, compileReader, "demo", compiler.ClaimSpec{Name: "analytics", Contract: "database-server", Class: "huge"})
	if err != nil {
		t.Fatal(err)
	}
	if len(fields) == 0 || fields[0].Path != "claims.analytics" || !strings.Contains(fields[0].Message, `class "huge"`) {
		t.Fatalf("field errors = %+v", fields)
	}
	fields, _, err = ValidateClaim(s.tree, compileReader, "demo", compiler.ClaimSpec{Name: "batch", Contract: "database-server"})
	if err != nil || fields != nil {
		t.Fatalf("a new claim of the default class: %+v %v", fields, err)
	}
	// A project without instance: its claims checked at their declaration
	// all the same, bound to none.
	s.tree["projects/idle/project.yaml"] = []byte("description: no instance yet\n")
	fields, _, err = ValidateClaim(s.tree, compileReader, "idle", compiler.ClaimSpec{Name: "analytics", Contract: "database-server", Class: "huge"})
	if err != nil || len(fields) == 0 || fields[0].Path != "claims.analytics" || !strings.Contains(fields[0].Message, `class "huge"`) {
		t.Fatalf("a project without instance: %+v %v", fields, err)
	}
	if fields, _, err = ValidateClaim(s.tree, compileReader, "idle", compiler.ClaimSpec{Name: "batch", Contract: "database-server"}); err != nil || fields != nil {
		t.Fatalf("a project without instance, a claim of the default class: %+v %v", fields, err)
	}
}

// The overlay is the tree a change makes: a removed directory gone, a
// file written in it there.
func TestOverlay(t *testing.T) {
	base := memTree{"a/b/c": []byte("1"), "a/d": []byte("2")}
	o := newOverlay(base)
	_ = o.Remove("a/b")
	_ = o.WriteFile("a/e/f", []byte("3"))
	if o.Exists("a/b") || o.Exists("a/b/c") || !o.Exists("a/e") {
		t.Error("Exists")
	}
	if names, _ := o.ReadDir("a"); strings.Join(names, ",") != "d,e" {
		t.Errorf("ReadDir = %v", names)
	}
	_ = o.WriteFile("a/b/g", []byte("4"))
	if names, _ := o.ReadDir("a/b"); strings.Join(names, ",") != "g" {
		t.Errorf("ReadDir after a write = %v", names)
	}
	if _, err := o.ReadFile("a/b/c"); err == nil {
		t.Error("a removed file read")
	}
	if len(base) != 2 {
		t.Error("the base changed")
	}
}
