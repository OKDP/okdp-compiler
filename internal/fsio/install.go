// Install: a compile as a script that installs it with Helm, one release
// per chart, or one release of the umbrella chart.
package fsio

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/okdp/okdp-compiler/internal/helmfn"
	"github.com/okdp/okdp-compiler/internal/okdp"
)

// InstallScript: the script that installs the files Write gives beside it
// (Output.Install), to write executable.
const InstallScript = "install.sh"

// install: InstallScript beside the files of valuesFiles, a POSIX shell
// script, marked generated (its line after #!), that installs each chart
// of res in its namespace, `helm upgrade --install`, from its upstream
// chart (Result.Charts) with its values file, <key>.values.yaml, in the
// script's directory, wherever it is run from. Each chart is its own
// release, <release>-<key> cut to 53 characters without a trailing "-": a
// namespace holds one release of a name. The charts of the OAuth clients
// (Result.ClientCharts) come first, their Job writing the Secret the others
// read, then the others by key: every chart by key for a Result without
// them.
func install(res *okdp.Result) ([]byte, error) {
	if !helmfn.IsRelease(res.Release) || !helmfn.IsDNSLabel(res.Namespace) {
		return nil, fmt.Errorf("Result.Release %q, Result.Namespace %q: the release and namespace of the compile (Inputs.Release, Inputs.Namespace), where %s installs the charts", res.Release, res.Namespace, InstallScript)
	}
	var clients, others []string
	for _, key := range slices.Sorted(maps.Keys(res.Values)) {
		if slices.Contains(res.ClientCharts, key) {
			clients = append(clients, key)
		} else {
			others = append(others, key)
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "#!/bin/sh\n%s# Installs %s in the namespace %s, one Helm release per chart: its\n# upstream chart with its values file. Recompile the instance to change it.\nset -eu\ncd \"$(dirname \"$0\")\"\n", generated, res.Release, res.Namespace)
	releases := map[string]string{} // release -> key
	for _, key := range append(clients, others...) {
		release := helmfn.TruncName(res.Release+"-"+key, helmfn.ReleaseMax)
		if other, ok := releases[release]; ok {
			return nil, fmt.Errorf("charts %q and %q: one Helm release, %q, once <release>-<key> is cut to %d characters: shorten the release", other, key, release, helmfn.ReleaseMax)
		}
		releases[release] = key
		args := append([]string{"helm", "upgrade", "--install", release}, chartArgs(res.Charts[key])...)
		args = append(args, "--namespace", res.Namespace, "-f", key+".values.yaml")
		for i, a := range args {
			args[i] = shellWord(a)
		}
		b.WriteString(strings.Join(args, " "))
		b.WriteByte('\n')
	}
	return []byte(b.String()), nil
}

// installUmbrella: InstallScript beside the files of umbrella, a POSIX
// shell script, marked generated (its line after #!), that installs res as
// one Helm release, Result.Release in Result.Namespace: the umbrella chart
// of the script's directory, wherever it is run from, its dependencies
// built in a temporary copy (helm dependency build adds Chart.lock and
// charts/, which the next compile into the directory would refuse), with
// UmbrellaValues. The OAuth clients' charts need no order: their Job is a
// pre-install hook.
func installUmbrella(res *okdp.Result) ([]byte, error) {
	if !helmfn.IsRelease(res.Release) || !helmfn.IsDNSLabel(res.Namespace) {
		return nil, fmt.Errorf("Result.Release %q, Result.Namespace %q: the release and namespace of the compile (Inputs.Release, Inputs.Namespace), where %s installs the chart", res.Release, res.Namespace, InstallScript)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "#!/bin/sh\n%s# Installs %s in the namespace %s, one Helm release: the\n# umbrella chart of this directory, its dependencies built in a copy, with\n# its values. Recompile the instance to change it.\nset -eu\ncd \"$(dirname \"$0\")\"\n", generated, res.Release, res.Namespace)
	b.WriteString("chart=$(mktemp -d)\ntrap 'rm -rf \"$chart\"' EXIT\ncp Chart.yaml \"$chart\"\nhelm dependency build \"$chart\"\n")
	fmt.Fprintf(&b, "helm upgrade --install %s \"$chart\" --namespace %s -f %s\n", shellWord(res.Release), shellWord(res.Namespace), UmbrellaValues)
	return []byte(b.String()), nil
}

// plainWord: a word no POSIX shell reads specially.
var plainWord = regexp.MustCompile(`^[A-Za-z0-9@%+=:,./_-]+$`)

// shellWord: s as one word of a shell command, single-quoted unless plain.
func shellWord(s string) string {
	if plainWord.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
