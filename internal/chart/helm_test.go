package chart

import (
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/okdp/okdp-compiler/internal/helmtest"
	"github.com/okdp/okdp-compiler/internal/value"
)

var update = flag.Bool("update", false, "rewrite the final.yaml of testdata/helm/values from the current compiler")

// Tests against what Helm computes: the fixtures of testdata/helm are
// written by `helm template` (testdata/helm/generate.sh: Helm 4 on the PATH,
// and the deployers' versions where they render differently; the versions
// in testdata/helm/SOURCE).

// helmFinalKnown: by Helm major version, the values cases whose final
// values that version renders differently from their layers
// (helm<major>-final.json), and what it renders instead of what Helm 4
// renders (helm-final.json): by top-level key, the value.
var helmFinalKnown = map[string]map[string]M{
	// Helm 3 reads the first document of a values file only: the a of the
	// first document of documents/values.yaml, which a later document
	// deletes, is back.
	"3": {"documents": {"a": 1.0}},
}

// TestHelmValues: for each testdata/helm/values/<case>, the chart defaults
// (values.yaml) and the -f layers (layer-*.yaml) read and coalesced as Helm
// does (helm.json); and the final values the compiler writes for these
// layers (final.yaml, rewritten by -update), which Helm renders the same
// (helm-final.json). Each deployer's major version renders them as it
// renders the layers (helm<major>.json, or helm.json), but for the known
// differences (helmFinalKnown).
func TestHelmValues(t *testing.T) {
	cases, _ := filepath.Glob(filepath.Join(helmtest.Dir, "values", "*"))
	if len(cases) == 0 {
		t.Fatal("no cases")
	}
	var majors map[string][]string
	if !*update {
		majors = helmtest.Majors(t)
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
				user = value.Merge(user, l)
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
			want := helmtest.JSON(t, filepath.Join(dir, "helm.json"))
			if got := coalesceValues(defaults, user); !reflect.DeepEqual(any(got), want) {
				t.Errorf("coalesced values:\n got %#v\nwant %#v", got, want)
			}
			b, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%v (run go test ./internal/chart -run TestHelmValues -update)", err)
			}
			if g, err := ParseValues(b); err != nil || !reflect.DeepEqual(g, final) {
				t.Errorf("%s differs from the final values (%v): run go test ./internal/chart -run TestHelmValues -update, then generate.sh", golden, err)
			}
			if got := helmtest.JSON(t, filepath.Join(dir, "helm-final.json")); !reflect.DeepEqual(got, want) {
				t.Errorf("Helm renders the final values differently:\n got %#v\nwant %#v", got, want)
			}
			// Coalesced again (as Helm does when it renders them), the final
			// values give what the templates see.
			if got := coalesceValues(defaults, final); !reflect.DeepEqual(any(got), want) {
				t.Errorf("final values coalesced:\n got %#v\nwant %#v", got, want)
			}
			// The renders of the deployers' Helm versions: helm<major>.json
			// where a major version renders the layers differently from
			// helm.json, helm<major>-final.json where it renders the final
			// values differently from the layers, only for a known
			// difference.
			variants, _ := filepath.Glob(filepath.Join(dir, "helm[0-9]*.json"))
			for _, path := range variants {
				rest := strings.TrimPrefix(filepath.Base(path), "helm")
				major := rest[:len(rest)-len(strings.TrimLeft(rest, "0123456789"))]
				if majors[major] == nil {
					t.Errorf("%s: a render of Helm %s, which SOURCE does not list (run generate.sh)", path, major)
				}
			}
			for major, versions := range majors {
				helmLayers := helmtest.Variant(t, dir, "helm.json", major)
				if helmLayers == nil {
					helmLayers = want
				}
				got := helmtest.Variant(t, dir, "helm-final.json", major)
				known, isKnown := helmFinalKnown[major][filepath.Base(dir)]
				switch {
				case got == nil && isKnown:
					t.Errorf("Helm %s renders the final values as the layers: drop the known difference (helmFinalKnown)", strings.Join(versions, ", "))
				case got == nil:
				case reflect.DeepEqual(got, helmLayers):
					t.Errorf("helm%s-final.json: the layers' render (run generate.sh)", major)
				case !isKnown:
					t.Errorf("Helm %s renders the final values differently from the layers:\n got %#v\nwant %#v", strings.Join(versions, ", "), got, helmLayers)
				default:
					// What Helm 4 renders, but for the known keys.
					expect := value.AsMap(helmtest.JSON(t, filepath.Join(dir, "helm-final.json")))
					for k, x := range known {
						if reflect.DeepEqual(expect[k], x) {
							t.Errorf("Helm %s: %s is %#v with Helm 4 too: not a difference", strings.Join(versions, ", "), k, x)
						}
						expect[k] = x
					}
					if !reflect.DeepEqual(got, any(expect)) {
						t.Errorf("Helm %s renders the final values:\n got %#v\nwant %#v (Helm 4's, but for the known difference)", strings.Join(versions, ", "), got, expect)
					}
				}
			}
		})
	}
}

// TestHelmSchema: for each testdata/helm/schema/<case>, the violations of
// values.schema.json by the coalesced values, as Helm reports them
// (helm.txt; Helm's order varies, the lines are compared sorted), with each
// deployer's version.
func TestHelmSchema(t *testing.T) {
	cases, _ := filepath.Glob(filepath.Join(helmtest.Dir, "schema", "*"))
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
				user = value.Merge(user, l)
			}
			schema, err := os.ReadFile(filepath.Join(dir, "values.schema.json"))
			if err != nil {
				t.Fatal(err)
			}
			v, err := checkSchema("values.schema.json", schema, coalesceValues(defaults, user))
			if err != nil {
				t.Fatal(err)
			}
			// helm.txt, and helm<major>.txt where a deployer's major version
			// reports differently: each is the compiler's report.
			for _, path := range helmtest.Renders(t, dir, "helm.txt") {
				name := filepath.Base(path)
				b, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				helm := strings.TrimSpace(string(b))
				if helm == "ok" {
					if v != "" {
						t.Errorf("%s: Helm accepts the values, got:\n%s", name, v)
					}
					continue
				}
				// Helm: "Error: <message>", "<chart>:", then the violations.
				lines := strings.Split(helm, "\n")
				got := schemaError("values", map[string]string{"schema": v})
				if got == nil {
					t.Fatalf("%s: no violation, Helm reports:\n%s", name, helm)
				}
				if want := strings.TrimPrefix(lines[0], "Error: ") + "\n" + lines[1]; !strings.HasPrefix(got.Error(), want) {
					t.Errorf("%s: got %q, want it to start with %q", name, got.Error(), want)
				}
				want := slices.Sorted(slices.Values(lines[2:]))
				if g := slices.Sorted(slices.Values(strings.Split(v, "\n"))); !slices.Equal(g, want) {
					t.Errorf("%s: got\n%s\nwant (sorted)\n%s", name, strings.Join(g, "\n"), strings.Join(want, "\n"))
				}
			}
		})
	}
}
