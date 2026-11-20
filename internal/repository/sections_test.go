package repository

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/okdp/okdp-compiler/internal/okdp"
)

// TestLoadWithSections: the packages of a tree are read for the rule set
// of its compiles: okdp.yaml holds its sections, which the mandatory set
// alone refuses.
func TestLoadWithSections(t *testing.T) {
	tree := fstest.MapFS{
		"catalog/app/1.0.0/Chart.yaml":            file("apiVersion: v2\nname: app\nversion: 1.0.0\n"),
		"catalog/app/1.0.0/okdp.yaml":             file("notes: []\n"),
		"projects/demo/instances/a/instance.yaml": file("service: app\nversion: 1.0.0\n"),
	}
	repos := []Repository{{"db", tree, RoleTree}}
	rules, err := okdp.LoadRules(fstest.MapFS{"rules.yaml": file("apiVersion: okdp-compiler/rules.v1\nname: notes\nversion: 1.0.0\nsections: [notes]\n")})
	if err != nil {
		t.Fatal(err)
	}
	if instances, err := Load(context.Background(), repos, rules); err != nil || len(instances) != 1 {
		t.Errorf("Load: %v %v", instances, err)
	}
	if l, err := LoadProjects(context.Background(), repos, rules); err != nil || len(l.Instances) != 1 {
		t.Errorf("LoadProjects: %v %v", l, err)
	}
	if _, err := Load(context.Background(), repos, nil); err == nil || !strings.Contains(err.Error(), "line 1: field notes not found in type okdp.Package") {
		t.Errorf("Load, the mandatory set: got %v", err)
	}
}
