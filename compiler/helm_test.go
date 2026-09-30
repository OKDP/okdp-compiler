package compiler

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/google/cel-go/cel"
	"gopkg.in/yaml.v3"
	sigsyaml "sigs.k8s.io/yaml"
)

// Tests against what Helm computes: the fixtures of testdata/helm are
// written by `helm template` (testdata/helm/generate.sh, Helm 4).

// helmdata: testdata/helm, at the repository root.
var helmdata = filepath.Join("..", "testdata", "helm")

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
	cases, _ := filepath.Glob(filepath.Join(helmdata, "values", "*"))
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
	dir := filepath.Join(helmdata, "functions")
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

// readYAMLStream: the documents of a multi-document YAML stream (a `helm
// template` output), each read as Helm reads YAML.
func readYAMLStream(b []byte) ([]any, error) {
	docs, err := splitDocuments(b)
	if err != nil {
		return nil, err
	}
	var out []any
	for _, doc := range docs {
		var v any
		if err := sigsyaml.Unmarshal(doc, &v); err != nil {
			return nil, err
		}
		if v != nil {
			out = append(out, v)
		}
	}
	return out, nil
}

// diffPaths: the differences between two values, one line per path.
func diffPaths(a, b any, path string) []string {
	am, aok := a.(M)
	bm, bok := b.(M)
	if aok && bok {
		var out []string
		keys := map[string]bool{}
		for k := range am {
			keys[k] = true
		}
		for k := range bm {
			keys[k] = true
		}
		for _, k := range slices.Sorted(maps.Keys(keys)) {
			x, inA := am[k]
			y, inB := bm[k]
			p := path + "." + k
			switch {
			case !inA:
				out = append(out, fmt.Sprintf("missing %s (want %v)", p, short(y)))
			case !inB:
				out = append(out, fmt.Sprintf("extra   %s = %v", p, short(x)))
			default:
				out = append(out, diffPaths(x, y, p)...)
			}
		}
		return out
	}
	// Lists of the same length: item by item. Otherwise compared whole.
	al, aok := a.([]any)
	bl, bok := b.([]any)
	if aok && bok && len(al) == len(bl) {
		var out []string
		for i := range al {
			out = append(out, diffPaths(al[i], bl[i], fmt.Sprintf("%s[%d]", path, i))...)
		}
		return out
	}
	if fmt.Sprintf("%#v", a) != fmt.Sprintf("%#v", b) {
		return []string{fmt.Sprintf("changed %s: got %v, want %v", path, short(a), short(b))}
	}
	return nil
}

// withoutChartLabel: Helm's objects without their helm.sh/chart labels (of
// the objects, and of the Secret an ExternalSecret writes), which the
// compiler does not set: OKDP's objects belong to no chart of the service.
func withoutChartLabel(objects []any) []any {
	var drop func(v any)
	drop = func(v any) {
		switch t := v.(type) {
		case M:
			delete(t, "helm.sh/chart")
			for _, x := range t {
				drop(x)
			}
		case []any:
			for _, x := range t {
				drop(x)
			}
		}
	}
	drop(objects)
	return objects
}

// byKey: objects by kind/namespace/name (helm template sorts them by kind).
func byKey(t *testing.T, objects []any) map[string]any {
	t.Helper()
	out := map[string]any{}
	for _, o := range objects {
		m := asMap(o)
		k := str(m["kind"]) + "/" + str(dig(m, "metadata.namespace")) + "/" + str(dig(m, "metadata.name"))
		if _, dup := out[k]; dup {
			t.Errorf("two objects %s", k)
		}
		out[k] = m
	}
	return out
}
