// Package compiler compiles an OKDP service instance into the values of its
// upstream charts: the public API of okdp-compiler. A package (service.yaml,
// mapping.cel.yaml, its parameters and base values) is read from any fs.FS
// (LoadDefinition), compiled for one instance (Compile), and written as
// files (Files, Document):
//
//	def, err := compiler.LoadDefinition(os.DirFS("packages/myservice"))
//	res, err := compiler.Compile(def, compiler.Inputs{
//		Charts:    os.DirFS("charts"),
//		Instance:  os.DirFS("instances/demo/myservice"),
//		Contracts: os.DirFS("contracts"),
//		Platform:  []map[string]any{platformValues},
//		Values:    []map[string]any{instanceValues},
//		Release:   "demo-myservice",
//		Namespace: "demo",
//	})
//	files, err := compiler.Files(res, compiler.DefaultIndent)
//
// The implementation is under internal/: the mapping language (compile),
// what the compiler reproduces of Helm (chart), OKDP's rules and the order
// of the stages (okdp), the edges (fsio), and the values they share (value).
package compiler

import (
	"context"
	"io/fs"

	"github.com/okdp/okdp-compiler/internal/chart"
	"github.com/okdp/okdp-compiler/internal/fsio"
	"github.com/okdp/okdp-compiler/internal/okdp"
)

// The types a caller names: a package, a compile's inputs and its result.
type (
	// Definition: a package, as LoadDefinition reads it: service.yaml, its
	// mapping, and the package's other files.
	Definition = okdp.Definition
	// Service: service.yaml.
	Service = okdp.Service
	// Requirement: an entry of require in service.yaml, a platform value the
	// service needs: in every case, or when its condition (If) holds.
	Requirement = okdp.Requirement
	// ChartSpec: a chart of service.yaml, by key.
	ChartSpec = chart.Spec
	// Upstream: the instance upstream values a chart of service.yaml accepts.
	Upstream = chart.Upstream
	// ChartRef: an upstream chart, pinned: what renders the values of a key.
	ChartRef = chart.Ref
	// Inputs: what one compile reads besides the package. Its values
	// layers, Platform and Values, may hold Go values of other types than
	// map[string]any and []any (map[string]string, []string, int32, a
	// struct by its json tags): each is read as the values it would be once
	// written to a values file and read by Helm; what values cannot hold (a
	// func, a NaN) fails the compile, its layer and path named.
	Inputs = okdp.Inputs
	// Result: what a compile produces.
	Result = okdp.Result
	// Dependency: an instance of the namespace a connection reference points
	// to.
	Dependency = okdp.Dependency
	// Option: a setting of CompileContext (WithCostLimit,
	// WithoutObjectsChart).
	Option = okdp.Option
)

// The chart keys the compiler adds: OKDP's own objects, and the main OAuth
// client (the suffix of the others).
const (
	ObjectsChart = okdp.ObjectsChart
	DcrChart     = okdp.DcrChart
)

// DefaultIndent: the indentation of the YAML files Files and Document write,
// in spaces.
const DefaultIndent = fsio.DefaultIndent

// DcrSource: the chart that registers an OAuth client in dcr mode, unless
// the platform names another (global.okdp.charts.oidcDcr). The platform
// names it, not the caller: this is the default the compiler writes values
// for.
func DcrSource() ChartRef {
	return okdp.DcrSource
}

// LoadDefinition reads the package at the root of fsys: service.yaml,
// checked, and mapping.cel.yaml; each compile reads its other files.
func LoadDefinition(fsys fs.FS) (*Definition, error) {
	return fsio.LoadDefinition(fsys)
}

// ParseValues: a values file as Helm reads it (YAML 1.1, every document of
// the file merged in order, string keys, every number a float64), for
// Inputs.Platform and Inputs.Values.
func ParseValues(b []byte) (map[string]any, error) {
	return chart.ParseValues(b)
}

// Compile runs the mapping of def on one instance: CompileContext without a
// context or options. It is safe for concurrent use: it keeps no state
// between calls.
func Compile(def *Definition, in Inputs) (*Result, error) {
	return okdp.Compile(def, in)
}

// CompileContext runs the mapping of def on one instance, cancelled with ctx
// (between stages, and within the evaluation of an expression), with the
// options opts. It is safe for concurrent use: it keeps no state between
// calls.
func CompileContext(ctx context.Context, def *Definition, in Inputs, opts ...Option) (*Result, error) {
	return okdp.CompileContext(ctx, def, in, opts...)
}

// WithCostLimit: the total CEL cost the expressions of one compile may use,
// the conditions of require, the mapping's and the OAuth client templates'
// (0, the default: no limit).
// Past the limit, the compile fails: a server bounds what a package's
// expressions compute over the values an instance gives.
func WithCostLimit(limit uint64) Option {
	return okdp.WithCostLimit(limit)
}

// WithoutObjectsChart: no okdp chart (ObjectsChart) in the result, for a
// caller that deploys OKDP's own objects (Result.Objects) otherwise;
// global.okdp.charts.objects is then not required.
func WithoutObjectsChart() Option {
	return okdp.WithoutObjectsChart()
}

// Files: what `okdp-compile --emit-dir` writes, file name -> content: the
// final values of each chart (<key>.values.yaml), the chart index
// (okdp.charts.yaml) and the dependencies (okdp.dependencies.yaml),
// indented by indent spaces (2 to 9).
func Files(res *Result, indent int) (map[string][]byte, error) {
	return fsio.Files(res, indent)
}

// UmbrellaValues: the file of Umbrella holding the instance's values, given
// to Helm as user values (helm -f, Argo CD valueFiles, Flux valuesFrom).
const UmbrellaValues = fsio.UmbrellaValues

// Umbrella: what `okdp-compile --umbrella` writes, file name -> content: the
// instance as one chart, Chart.yaml, an umbrella over the upstream charts of
// its keys (aliased to the key when it is not the chart's name), and
// UmbrellaValues, the final values of each key under the key; indented by
// indent spaces (2 to 9).
func Umbrella(def *Definition, res *Result, indent int) (map[string][]byte, error) {
	return fsio.Umbrella(def, res, indent)
}

// Document: what `okdp-compile --out` writes: the chart index, the
// dependencies, the objects and the computed values, in one YAML
// document, indented by indent spaces (2 to 9).
func Document(res *Result, indent int) ([]byte, error) {
	return fsio.Document(res, indent)
}
