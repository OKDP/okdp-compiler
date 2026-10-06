package compile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"cel.dev/cel-go/cel"
	"go.yaml.in/yaml/v3"

	"github.com/okdp/okdp-compiler/internal/chart"
	"github.com/okdp/okdp-compiler/internal/value"
)

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

// helmMergeKeys: values with merge keys, for TestHelmMergeKeys and
// FuzzMappingReader.
const helmMergeKeys = `a: &a {x: 1, y: {p: 1}}
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

// TestHelmMergeKeys: a mapping reads merge keys (<<) as Helm reads a values
// file (chart.ParseValues, sigs.k8s.io/yaml).
func TestHelmMergeKeys(t *testing.T) {
	helm, err := chart.ParseValues([]byte(helmMergeKeys))
	if err != nil {
		t.Fatal(err)
	}
	d, err := Load([]byte("output:\n  x:\n    "+strings.ReplaceAll(helmMergeKeys, "\n", "\n    ")), M{}, nil)
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

// readTime: the time the reader may take on a document of the fuzzer, a
// few hundred bytes: far more than the 100,000 nodes aliases expand to at
// most take.
const readTime = 2 * time.Second

// nonSpecific: YAML's non-specific tag, !, which yaml.v3 drops: ! 1 is the
// number to the reader, the text to Helm.
var nonSpecific = regexp.MustCompile(`!([\s,\]}]|$)`)

// FuzzMappingReader: the mapping reader (parseDoc) returns on any document,
// aliases and merge keys included, soon, and with an error rather than a
// huge document: what it reads holds a value per node of the document at
// most, and maxAliasNodes more, each one as values hold it
// (value.Normalize keeps it as it is); a document Helm reads too is the
// same values to both (but with a non-specific tag). Seeds: the documents
// of TestHelmMergeKeys, TestAliases, TestMergeKeys and TestMappingYAML11.
func FuzzMappingReader(f *testing.F) {
	for _, src := range []string{helmMergeKeys, aliasesDoc, mergeKeysDoc, yaml11Doc} {
		f.Add([]byte(src))
	}
	for _, tc := range aliasErrors {
		f.Add([]byte(tc.src))
	}
	for _, tc := range mergeKeyErrors {
		f.Add([]byte(tc.src))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		start := time.Now()
		top, err := parseDoc(b)
		if d := time.Since(start); d > readTime {
			t.Errorf("%q: read in %v", b, d)
		}
		if err != nil {
			return
		}
		var doc yaml.Node
		if err := yaml.Unmarshal(helmLines(b), &doc); err != nil {
			t.Fatal(err)
		}
		v := plain(top)
		if n, most := size(v), nodes(&doc)+maxAliasNodes; n > most {
			t.Fatalf("%q: %d values, for %d at most", b, n, most)
		}
		if n, err := value.Normalize(v); err != nil || !reflect.DeepEqual(n, v) {
			t.Fatalf("%q: read as %#v, which values do not hold (%v)", b, v, err)
		}
		if helm, err := chart.ParseValues(b); err == nil && !nonSpecific.Match(b) && !reflect.DeepEqual(v, any(helm)) {
			t.Errorf("%q: read as %#v, by Helm as %#v", b, v, helm)
		}
	})
}

// plain: a value parseDoc read, its maps as values maps.
func plain(v any) any {
	switch t := v.(type) {
	case *omap:
		m := M{}
		for _, k := range t.keys {
			m[k] = plain(t.vals[k])
		}
		return m
	case []any:
		l := make([]any, len(t))
		for i, x := range t {
			l[i] = plain(x)
		}
		return l
	}
	return v
}

// size: the values v holds, itself included.
func size(v any) int {
	n := 1
	switch t := v.(type) {
	case M:
		for _, x := range t {
			n += size(x)
		}
	case []any:
		for _, x := range t {
			n += size(x)
		}
	}
	return n
}

// nodes: the nodes of a YAML document but its aliases.
func nodes(n *yaml.Node) int {
	if n.Kind == yaml.AliasNode {
		return 0
	}
	c := 1
	for _, x := range n.Content {
		c += nodes(x)
	}
	return c
}

// TestHelmFunctions: sha256, toJson, toPrettyJson, toYaml and quote called
// from CEL, against Helm's sha256sum (of toString), toJson, toPrettyJson,
// toYaml and quote on the same inputs (testdata/helm/functions: helm.json,
// and helm<major>.json where a deployer's major version renders
// differently).
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
	e := &celEngine{envs: map[string]*cel.Env{}}
	helmJSON(t, filepath.Join(dir, "helm.json")) // there, at least
	renders, _ := filepath.Glob(filepath.Join(dir, "helm*.json"))
	for _, path := range renders {
		helm := value.AsMap(helmJSON(t, path))
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
					t.Errorf("%s: %s(%s): %v", filepath.Base(path), f, name, err)
				case got != w:
					t.Errorf("%s: %s(%s):\n got %q\nwant %q", filepath.Base(path), f, name, got, w)
				}
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
