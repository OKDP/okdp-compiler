package compile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/google/cel-go/cel"

	"github.com/okdp/okdp-compiler/internal/chart"
	"github.com/okdp/okdp-compiler/internal/value"
)

// Tests against what Helm computes: the fixtures of testdata/helm are
// written by `helm template` (testdata/helm/generate.sh, Helm 4).

// helmdata: testdata/helm, at the repository root.
var helmdata = filepath.Join("..", "..", "testdata", "helm")

// helmJSON: a JSON file written by generate.sh.
func helmJSON(t *testing.T, path string) any {
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

// TestHelmMergeKeys: a mapping reads merge keys (<<) as Helm reads a values
// file (chart.ParseValues, sigs.k8s.io/yaml).
func TestHelmMergeKeys(t *testing.T) {
	src := `a: &a {x: 1, y: {p: 1}}
c: &c {y: 3, z: 3}
alias: {<<: *a}
after: {<<: *a, x: 2}
list: {<<: [*a, *c], w: 1}
inline: {<<: {x: 4}}
nested: {<<: {<<: *c, q: 1}}
twice: {<<: *a, <<: *c}
empty: {<<: [], k: 1}
quoted: {"<<": *c}
tagged: {!!merge <<: *c}
`
	helm, err := chart.ParseValues([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	d, err := Load([]byte("output:\n  x:\n    "+strings.ReplaceAll(src, "\n", "\n    ")), M{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	out, err := d.Section("output")
	if err != nil {
		t.Fatal(err)
	}
	if got := value.AsMap(out)["x"]; !reflect.DeepEqual(got, helm) {
		t.Errorf("got  %#v\nhelm %#v", got, helm)
	}
}

// TestHelmFunctions: sha256, toJson, toPrettyJson, toYaml and quote called
// from CEL, against Helm's sha256sum (of toString), toJson, toPrettyJson,
// toYaml and quote on the same inputs (testdata/helm/functions).
func TestHelmFunctions(t *testing.T) {
	dir := filepath.Join(helmdata, "functions")
	b, err := os.ReadFile(filepath.Join(dir, "values.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	in, err := chart.ParseValues(b)
	if err != nil {
		t.Fatal(err)
	}
	helm := value.AsMap(helmJSON(t, filepath.Join(dir, "helm.json")))
	e := &celEngine{envs: map[string]*cel.Env{}}
	for name, x := range value.AsMap(in["inputs"]) {
		want := value.AsMap(value.AsMap(helm["functions"])[name])
		for _, f := range []string{"sha256", "toJson", "toPrettyJson", "toYaml", "quote"} {
			w, ok := want[f]
			if !ok {
				continue // sha256sum of a null: Helm's toString gives "<nil>"
			}
			got, err := e.eval(f+"(x)", M{"x": x})
			switch {
			// Helm prints "" when the encoder fails; the compile fails.
			case err != nil && w == "":
			case err != nil:
				t.Errorf("%s(%s): %v", f, name, err)
			case got != w:
				t.Errorf("%s(%s):\n got %q\nwant %q", f, name, got, w)
			}
		}
	}
	// What Helm cannot show: a null hashes as "", quotes as nothing.
	for expr, want := range map[string]string{"sha256(null)": sha256Hex(""), "quote(null)": "", "sha256(b'abc')": sha256Hex("abc")} {
		if got, err := e.eval(expr, M{}); err != nil || got != want {
			t.Errorf("%s: got %q (%v), want %q", expr, got, err, want)
		}
	}
}
