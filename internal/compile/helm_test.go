package compile

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"testing"

	"github.com/okdp/okdp-compiler/internal/chart"
	"github.com/okdp/okdp-compiler/internal/helmfn"
	"github.com/okdp/okdp-compiler/internal/helmtest"
	"github.com/okdp/okdp-compiler/internal/value"
)

// Tests against what Helm computes: the fixtures of testdata/helm are
// written by `helm template` (testdata/helm/generate.sh: Helm 4 on the PATH,
// and the deployers' versions where they render differently; the versions
// in testdata/helm/SOURCE).

// nonSpecific: YAML's non-specific tag, !, which yaml.v3 drops: ! 1 is the
// number to the reader, the text to Helm.
var nonSpecific = regexp.MustCompile(`!([\s,\]}]|$)`)

// FuzzMappingReader: the mapping reader (parseDoc) returns on any document,
// with values as values hold them (value.Normalize keeps them as they are);
// a document Helm reads too is the same values to both (but with a
// non-specific tag, or a "\r\r\n", two line breaks to the reader, one to
// Helm). Seeds: the documents of TestMappingYAML11.
func FuzzMappingReader(f *testing.F) {
	f.Add([]byte(yaml11Doc))
	for src := range readerErrors {
		f.Add([]byte(src))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		top, err := parseDoc(b)
		if err != nil {
			return
		}
		v := plain(top)
		if n, err := value.Normalize(v); err != nil || !reflect.DeepEqual(n, v) {
			t.Fatalf("%q: read as %#v, which values do not hold (%v)", b, v, err)
		}
		if helm, err := chart.ParseValues(b); err == nil && !nonSpecific.Match(b) && !bytes.Contains(b, []byte("\r\r\n")) && !reflect.DeepEqual(v, any(helm)) {
			t.Errorf("%q: read as %#v, by Helm as %#v", b, v, helm)
		}
	})
}

// plain: a value parseDoc read, its maps as values maps.
func plain(v any) any {
	switch t := v.(type) {
	case *omap:
		m := M{}
		for _, f := range t.fields() {
			m[f.key] = plain(f.val)
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

// TestHelmFunctions: sha256, toJson, toPrettyJson, toYaml and quote called
// from CEL, against Helm's sha256sum (of toString), toJson, toPrettyJson,
// toYaml and quote on the same inputs (testdata/helm/functions: helm.json,
// and helm<major>.json where a deployer's major version renders
// differently).
func TestHelmFunctions(t *testing.T) {
	dir := filepath.Join(helmtest.Dir, "functions")
	b, err := os.ReadFile(filepath.Join(dir, "values.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	in, err := chart.ParseValues(b)
	if err != nil {
		t.Fatal(err)
	}
	s := NewSession(context.Background(), 0)
	for _, path := range helmtest.Renders(t, dir, "helm.json") {
		helm := value.AsMap(helmtest.JSON(t, path))
		for name, x := range value.AsMap(in["inputs"]) {
			want := value.AsMap(value.AsMap(helm["functions"])[name])
			for _, f := range []string{"sha256", "toJson", "toPrettyJson", "toYaml", "quote"} {
				w, ok := want[f]
				if !ok {
					continue // sha256sum of a null: Helm's toString gives "<nil>"
				}
				got, err := s.eval(f+"(x)", M{"x": x})
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
	for expr, want := range map[string]string{"sha256(null)": helmfn.Sha256Sum(""), "quote(null)": ""} {
		if got, err := s.eval(expr, M{}); err != nil || got != want {
			t.Errorf("%s: got %q (%v), want %q", expr, got, err, want)
		}
	}
}
