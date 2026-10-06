package fsio

import (
	"strings"
	"testing"

	"github.com/okdp/okdp-compiler/internal/chart"
	"github.com/okdp/okdp-compiler/internal/okdp"
)

// TestInstall: the script that installs each chart with the upstream chart
// rendering it, the OAuth clients first, a word the shell reads specially
// quoted; what it cannot install is refused.
func TestInstall(t *testing.T) {
	res := &okdp.Result{
		Values: map[string]map[string]any{"oidc-dcr": {}, "console-oidc-dcr": {}, "app": {}},
		Charts: map[string]chart.Ref{
			"oidc-dcr":         okdp.DcrSource,
			"console-oidc-dcr": okdp.DcrSource,
			"app":              {Repository: "https://charts.example.org/a?b&c", Chart: "app", Version: "1.0.0"},
		},
		Release:   "demo",
		Namespace: "ns",
	}
	want := `#!/bin/sh
# Installs demo in the namespace ns, one Helm release per chart: its
# upstream chart with its values file, written by okdp-compile.
set -eu
cd "$(dirname "$0")"
helm upgrade --install demo-console-oidc-dcr oci://quay.io/adaltas/oidc-dcr --version 0.5.0 --namespace ns -f console-oidc-dcr.values.yaml
helm upgrade --install demo-oidc-dcr oci://quay.io/adaltas/oidc-dcr --version 0.5.0 --namespace ns -f oidc-dcr.values.yaml
helm upgrade --install demo-app app --repo 'https://charts.example.org/a?b&c' --version 1.0.0 --namespace ns -f app.values.yaml
`
	if got, err := Install(res); err != nil || string(got) != want {
		t.Errorf("got %v %s, want %s", err, got, want)
	}
	for name, tc := range map[string]struct {
		res  okdp.Result
		want string
	}{
		"no release": {okdp.Result{Namespace: "ns"}, `Result.Release "", Result.Namespace "ns": the release and namespace of the compile`},
		"a bad key":  {okdp.Result{Values: map[string]map[string]any{"../app": {}}, Release: "demo", Namespace: "ns"}, `chart "../app": a key of the result names a file`},
		"no chart":   {okdp.Result{Values: map[string]map[string]any{"app": {}}, Release: "demo", Namespace: "ns"}, `chart "app": not in Result.Charts`},
		// Two keys, one release once cut to 53 characters.
		"one release": {okdp.Result{
			Values:  map[string]map[string]any{"app-1": {}, "app-2": {}},
			Charts:  map[string]chart.Ref{"app-1": {}, "app-2": {}},
			Release: strings.Repeat("r", 49), Namespace: "ns",
		}, `charts "app-1" and "app-2": one Helm release, "` + strings.Repeat("r", 49) + `-app", once <release>-<key> is cut to 53 characters`},
	} {
		if _, err := Install(&tc.res); err == nil || !strings.HasPrefix(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want %s", name, err, tc.want)
		}
	}
}
