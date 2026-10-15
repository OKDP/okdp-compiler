// Package chart is what the compiler reproduces of Helm and needs of the
// upstream charts: values read, merged and coalesced as Helm does
// (values.go), checked against a values.schema.json (schema.go), the chart
// references and the chart cache of the checks (ref.go, cache.go), a
// package's Chart.yaml (metadata.go) and its dependencies, with okdp.yaml's
// defaults and upstream (spec.go, paths.go), a values schema, its defaults
// and the parameters of an instance (parameters.go), the final values of
// each chart key and their check (merge.go). It knows neither the mapping
// language nor OKDP.
//
// Values: reading YAML as Helm values, and Helm's merge rules.
//
// Values files are read as Helm 3 reads them: the first YAML document of the
// file decoded by sigs.k8s.io/yaml (YAML 1.1: y, yes, on... are booleans;
// through JSON: string keys, every number a float64); Helm 4 merges the
// later documents, which are ignored. The layers are merged as Helm merges
// its -f files (MergeMaps); the parameters, and the final values a check
// reads, are coalesced over their defaults as Helm does (CoalesceValues): a
// null deletes a default, and is kept where there is no default. Helm's own
// functions are not imported: helm.sh/helm/v4
// pulls in client-go and a newer Go and Kubernetes than the programs that
// embed this package, for about a hundred lines. They are reproduced from
// helm v4.3.0 (pkg/chart/common/util/coalesce.go), and testdata/helm holds
// what `helm template` computes on each case.
package chart

import (
	"fmt"
	"io/fs"

	sigsyaml "sigs.k8s.io/yaml"

	"github.com/okdp/okdp-compiler/internal/value"
)

// M: a values map (value.M).
type M = value.M

// readValues: a values file of fsys, read as Helm reads values files.
func readValues(fsys fs.FS, name string) (M, error) {
	b, err := fs.ReadFile(fsys, name)
	if err != nil {
		return nil, err
	}
	v, err := ParseValues(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return v, nil
}

// ParseValues: a values file as Helm 3 reads it: its first YAML document,
// decoded by sigs.k8s.io/yaml (YAML 1.1, string keys, every number a
// float64), for Inputs.Values.
func ParseValues(b []byte) (M, error) {
	v := M{}
	if err := sigsyaml.Unmarshal(b, &v); err != nil {
		return nil, fmt.Errorf("cannot unmarshal yaml document: %w", err)
	}
	return v, nil
}

// coalesceValues: Helm's CoalesceValues for a chart without dependencies,
// the values its templates see: user values (the merged -f files) over the
// chart defaults. A user null deletes the default (the key is gone); a null
// where the chart has no default stays; null defaults are dropped (Helm 4).
// When a value and its default are not both maps, the value wins. Neither
// argument is changed.
func coalesceValues(defaults, user M) M {
	return coalesce(defaults, user, true)
}

// coalesce: coalesceValues at any depth, Helm's coalesceTablesFullKey and
// cleanNilValues below the top level (top false), where a user null over a
// null default stays.
func coalesce(defaults, user M, top bool) M {
	out := value.AsMap(value.Copy(user))
	for k, d := range defaults {
		u, ok := out[k]
		um, uMap := u.(M)
		switch dm, dMap := d.(M); {
		case dMap && (!ok || uMap):
			out[k] = coalesce(dm, um, false)
		case !ok && d != nil:
			out[k] = value.Copy(d)
		case ok && u == nil && (d != nil || top):
			delete(out, k)
		}
	}
	return out
}
