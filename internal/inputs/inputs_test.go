package inputs

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/okdp/okdp-compiler/internal/catalog"
	"github.com/okdp/okdp-compiler/internal/okdp"
)

func mapFile(s string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(s)} }

// testCatalog: one package, app 1.0.0, with no mapping.
func testCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	c, err := catalog.Load([]catalog.Files{{Name: "charts", FS: fstest.MapFS{
		"catalog/app/1.0.0/Chart.yaml": mapFile("apiVersion: v2\nname: app\nversion: 1.0.0\n"),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// TestLoad: documents in several files of any layout, a values file
// beside them, the layers and groups each instance gets, the claimers
// absent or empty.
func TestLoad(t *testing.T) {
	src := Files{Name: "in", FS: fstest.MapFS{
		"settings.yaml":           mapFile("kind: Settings\nvalues: {global: {region: eu}}\n"),
		"teams/demo.yaml":         mapFile("kind: Project\nname: demo\naccess: {devs: editor, leads: admin}\nvaluesFiles: [demo/project.yaml]\n---\nkind: Instance\nproject: demo\nname: web\nservice: app\nversion: 1.0.0\nclaimers: []\nconnections: {db: {contract: database}}\nvaluesFiles: [demo/web.yaml]\nvalues: {replicas: 3}\n"),
		"teams/demo/project.yaml": mapFile("global: {team: demo}\n"),
		"teams/demo/web.yaml":     mapFile("replicas: 2\nkind: not a document\n"),
		"lab/api.yml":             mapFile("kind: Instance\nproject: lab\nname: api\nservice: app\nversion: 1.0.0\n"),
	}}
	instances, err := Load(context.Background(), []Files{src}, testCatalog(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 2 || instances[0].Inputs.Release != "demo-web" || instances[1].Inputs.Release != "lab-api" {
		t.Fatalf("instances: %+v", instances)
	}
	web, api := instances[0], instances[1]
	if web.Name != "web" || web.Inputs.Namespace != "demo" || web.Inputs.Claimers == nil || len(web.Inputs.Claimers) != 0 {
		t.Errorf("web: %+v", web.Inputs)
	}
	if api.Inputs.Claimers != nil {
		t.Errorf("api: claimers %v, want nil: absent", api.Inputs.Claimers)
	}
	// the Settings', the project's values file, the connections, the
	// instance's values file and values.
	if len(web.Inputs.Values) != 5 || web.Inputs.Values[0]["global"] == nil || web.Inputs.Values[1]["global"] == nil ||
		web.Inputs.Values[2]["connections"] == nil || web.Inputs.Values[3]["replicas"] != float64(2) || web.Inputs.Values[4]["replicas"] != float64(3) {
		t.Errorf("web's values: %v", web.Inputs.Values)
	}
	if want := (map[string]any{"access": map[string]any{"groups": map[string]any{"devs": string(okdp.RoleEditor), "leads": string(okdp.RoleAdmin)}}}); !reflect.DeepEqual(web.Inputs.Settings, want) {
		t.Errorf("web's groups: %v", web.Inputs.Settings)
	}
	if len(api.Inputs.Values) != 1 || api.Inputs.Settings != nil {
		t.Errorf("api: %v, %v: the Settings' values alone, no group", api.Inputs.Values, api.Inputs.Settings)
	}
}

// TestLoadErrors: every problem at once, each named by its file and
// document.
func TestLoadErrors(t *testing.T) {
	src := Files{Name: "in", FS: fstest.MapFS{
		"a.yaml": mapFile("kind: Settings\n---\nkind: Settings\n---\nkind: Project\nname: demo\n---\nkind: Project\nname: demo\n---\nkind: Project\nname: lab\naccess: {a/b: viewer, devs: owner}\n---\nkind: Instance\nproject: demo\nname: web\nservice: app\nversion: 1.0.0\n"),
		"b.yaml": mapFile("kind: Instance\nproject: demo\nname: web\nservice: app\nversion: 1.0.0\n---\nkind: Instance\nproject: Demo\nname: x\nservice: app\nversion: 2.0.0\n---\nkind: Instance\nproject: lab\nname: y\nservice: app\nversion: 1.0.0\nvaluesFiles: [../outside.yaml]\nclaimers: [a, a]\nreplicas: 1\n"),
		"c.yaml": mapFile("replicas: 2\n"),
		"d.yaml": mapFile("kind: Deployment\n"),
	}}
	_, err := Load(context.Background(), []Files{src}, testCatalog(t), nil)
	if err == nil {
		t.Fatal("no error")
	}
	for _, want := range []string{
		"in/a.yaml: document 2 (line 3, Settings): a second Settings document, after in/a.yaml: document 1 (line 1, Settings)",
		`in/a.yaml: document 4 (line 8, Project): project "demo" is given twice, first in in/a.yaml: document 3 (line 5, Project)`,
		"in/b.yaml: document 3 (line 13, Instance): line 20: replicas: not a key of the document, which holds kind, project, name, service, version, claimers, connections, values, valuesFiles\n",
		"in/b.yaml: document 1 (line 1, Instance): instance demo/web is given twice",
		"in/b.yaml: document 2 (line 7, Instance): line 8: project: a DNS label expected",
		`valuesFiles: "../outside.yaml": a path relative to the document's file, inside in`,
		"claimers: a is listed twice",
		"in/a.yaml: document 5 (line 11, Project): access.a/b: a group name",
		`access.devs: a role, viewer, editor or admin, got "owner"`,
		"in/c.yaml: document 1 (line 1): no kind",
		`in/d.yaml: document 1 (line 1): kind "Deployment"`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("got\n%v\nwant it to hold %s", err, want)
		}
	}
}
