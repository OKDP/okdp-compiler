package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/okdp/okdp-compiler/compiler"
)

// TestEmit: the final values of each chart, the index of the vendored
// chart rendering each, the objects and the dependencies.
func TestEmit(t *testing.T) {
	dir := t.TempDir()
	res := &compiler.Result{
		Values: map[string]map[string]any{"oidc-dcr": {"a": 1}, "console-oidc-dcr": {"b": 2}},
		Charts: map[string]string{"oidc-dcr": "oidc-dcr", "console-oidc-dcr": "oidc-dcr"},
	}
	if err := emit(dir, res); err != nil {
		t.Fatal(err)
	}
	for file, want := range map[string]string{
		"oidc-dcr.values.yaml":         "a: 1\n",
		"console-oidc-dcr.values.yaml": "b: 2\n",
		"okdp.charts.yaml":             "console-oidc-dcr: oidc-dcr\noidc-dcr: oidc-dcr\n",
		"okdp.objects.yaml":            "[]\n",
		"okdp.dependencies.yaml":       "[]\n",
	} {
		b, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != want {
			t.Errorf("%s: got %q, want %q", file, b, want)
		}
	}
}
