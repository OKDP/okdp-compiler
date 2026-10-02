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
//		Values:    []map[string]any{platformValues, instanceValues},
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
	// ChartSpec: a chart of service.yaml, by key.
	ChartSpec = chart.Spec
	// Upstream: the instance upstream values a chart of service.yaml accepts.
	Upstream = chart.Upstream
	// ChartRef: an upstream chart, pinned: what renders the values of a key.
	ChartRef = chart.Ref
	// Inputs: what one compile reads besides the package.
	Inputs = okdp.Inputs
	// Result: what a compile produces.
	Result = okdp.Result
	// Dependency: an instance of the namespace a connection reference points
	// to.
	Dependency = okdp.Dependency
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
// the platform names another (global.okdp.charts.oidcDcr).
var DcrSource = okdp.DcrSource

// LoadDefinition reads the package at the root of fsys: service.yaml,
// checked, and mapping.cel.yaml; each compile reads its other files.
func LoadDefinition(fsys fs.FS) (*Definition, error) {
	return fsio.LoadDefinition(fsys)
}

// ParseValues: a values file as Helm reads it (YAML 1.1, every document of
// the file merged in order, string keys, every number a float64), for
// Inputs.Values.
func ParseValues(b []byte) (map[string]any, error) {
	return chart.ParseValues(b)
}

// Compile runs the mapping of def on one instance. It is safe for
// concurrent use: it keeps no state between calls.
func Compile(def *Definition, in Inputs) (*Result, error) {
	return okdp.Compile(def, in)
}

// Files: what `okdp-compile --emit-dir` writes, file name -> content: the
// final values of each chart (<key>.values.yaml), the chart index
// (okdp.charts.yaml) and the dependencies (okdp.dependencies.yaml),
// indented by indent spaces (2 to 9).
func Files(res *Result, indent int) (map[string][]byte, error) {
	return fsio.Files(res, indent)
}

// Document: what `okdp-compile --out` writes: the chart index, the
// dependencies, the objects and the computed values, in one YAML
// document, indented by indent spaces (2 to 9).
func Document(res *Result, indent int) ([]byte, error) {
	return fsio.Document(res, indent)
}
