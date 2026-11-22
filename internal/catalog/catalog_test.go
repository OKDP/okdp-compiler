package catalog

import (
	"errors"
	"io/fs"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

// TestCatalog: the services of two repositories, their versions highest
// first, a package, the contracts of both.
func TestCatalog(t *testing.T) {
	c, err := Load([]Files{
		{Name: "platform-charts", FS: fstest.MapFS{
			"catalog/trino/480.0.0-1.0.0/Chart.yaml":  file("name: trino\n"),
			"catalog/trino/480.0.0-1.0.10/Chart.yaml": file("name: trino\n"),
			"catalog/trino/479.0.0-1.0.0/Chart.yaml":  file("name: trino\n"),
			"catalog/trino/README.md":                 file("not a version\n"),
			"catalog/hive/4.0.1-1.0.0/Chart.yaml":     file("name: hive\n"),
			"contracts/s3.schema.json":                file("{}"),
		}},
		{Name: "sandbox-dependencies", FS: fstest.MapFS{
			"catalog/keycloak/7.3.2-1.0.0/Chart.yaml": file("name: keycloak\n"),
			"contracts/s3.schema.json":                file("{}"),
			"contracts/http.schema.json":              file("{}"),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	services, err := c.Services()
	if err != nil {
		t.Fatal(err)
	}
	want := []Service{
		{Name: "hive", Repository: "platform-charts", Versions: []string{"4.0.1-1.0.0"}},
		{Name: "keycloak", Repository: "sandbox-dependencies", Versions: []string{"7.3.2-1.0.0"}},
		{Name: "trino", Repository: "platform-charts", Versions: []string{"480.0.0-1.0.10", "480.0.0-1.0.0", "479.0.0-1.0.0"}},
	}
	if !reflect.DeepEqual(services, want) {
		t.Errorf("services: got %v, want %v", services, want)
	}

	pkg, err := c.Package("keycloak", "7.3.2-1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if b, err := fs.ReadFile(pkg, "Chart.yaml"); err != nil || string(b) != "name: keycloak\n" {
		t.Errorf("package: got %q, %v", b, err)
	}
	for _, missing := range [][2]string{{"trino", "1.0.0"}, {"trino", "README.md"}, {"nope", "1.0.0"}, {"trino", ".."}, {"../catalog", "x"}} {
		if _, err := c.Package(missing[0], missing[1]); !errors.Is(err, ErrNotInCatalog) {
			t.Errorf("%v: got %v, want ErrNotInCatalog", missing, err)
		}
	}
	if _, err := c.Service("nope"); !errors.Is(err, ErrNotInCatalog) {
		t.Errorf("service nope: got %v, want ErrNotInCatalog", err)
	}

	entries, err := fs.ReadDir(c.Contracts(), ".")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if got := strings.Join(names, " "); got != "http.schema.json s3.schema.json" {
		t.Errorf("contracts: got %s", got)
	}
}

// TestCatalogErrors: a service in two repositories, a contract that
// differs, as Load refuses them.
func TestCatalogErrors(t *testing.T) {
	_, err := Load([]Files{
		{Name: "a", FS: fstest.MapFS{"catalog/trino/1.0.0/Chart.yaml": file("x"), "contracts/s3.schema.json": file("{}")}},
		{Name: "b", FS: fstest.MapFS{"catalog/trino/2.0.0/Chart.yaml": file("x"), "contracts/s3.schema.json": file(`{"title": "other"}`)}},
	})
	for _, want := range []string{`service "trino" is in the repositories a and b`, "contract s3 differs between the repositories a and b"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("got %v, want %q", err, want)
		}
	}
}

// refusingFS: an fs.FS that refuses to open name, permission denied.
type refusingFS struct {
	fs.FS
	name string
}

func (f refusingFS) Open(name string) (fs.File, error) {
	if name == f.name {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrPermission}
	}
	return f.FS.Open(name)
}

// TestCatalogUnreadable: a version directory its repository's fs.FS
// refuses (permission denied) gives that error, named, not
// ErrNotInCatalog: the version is there.
func TestCatalogUnreadable(t *testing.T) {
	c, err := Load([]Files{{Name: "charts", FS: refusingFS{fstest.MapFS{"catalog/web/1.0.0/Chart.yaml": file("name: web\n")}, "catalog/web/1.0.0"}}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Package("web", "1.0.0")
	want := `version "1.0.0" of service "web": charts/catalog/web/1.0.0: permission denied`
	if err == nil || errors.Is(err, ErrNotInCatalog) || err.Error() != want {
		t.Errorf("got %v, want %s", err, want)
	}
}

// TestSortVersions: SemVer by precedence, highest first, then the others.
func TestSortVersions(t *testing.T) {
	versions := []string{"1.0.0", "not-semver", "10.0.0", "2.0.0-1.0.0", "2.0.0", "2.0.0-1.0.2", "another"}
	sortVersions(versions)
	if got, want := strings.Join(versions, " "), "10.0.0 2.0.0 2.0.0-1.0.2 2.0.0-1.0.0 1.0.0 not-semver another"; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func file(s string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(s)} }
