// Package compiler compiles an OKDP service instance into the values of its
// upstream charts. A package (Chart.yaml, okdp.yaml, values.cel.yaml or
// values.yaml, its parameters and OKDP's defaults for its charts) is read
// from any fs.FS (LoadDefinition), compiled for one instance (Compile), and
// written as the files of okdp-compile (Write):
//
//	def, err := compiler.LoadDefinition(os.DirFS("db/catalog/myservice/1.0.0"))
//	res, err := compiler.Compile(context.Background(), def, compiler.Inputs{
//		Charts:    os.DirFS("charts"),
//		Contracts: os.DirFS("contracts"),
//		Context:   []compiler.ContextLayer{{Values: platformValues}, {Level: 2, Values: instanceConnections}},
//		Values:    []map[string]any{instanceValues},
//		Release:   "demo-myservice",
//		Namespace: "demo",
//	})
//	files, err := compiler.Write(res) // -o <dir>
//
// A package is a Helm chart: each dependency of its Chart.yaml is the
// upstream chart that renders the values of its key (repository, chart,
// version), and Result.Charts maps every compiled chart to it: a deployer
// renders the upstream chart from its repository. The compiler reads only
// its defaults and schema, from the chart cache (Inputs.Charts). Write gives
// the instance as one chart, an umbrella over these upstream charts, its
// values, and the instances it depends on.
//
// Everything is read through fs.FS and in-memory values, so a caller (a
// server, a CLI) can compile from a checkout, an embedded filesystem or a
// git tree without touching the disk.
//
// This package is the public API; the implementation is under internal/:
// OKDP's rules and the order of the stages (okdp), the umbrella a compile is
// written as (umbrella), the mapping language (compile), what the compiler
// reproduces of Helm (chart), and the values they share (value).
package compiler

import (
	"context"
	"io/fs"

	"github.com/okdp/okdp-compiler/internal/chart"
	"github.com/okdp/okdp-compiler/internal/okdp"
	"github.com/okdp/okdp-compiler/internal/umbrella"
)

// The types a caller names.
type (
	// Definition: a package, as LoadDefinition reads it: Chart.yaml,
	// okdp.yaml, its values file, values.cel.yaml or values.yaml, and the
	// package's other files; its warnings (Warnings).
	Definition = okdp.Definition
	// Inputs: what a compile reads besides the package.
	Inputs = okdp.Inputs
	// ContextLayer: a values layer of the context (Inputs.Context) at its
	// level: 0, the platform's, then each level adds to those below it.
	ContextLayer = okdp.ContextLayer
	// Result: what a compile produces, its warnings among them (Warnings),
	// which do not fail it.
	Result = okdp.Result
	// ChartRef: an upstream chart, pinned: what renders the values of a key.
	ChartRef = chart.Ref
	// ChartMetadata: a package's Chart.yaml (Definition.Chart,
	// Result.Chart).
	ChartMetadata = chart.Metadata
)

// The files Write gives besides Chart.yaml.
const (
	// UmbrellaValues: the umbrella's values, given to Helm as user values
	// (helm -f, Argo CD valueFiles, Flux valuesFrom).
	UmbrellaValues = umbrella.ValuesFile
	// DependenciesFile: Result.Dependencies as a YAML list, [] when there is
	// none, for a deployer that orders the releases (Flux dependsOn, Argo CD
	// sync waves).
	DependenciesFile = umbrella.DependenciesFile
)

// LoadDefinition reads the package at the root of fsys: Chart.yaml, then
// okdp.yaml and its values file, values.cel.yaml or values.yaml, when it
// has them, checked; each compile reads its other files. A values.yaml
// that holds expressions is read all the same, with a warning
// (Definition.Warnings, then Result.Warnings).
func LoadDefinition(fsys fs.FS) (*Definition, error) {
	return okdp.LoadDefinition(fsys)
}

// Compile runs the mapping of def on one instance, cancelled with ctx
// (between stages, and within the evaluation of an expression). It is safe
// for concurrent use: it keeps no state between calls.
func Compile(ctx context.Context, def *Definition, in Inputs) (*Result, error) {
	return okdp.Compile(ctx, def, in)
}

// Write: what `okdp-compile -o <dir>` writes for res, file name -> content:
// Chart.yaml, the instance as one chart, the package's Chart.yaml with the
// dependencies compiled, an umbrella over the upstream charts of its keys;
// UmbrellaValues, the final values of each key under the key; and
// DependenciesFile. res is what Compile returned: Write does not check it
// again. Every file is built before Write returns.
func Write(res *Result) (map[string][]byte, error) {
	return umbrella.Write(res)
}

// ParseValues: a values file as Helm 3 reads it (its first document, YAML
// 1.1, string keys, every number a float64), for Inputs.Context and
// Inputs.Values.
func ParseValues(b []byte) (map[string]any, error) {
	return chart.ParseValues(b)
}
