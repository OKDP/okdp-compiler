package compile

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/okdp/okdp-compiler/internal/chart"
	"github.com/okdp/okdp-compiler/internal/helmtest"
	"github.com/okdp/okdp-compiler/internal/value"
)

// Tests against what Helm computes: the fixtures of testdata/helm are
// written by `helm template` (testdata/helm/generate.sh: Helm 4 on the PATH,
// and the deployers' versions where they render differently; the versions
// in testdata/helm/SOURCE).

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
	for expr, want := range map[string]string{"sha256(null)": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", "quote(null)": ""} {
		if got, err := s.eval(expr, M{}); err != nil || got != want {
			t.Errorf("%s: got %q (%v), want %q", expr, got, err, want)
		}
	}
}
