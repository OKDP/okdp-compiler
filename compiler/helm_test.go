package compiler

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

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
