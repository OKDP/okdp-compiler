// Package chart is what the compiler reproduces of Helm and needs of the
// upstream charts: values read as Helm does, a package's parameters
// coalesced over its schema's defaults (values.go) and checked against a
// schema (schema.go, parameters.go), the compiler's own
// checks of values (check.go), a package's Chart.yaml (metadata.go), its
// dependencies with what okdp.yaml says of them (spec.go, paths.go), and
// the chart references (ref.go). It knows neither the mapping language nor
// OKDP.
//
// Values files are read as Helm 3 reads them: only the first YAML document
// is decoded (Helm 4 merges the later ones), as YAML 1.1 (y, yes, on are
// booleans) and through JSON (string keys, every number a float64).
//
// Helm's own functions are not imported: helm.sh/helm/v4 pulls in
// client-go, and a newer Go and Kubernetes than the programs that embed
// this package have. Its coalescing is reproduced from helm v4.3.0
// (pkg/chart/common/util/coalesce.go). The compiler reads no upstream
// chart: the checks against them, with Helm's own functions, are the
// host's.
package chart

import (
	"fmt"
	"io/fs"

	sigsyaml "sigs.k8s.io/yaml"

	"github.com/okdp/okdp-compiler/value"
)

// M is a values map (value.M).
type M = value.M

// ReadValues reads and parses the values file name of fsys.
func ReadValues(fsys fs.FS, name string) (M, error) {
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

// ParseValues decodes a values file as Helm 3 reads it (see the package
// doc). Callers use it to build Inputs.Values.
func ParseValues(b []byte) (M, error) {
	v := M{}
	if err := sigsyaml.Unmarshal(b, &v); err != nil {
		return nil, fmt.Errorf("cannot unmarshal yaml document: %w", err)
	}
	return v, nil
}

// coalesceValues is Helm's CoalesceValues for a chart without
// dependencies: the values the chart's templates see, user (the merged -f
// files) over the chart's defaults. A user null deletes the default, and
// stays where there is none. A null default is dropped (Helm 4). When a
// value and its default are not both maps, the value wins. Neither
// argument is changed.
func coalesceValues(defaults, user M) M {
	return coalesce(defaults, user, true)
}

// coalesce is coalesceValues at any depth. Below the top level (top is
// false), a user null over a null default stays, as in Helm.
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
