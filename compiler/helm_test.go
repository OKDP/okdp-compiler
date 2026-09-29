package compiler

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/google/cel-go/cel"
	"gopkg.in/yaml.v3"
)

// Tests against what Helm computes: the fixtures of testdata/helm are
// written by `helm template` (testdata/helm/generate.sh, Helm 4).

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

// TestHelmValues: for each testdata/helm/values/<case>, the chart defaults
// (values.yaml) and the -f layers (layer-*.yaml) read and coalesced as Helm
// does (helm.json); and the final values the compiler writes for these
// layers (final.yaml, rewritten by -update), which Helm renders the same
// (helm-final.json).
func TestHelmValues(t *testing.T) {
	cases, _ := filepath.Glob(filepath.Join("testdata", "helm", "values", "*"))
	if len(cases) == 0 {
		t.Fatal("no cases")
	}
	for _, dir := range cases {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			defaults, err := readValues(os.DirFS(dir), "values.yaml")
			if err != nil {
				t.Fatal(err)
			}
			files, _ := filepath.Glob(filepath.Join(dir, "layer-*.yaml"))
			var layers []M
			user := M{}
			for _, f := range files {
				b, err := os.ReadFile(f)
				if err != nil {
					t.Fatal(err)
				}
				l, err := ParseValues(b)
				if err != nil {
					t.Fatalf("%s: %v", f, err)
				}
				layers = append(layers, l)
				user = mergeMaps(user, l)
			}
			// The compiler's final values for these layers, rendered by Helm.
			final := layered(defaults, layers...)
			golden := filepath.Join(dir, "final.yaml")
			if *update {
				b, err := yaml.Marshal(final)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(golden, b, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			// What the chart's templates see.
			want := helmJSON(t, filepath.Join(dir, "helm.json"))
			if got := coalesceValues(defaults, user); !reflect.DeepEqual(any(got), want) {
				t.Errorf("coalesced values:\n got %#v\nwant %#v", got, want)
			}
			b, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%v (run go test ./compiler -run TestHelmValues -update)", err)
			}
			if g, err := ParseValues(b); err != nil || !reflect.DeepEqual(g, final) {
				t.Errorf("%s differs from the final values (%v): run go test ./compiler -run TestHelmValues -update, then generate.sh", golden, err)
			}
			if got := helmJSON(t, filepath.Join(dir, "helm-final.json")); !reflect.DeepEqual(got, want) {
				t.Errorf("Helm renders the final values differently:\n got %#v\nwant %#v", got, want)
			}
			// Coalesced again (as Helm does when it renders them), the final
			// values give what the templates see.
			if got := coalesceValues(defaults, final); !reflect.DeepEqual(any(got), want) {
				t.Errorf("final values coalesced:\n got %#v\nwant %#v", got, want)
			}
		})
	}
}

// TestHelmSchema: for each testdata/helm/schema/<case>, the violations of
// values.schema.json by the coalesced values, as Helm reports them
// (helm.txt; Helm's order varies, the lines are compared sorted).
func TestHelmSchema(t *testing.T) {
	cases, _ := filepath.Glob(filepath.Join("testdata", "helm", "schema", "*"))
	if len(cases) == 0 {
		t.Fatal("no cases")
	}
	for _, dir := range cases {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			fsys := os.DirFS(dir)
			defaults, err := readValues(fsys, "values.yaml")
			if err != nil {
				t.Fatal(err)
			}
			files, _ := filepath.Glob(filepath.Join(dir, "layer-*.yaml"))
			user := M{}
			for _, f := range files {
				b, err := os.ReadFile(f)
				if err != nil {
					t.Fatal(err)
				}
				l, err := ParseValues(b)
				if err != nil {
					t.Fatalf("%s: %v", f, err)
				}
				user = mergeMaps(user, l)
			}
			v, err := schemaViolations(fsys, "values.schema.json", coalesceValues(defaults, user))
			if err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(filepath.Join(dir, "helm.txt"))
			if err != nil {
				t.Fatalf("%v (run testdata/helm/generate.sh)", err)
			}
			helm := strings.TrimSpace(string(b))
			if helm == "ok" {
				if v != "" {
					t.Errorf("Helm accepts the values, got:\n%s", v)
				}
				return
			}
			// Helm: "Error: <message>", "<chart>:", then the violations.
			lines := strings.Split(helm, "\n")
			got := schemaError("values", map[string]string{"schema": v})
			if got == nil {
				t.Fatalf("no violation, Helm reports:\n%s", helm)
			}
			if want := strings.TrimPrefix(lines[0], "Error: ") + "\n" + lines[1]; !strings.HasPrefix(got.Error(), want) {
				t.Errorf("got %q, want it to start with %q", got.Error(), want)
			}
			want := slices.Sorted(slices.Values(lines[2:]))
			if g := slices.Sorted(slices.Values(strings.Split(v, "\n"))); !slices.Equal(g, want) {
				t.Errorf("got\n%s\nwant (sorted)\n%s", strings.Join(g, "\n"), strings.Join(want, "\n"))
			}
		})
	}
}

