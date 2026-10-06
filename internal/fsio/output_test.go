package fsio

import (
	"strings"
	"testing"

	"github.com/okdp/okdp-compiler/internal/chart"
	"github.com/okdp/okdp-compiler/internal/okdp"
)

// TestFiles: the final values of each chart, indented by 2 spaces, or as
// asked; a key that would name a file out of the directory is refused.
func TestFiles(t *testing.T) {
	res := &okdp.Result{
		Values: map[string]map[string]any{"oidc-dcr": {"a": map[string]any{"b": []any{1}}}, "console-oidc-dcr": {"b": 2}},
		Charts: map[string]chart.Ref{"oidc-dcr": okdp.DcrSource, "console-oidc-dcr": okdp.DcrSource},
	}
	files, err := Files(res, DefaultIndent)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"oidc-dcr.values.yaml":         "a:\n  b:\n    - 1\n",
		"console-oidc-dcr.values.yaml": "b: 2\n",
	}
	if len(files) != len(want) {
		t.Errorf("got %d files, want %d", len(files), len(want))
	}
	for name, w := range want {
		if got := string(files[name]); got != w {
			t.Errorf("%s: got %q, want %q", name, got, w)
		}
	}
	// --indent 4: every file.
	if files, err = Files(res, 4); err != nil || string(files["oidc-dcr.values.yaml"]) != "a:\n    b:\n        - 1\n" {
		t.Errorf("indent 4: %v %q", err, files["oidc-dcr.values.yaml"])
	}
	if b, err := Document(res, 4); err != nil || !strings.Contains(string(b), "\n    oidc-dcr:\n        repository:") {
		t.Errorf("indent 4, the document: %v %s", err, b)
	}
	if _, err := Files(res, 1); err == nil || err.Error() != "indent 1: 2 to 9 spaces" {
		t.Errorf("indent 1: got %v", err)
	}
	// A key of a Result built by a caller.
	bad := &okdp.Result{Values: map[string]map[string]any{"../app": {}}}
	if _, err := Files(bad, DefaultIndent); err == nil || !strings.HasPrefix(err.Error(), `chart "../app": a key of the result names a file`) {
		t.Errorf("../app: got %v", err)
	}
}

// TestDependencies: the dependencies as a YAML list, [] when there is none.
func TestDependencies(t *testing.T) {
	for name, tc := range map[string]struct {
		deps []okdp.Dependency
		want string
	}{
		"none": {nil, "[]\n"},
		"two": {[]okdp.Dependency{{Instance: "demo-db", Contract: "database", Field: "backends[0].database"}, {Instance: "demo-s3", Contract: "s3", Field: "storage"}},
			"- instance: demo-db\n  contract: database\n  field: backends[0].database\n- instance: demo-s3\n  contract: s3\n  field: storage\n"},
	} {
		if got, err := Dependencies(&okdp.Result{Dependencies: tc.deps}, DefaultIndent); err != nil || string(got) != tc.want {
			t.Errorf("%s: got %v %q, want %q", name, err, got, tc.want)
		}
	}
}
