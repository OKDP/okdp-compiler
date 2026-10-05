package chart

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/okdp/okdp-compiler/internal/value"
)

var update = flag.Bool("update", false, "rewrite the final.yaml of testdata/helm/values from the current compiler")

// Tests against what Helm computes: the fixtures of testdata/helm are
// written by `helm template` (testdata/helm/generate.sh: Helm 4 on the PATH,
// and the deployers' versions where they render differently; the versions
// in testdata/helm/SOURCE).

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

// helmMajors: the major versions of the deployers' Helm versions that
// testdata/helm/SOURCE records (helms), each with its versions: the
// versions generate.sh renders a helm<major> file with, where they render
// differently from Helm 4 on the PATH.
func helmMajors(t *testing.T) map[string][]string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(helmdata, "SOURCE"))
	if err != nil {
		t.Fatalf("%v (run testdata/helm/generate.sh)", err)
	}
	majors := map[string][]string{}
	for line := range strings.Lines(string(b)) {
		if f := strings.Fields(line); len(f) > 0 && f[0] == "helms" {
			for _, v := range f[1:] {
				m, _, _ := strings.Cut(v, ".")
				majors[m] = append(majors[m], v)
			}
		}
	}
	return majors
}

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

// helmVariant: the render of a deployer's major version in the case
// directory dir, beside the reference file name (helm.json: helm3.json),
// nil when it renders as the reference.
func helmVariant(t *testing.T, dir, name, major string) any {
	t.Helper()
	path := filepath.Join(dir, strings.Replace(name, "helm", "helm"+major, 1))
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	return helmJSON(t, path)
}

// TestHelmValues: for each testdata/helm/values/<case>, the chart defaults
// (values.yaml) and the -f layers (layer-*.yaml) read and coalesced as Helm
// does (helm.json); and the final values the compiler writes for these
// layers (final.yaml, rewritten by -update), which Helm renders the same
// (helm-final.json). Each deployer's major version renders them as it
// renders the layers (helm<major>.json, or helm.json), but for the known
// differences (helmFinalKnown).
func TestHelmValues(t *testing.T) {
	cases, _ := filepath.Glob(filepath.Join(helmdata, "values", "*"))
	if len(cases) == 0 {
		t.Fatal("no cases")
	}
	var majors map[string][]string
	if !*update {
		majors = helmMajors(t)
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
			want := helmJSON(t, filepath.Join(dir, "helm.json"))
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
			if got := helmJSON(t, filepath.Join(dir, "helm-final.json")); !reflect.DeepEqual(got, want) {
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
				helmLayers := helmVariant(t, dir, "helm.json", major)
				if helmLayers == nil {
					helmLayers = want
				}
				got := helmVariant(t, dir, "helm-final.json", major)
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
					expect := value.AsMap(helmJSON(t, filepath.Join(dir, "helm-final.json")))
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
	cases, _ := filepath.Glob(filepath.Join(helmdata, "schema", "*"))
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
			v, err := schemaViolations(fsys, "values.schema.json", coalesceValues(defaults, user))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(dir, "helm.txt")); err != nil {
				t.Fatalf("%v (run testdata/helm/generate.sh)", err)
			}
			// helm.txt, and helm<major>.txt where a deployer's major version
			// reports differently: each is the compiler's report.
			reports, _ := filepath.Glob(filepath.Join(dir, "helm*.txt"))
			for _, path := range reports {
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