// TestHelmFunctions: sha256, toJson, toPrettyJson, toYaml, quote and
// okdpFullname called from CEL, against Helm's sha256sum (of toString),
// toJson, toPrettyJson, toYaml, quote and okdp.fullname on the same inputs
// (testdata/helm/functions).
func TestHelmFunctions(t *testing.T) {
	dir := filepath.Join("testdata", "helm", "functions")
	b, err := os.ReadFile(filepath.Join(dir, "values.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	in, err := ParseValues(b)
	if err != nil {
		t.Fatal(err)
	}
	helm := asMap(helmJSON(t, filepath.Join(dir, "helm.json")))
	e := &celEngine{envs: map[string]*cel.Env{}, ctx: M{"release": M{"name": helm["release"]}}}
	for name, x := range asMap(in["inputs"]) {
		want := asMap(asMap(helm["functions"])[name])
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
	for suffix, w := range asMap(helm["fullname"]) {
		got, err := e.eval("okdpFullname(s)", M{"s": suffix})
		if err != nil || got != w {
			t.Errorf("okdpFullname(%q): got %q (%v), want %q", suffix, got, err, w)
		}
	}
	if got, err := e.eval("okdpFullname()", M{}); err != nil || got != helm["release"] {
		t.Errorf("okdpFullname(): got %q (%v)", got, err)
	}
	// What Helm cannot show: a null hashes as "", quotes as nothing.
	for expr, want := range map[string]string{"sha256(null)": sha256Hex(""), "quote(null)": "", "sha256(b'abc')": sha256Hex("abc")} {
		if got, err := e.eval(expr, M{}); err != nil || got != want {
			t.Errorf("%s: got %q (%v), want %q", expr, got, err, want)
		}
	}
}

// TestHelmLabels: the chart metadata, labels and selectorLabels of the
// mapping context and okdpLabels / okdpSelectorLabels, against Helm's
// .Chart, okdp.labels and okdp.selectorLabels (testdata/helm/labels).
func TestHelmLabels(t *testing.T) {
	cases, _ := filepath.Glob(filepath.Join("testdata", "helm", "labels", "*"))
	if len(cases) == 0 {
		t.Fatal("no cases")
	}
	for _, dir := range cases {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			chart, err := readChart(os.DirFS(dir))
			if err != nil {
				t.Fatal(err)
			}
			helm := asMap(helmJSON(t, filepath.Join(dir, "helm.json")))
			hc := asMap(helm["chart"])
			if want := (chartMeta{Name: str(hc["Name"]), Version: str(hc["Version"]), AppVersion: str(hc["AppVersion"])}); chart != want {
				t.Errorf("chart: got %#v, want %#v", chart, want)
			}
			release := str(helm["release"])
			ctx := chartContext(chart, release)
			ctx["release"] = M{"name": release}
			e := &celEngine{envs: map[string]*cel.Env{}, ctx: ctx}
			want := asMap(helm["out"])
			for name, expr := range map[string]string{
				"labels":                  "labels",
				"labelsComponent":         "okdpLabels('hook')",
				"selectorLabels":          "selectorLabels",
				"selectorLabelsComponent": "okdpSelectorLabels('hook')",
			} {
				got, err := e.eval(expr, ctx)
				if err != nil {
					t.Fatalf("%s: %v", expr, err)
				}
				if !reflect.DeepEqual(got, want[name]) {
					t.Errorf("%s:\n got %#v\nwant %#v", expr, got, want[name])
				}
			}
		})
	}
}
