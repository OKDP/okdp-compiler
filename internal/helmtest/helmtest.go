// Package helmtest is what the tests against Helm share, imported by tests
// only: the fixtures of testdata/helm, written by `helm template`
// (testdata/helm/generate.sh: Helm 4 on the PATH, and the deployers'
// versions where they render differently; the versions in
// testdata/helm/SOURCE), their JSON renders, the Helm versions and the
// helm<major> files of each deployer's major version.
package helmtest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Dir: testdata/helm, at the repository root, from a package of internal,
// where its tests run.
var Dir = filepath.Join("..", "..", "testdata", "helm")

// JSON: a JSON file written by generate.sh.
func JSON(t *testing.T, path string) any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run testdata/helm/generate.sh)", err)
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return v
}

// source: the words after the key of the lines of testdata/helm/SOURCE
// whose key is one of keys, in their order.
func source(t *testing.T, keys ...string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(Dir, "SOURCE"))
	if err != nil {
		t.Fatalf("%v (run testdata/helm/generate.sh)", err)
	}
	var out []string
	for line := range strings.Lines(string(b)) {
		if f := strings.Fields(line); len(f) > 0 && slices.Contains(keys, f[0]) {
			out = append(out, f[1:]...)
		}
	}
	return out
}

// Versions: the Helm versions testdata/helm/SOURCE records: the helm on
// the PATH (helm) and the deployers' (helms).
func Versions(t *testing.T) []string {
	t.Helper()
	return source(t, "helm", "helms")
}

// Majors: the major versions of the deployers' Helm versions that
// testdata/helm/SOURCE records (helms), each with its versions: the
// versions generate.sh renders a helm<major> file with, where they render
// differently from Helm 4 on the PATH.
func Majors(t *testing.T) map[string][]string {
	t.Helper()
	majors := map[string][]string{}
	for _, v := range source(t, "helms") {
		m, _, _ := strings.Cut(v, ".")
		majors[m] = append(majors[m], v)
	}
	return majors
}

// Variant: the render of a deployer's major version in the case directory
// dir, beside the reference file name (helm.json: helm3.json), nil when it
// renders as the reference.
func Variant(t *testing.T, dir, name, major string) any {
	t.Helper()
	path := filepath.Join(dir, strings.Replace(name, "helm", "helm"+major, 1))
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	return JSON(t, path)
}

// Renders: the renders of a fixture in dir, by name: the reference file
// name (helm.json, helm.txt), which must be there, and the files beside it
// that start with helm and end as it does (helm3.json, where a deployer's
// major version renders differently).
func Renders(t *testing.T, dir, name string) []string {
	t.Helper()
	if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
		t.Fatalf("%v (run testdata/helm/generate.sh)", err)
	}
	renders, _ := filepath.Glob(filepath.Join(dir, strings.Replace(name, "helm", "helm*", 1)))
	return renders
}
