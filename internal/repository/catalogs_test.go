package repository

import (
	"maps"
	"strings"
	"testing"
	"testing/fstest"
)

// TestParseCatalogs: catalogs.yaml read strictly, every problem at once,
// in the order of the file; a ref main by default.
func TestParseCatalogs(t *testing.T) {
	c, err := ParseCatalogs(map[string]any{
		"repositories": []any{map[string]any{"name": "a", "url": "https://git/a.git"}},
		"categories": []any{map[string]any{"title": "Databases", "icon": "pi-database", "services": []any{
			map[string]any{"name": "app", "default": "1.0.0", "exposesUI": true},
		}}},
	})
	if err != nil || c.Repositories[0].Ref != DefaultCatalogRef || c.Categories[0].Services[0].Default != "1.0.0" || !*c.Categories[0].Services[0].ExposesUI {
		t.Fatalf("%+v %v", c, err)
	}
	_, err = ParseCatalogs(map[string]any{
		"serviceCatalog": map[string]any{},
		"repositories": []any{
			map[string]any{"name": "a", "url": "u", "branch": "x"},
			map[string]any{"name": "a"},
		},
		"categories": []any{
			map[string]any{"title": "A", "services": []any{map[string]any{"name": "app", "versions": []any{"1.0.0"}}}},
			map[string]any{"title": "A", "services": []any{map[string]any{"name": "app", "default": 1.0}}},
			map[string]any{"icon": "x"},
			"B",
		},
	})
	want := []string{
		`serviceCatalog: unknown key: repositories, categories only`,
		`repositories[0].branch: unknown key: name, url, ref only`,
		`repositories[1]: url is required`,
		`repositories[1]: two catalogs are named "a"`,
		`categories[0].services[0].versions: unknown key: name, description, default, label, exposesUI only`,
		`categories[1]: two categories are titled "A"`,
		`categories[1].services[0].default: a string, got 1`,
		`categories[1].services[0]: service "app" is listed twice in the menu, at categories[0].services[0] too: a service is in one category`,
		`categories[2]: title is required`,
		`categories[2]: services is required, a list of {name, description, default, label, exposesUI}`,
		`categories[3]: an object {title, icon, services}, got "B"`,
	}
	if err == nil || err.Error() != strings.Join(want, "\n") {
		t.Errorf("got\n%v\nwant\n%s", err, strings.Join(want, "\n"))
	}
}

// TestLoadCatalogsFile: a tree's catalogs.yaml checked by Load: its
// menu's services of the catalogs, their default versions; in one
// repository of the tree at most.
func TestLoadCatalogsFile(t *testing.T) {
	tree := maps.Clone(whole)
	tree["catalogs.yaml"] = file("repositories: [{name: c, url: u}]\ncategories:\n  - title: Apps\n    services: [{name: app, default: 2.0.0}, {name: gone}, {name: app2, default: 9.0.0}]\n")
	tree["catalog/app2/1.0.0/Chart.yaml"] = file("apiVersion: v2\nname: app2\nversion: 1.0.0\n")
	_, err := Load([]Repository{{"db", tree, RoleTree}})
	want := []string{
		`db/catalogs.yaml: categories[0].services[1]: service "gone" is in no catalog (catalog/gone/ of no repository)`,
		`db/catalogs.yaml: categories[0].services[2].default: version "9.0.0" of service "app2"`,
	}
	if err == nil || !strings.HasPrefix(err.Error(), want[0]+"\n"+want[1]) {
		t.Errorf("got\n%v\nwant\n%s", err, strings.Join(want, "\n"))
	}
	tree["catalogs.yaml"] = file("repositories: [{name: c, url: u}]\ncategories: [{title: Apps, services: [{name: app}]}]\n")
	if _, err := Load([]Repository{{"db", tree, RoleTree}}); err != nil {
		t.Fatal(err)
	}
	other := fstest.MapFS{"catalogs.yaml": file("repositories: []\n")}
	if _, err := Load([]Repository{{"db", tree, RoleTree}, {"other", other, RoleTree}}); err == nil || !strings.Contains(err.Error(), "catalogs.yaml is in the repositories db and other") {
		t.Errorf("two catalogs.yaml: %v", err)
	}
}
