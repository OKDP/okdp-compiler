// Package helm writes a compile as the files of okdp-compile (Write):
// one chart for the whole instance, an umbrella over the upstream charts of
// its keys and the chart of OKDP's own objects, for one Helm release per instance under
// the release name its values were compiled for, and what the instance
// publishes. The caller writes them (the CLI to a directory, a server to
// git).
package helm

import (
	"bytes"
	"maps"
	"slices"

	"go.yaml.in/yaml/v3"

	"github.com/okdp/okdp-compiler/chart"
	"github.com/okdp/okdp-compiler/internal/okdp"
	"github.com/okdp/okdp-compiler/internal/target"
)

// The files Write gives besides Chart.yaml.
const (
	// ValuesFile: the umbrella's values file, given to Helm as user values
	// (helm -f, Argo CD valueFiles, Flux valuesFrom). As the chart's own
	// values.yaml they would be defaults, and Helm drops the nulls of
	// defaults: a null that deletes an upstream default would be lost.
	ValuesFile = "instance.values.yaml"
	// InfoFile: Result.Info, what the instance publishes: url,
	// usage, and provides, the connections it provides, each key written
	// when set, what a control plane or a console reads of it in the
	// deployments repository. Written when it publishes something. Helm
	// ignores it.
	InfoFile = "okdp.info.yaml"
)

// Write: what `okdp-compile -o <dir>` writes for res, path -> content:
//
//   - Chart.yaml, the chart of the instance: the package's Chart.yaml
//     (Result.Chart), its fields kept, with one dependency per compiled
//     key: first those of Chart.yaml, in its order, each as written; then
//     the compiler's own charts, the OAuth clients' and, when res has
//     objects, the okdp-objects chart (Result.ObjectsSource), by key, each
//     aliased to its key when the key is not the chart's name (Helm then
//     names the subchart after the key: its .Chart.Name is the key);
//   - ValuesFile, the final values of each key under the key, each with its
//     own global, marked generated (target.Generated), and, when res has
//     objects, Result.Objects as one YAML stream, one document per object,
//     under okdp.ObjectsChart, okdp.ObjectsValue: a string, so that Helm
//     changes neither the {{ of an ExternalSecret's template, nor a null,
//     nor an integer beyond the precision of a float64;
//   - InfoFile, when the instance publishes something: a URL, a usage
//     or a connection.
//
// The YAML files are indented by 2 spaces. Every file is built before
// Write returns: one that fails leaves the caller nothing to write. res is
// what Compile returned: Write does not check it again.
func Write(res *okdp.Result) (map[string][]byte, error) {
	values, charts := maps.Collect(maps.All(res.Values)), maps.Collect(maps.All(res.Charts))
	if len(res.Objects) > 0 {
		var objects []any
		for _, o := range res.Objects {
			objects = append(objects, o)
		}
		stream, err := encode(objects)
		if err != nil {
			return nil, err
		}
		values[okdp.ObjectsChart] = map[string]any{okdp.ObjectsValue: string(stream)}
		charts[okdp.ObjectsChart] = res.ObjectsSource
	}
	// The rendered dependencies of Chart.yaml, in its order.
	c := res.Chart
	c.Dependencies = nil
	listed := map[string]bool{}
	for _, d := range res.Chart.Dependencies {
		listed[d.Key()] = true
		if _, ok := values[d.Key()]; ok {
			c.Dependencies = append(c.Dependencies, d)
		}
	}
	// Then the compiler's own charts, by key.
	for _, key := range slices.Sorted(maps.Keys(values)) {
		if listed[key] {
			continue
		}
		ref := charts[key]
		d := chart.Dependency{Name: ref.Chart, Repository: ref.Repository, Version: ref.Version}
		if key != ref.Chart {
			d.Alias = key
		}
		c.Dependencies = append(c.Dependencies, d)
	}
	docs := map[string][]any{"Chart.yaml": {c}, ValuesFile: {values}}
	if i := res.Info; i.URL != "" || i.Usage != "" || len(i.Provides) > 0 {
		docs[InfoFile] = []any{i}
	}
	files := map[string][]byte{}
	for name, vs := range docs {
		b, err := encode(vs)
		if err != nil {
			return nil, err
		}
		if name == ValuesFile {
			b = append([]byte(target.Generated), b...)
		}
		files[name] = b
	}
	return files, nil
}

// encode: vs as a YAML stream, one document each, indented by 2 spaces.
func encode(vs []any) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	for _, v := range vs {
		if err := enc.Encode(v); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}
