// Output: what a compile is written as, file name -> content; the caller
// writes them (the CLI to a directory, a server to git).
package fsio

import (
	"bytes"
	"fmt"
	"maps"
	"regexp"
	"slices"

	"gopkg.in/yaml.v3"

	"github.com/okdp/okdp-compiler/internal/okdp"
)

// DefaultIndent: the indentation of the YAML files, in spaces.
const DefaultIndent = 2

// Document: one YAML document, keys sorted: charts (the chart index,
// Result.Charts), dependencies, objects and values (Result.Computed: what
// the mapping computed, before the merges); indented by indent spaces (2
// to 9).
func Document(res *okdp.Result, indent int) ([]byte, error) {
	return marshal(map[string]any{"values": res.Computed, "charts": res.Charts, "objects": res.Objects, "dependencies": res.Dependencies}, indent)
}

// Files: <chart>.values.yaml, the final values of each chart, OKDP's own
// objects among them (okdp.values.yaml, the values of the okdp chart);
// okdp.charts.yaml, the index of the upstream chart that renders each
// (chart: {repository, chart, version}: `helm template <chart> --repo
// <repository> --version <version> -f <chart>.values.yaml`); and
// okdp.dependencies.yaml. Each is indented by indent spaces (2 to 9).
func Files(res *okdp.Result, indent int) (map[string][]byte, error) {
	files := map[string][]byte{}
	for _, chart := range slices.Sorted(maps.Keys(res.Values)) {
		if err := checkKey(chart); err != nil {
			return nil, err
		}
		b, err := marshal(res.Values[chart], indent)
		if err != nil {
			return nil, err
		}
		files[chart+".values.yaml"] = b
	}
	for name, v := range map[string]any{"okdp.charts.yaml": res.Charts, "okdp.dependencies.yaml": res.Dependencies} {
		b, err := marshal(v, indent)
		if err != nil {
			return nil, err
		}
		files[name] = b
	}
	return files, nil
}

// resultKey: a chart key as the outputs name it: a file
// (<key>.values.yaml) and an umbrella dependency alias, letters, digits, -
// and _ (Helm's rule for an alias). The keys of a compile follow it (DNS
// labels, chart.Specs.Check, and the compiler's): this refuses those of a
// Result built by a caller.
var resultKey = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// checkKey: an error unless the outputs can name the chart key key
// (resultKey).
func checkKey(key string) error {
	if !resultKey.MatchString(key) {
		return fmt.Errorf("chart %q: a key of the result names a file (<key>.values.yaml) and an umbrella dependency alias: letters, digits, - and _", key)
	}
	return nil
}

// marshal: v as a YAML document indented by indent spaces, 2 to 9 (what
// yaml.v3 writes; it would take any other value as 2).
func marshal(v any, indent int) ([]byte, error) {
	if indent < 2 || indent > 9 {
		return nil, fmt.Errorf("indent %d: 2 to 9 spaces", indent)
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(indent)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
