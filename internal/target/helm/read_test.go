package helm

import (
	"errors"
	"io/fs"
	"path"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/okdp/okdp-compiler/internal/chart"
	"github.com/okdp/okdp-compiler/internal/okdp"
	"github.com/okdp/okdp-compiler/internal/target"
)

// TestReadCompiled: the files Write gives, read back: the package, what
// the instance publishes, its Chart.yaml, its values; an instance of no
// Chart.yaml not compiled, skipped by ReadPublished.
func TestReadCompiled(t *testing.T) {
	app := chart.Ref{Repository: "https://charts.example.org", Chart: "app", Version: "1.0.0"}
	info := okdp.Info{URL: "https://db.example.org", Provides: []okdp.Output{{Name: "db", Contract: "database", Values: map[string]any{"port": 5432}}}}
	res := &okdp.Result{
		Namespace: "data", Release: "data-db",
		Values: map[string]map[string]any{"app": {"replicas": 2}},
		Charts: map[string]chart.Ref{"app": app},
		Chart:  chart.Metadata{APIVersion: "v2", Name: "db", Version: "1.2.0", Dependencies: []chart.Dependency{{Name: "app", Repository: app.Repository, Version: app.Version}}},
		Info:   info,
	}
	files, err := Target{}.Files([]*okdp.Result{res, {Namespace: "data", Release: "data-plain", Values: map[string]map[string]any{}, Chart: chart.Metadata{APIVersion: "v2", Name: "plain", Version: "0.1.0"}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	output := fstest.MapFS{"projects/data/instances/data-gone/README": {Data: []byte("x")}}
	for p, b := range files {
		output[p] = &fstest.MapFile{Data: b}
	}

	c, err := ReadCompiled(output, "data", "data-db")
	if err != nil {
		t.Fatal(err)
	}
	if c.Release != "data-db" || c.Service != "db" || c.Version != "1.2.0" || !reflect.DeepEqual(c.Info, info) {
		t.Errorf("published: %+v", c.Published)
	}
	if string(c.Chart) != string(output[path.Join(target.InstanceDir("data", "data-db"), "Chart.yaml")].Data) || !reflect.DeepEqual(c.Values["app"], map[string]any{"replicas": 2.0}) {
		t.Errorf("compiled: %s %v", c.Chart, c.Values)
	}
	if _, err := ReadCompiled(output, "data", "data-gone"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("not compiled: %v", err)
	}

	published, err := ReadPublished(output, "data")
	if err != nil || len(published) != 2 || published[0].Release != "data-db" || published[1].Release != "data-plain" || published[1].Service != "plain" || published[1].Info.URL != "" {
		t.Fatalf("published: %+v %v", published, err)
	}
	if published, err := ReadPublished(output, "none"); err != nil || published != nil {
		t.Errorf("a namespace of no instance: %+v %v", published, err)
	}
	output["projects/data/instances/data-db/okdp.info.yaml"] = &fstest.MapFile{Data: []byte("url: [\n")}
	if _, err := ReadPublished(output, "data"); err == nil || !strings.HasPrefix(err.Error(), "projects/data/instances/data-db/okdp.info.yaml: ") {
		t.Errorf("an info file that does not parse: %v", err)
	}
}
