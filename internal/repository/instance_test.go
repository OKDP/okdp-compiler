package repository

import (
	"maps"
	"strings"
	"testing"
)

// TestInstanceFiles: an instance's directory holds its files only: any
// other entry, a file or a directory, refused, each named.
func TestInstanceFiles(t *testing.T) {
	tree := maps.Clone(whole)
	tree["projects/demo/instances/a/values.yaml"] = file("replicas: 3\n")
	tree["projects/demo/instances/a/config.yaml/x"] = file("")
	tree["projects/demo/instances/b/.keep"] = file("")
	delete(tree, "projects/demo/instances/a/config.yaml")
	_, err := Load([]Repository{{"db", tree, RoleTree}})
	want := []string{
		"db/projects/demo/instances/a/config.yaml: not a file of an instance, which holds instance.yaml, config.yaml and connections.yaml only: no compile reads it",
		"db/projects/demo/instances/a/values.yaml: not a file of an instance, which holds instance.yaml, config.yaml and connections.yaml only: no compile reads it",
		"db/projects/demo/instances/b/.keep: not a file of an instance, which holds instance.yaml, config.yaml and connections.yaml only: no compile reads it",
	}
	if err == nil || err.Error() != strings.Join(want, "\n") {
		t.Errorf("got\n%v\nwant\n%s", err, strings.Join(want, "\n"))
	}
}
