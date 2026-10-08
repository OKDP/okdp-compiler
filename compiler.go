// Package compiler compiles an OKDP service instance into the values of its
// upstream charts: the public API of okdp-compiler. A package (service.yaml,
// values.cel.yaml, its parameters and OKDP's defaults for its charts) is
// read from any fs.FS (LoadDefinition), compiled for one instance
// (Compile), and written as the files of okdp-compile (Write):
//
//	def, err := compiler.LoadDefinition(os.DirFS("packages/myservice"))
//	res, err := compiler.Compile(def, compiler.Inputs{
//		Charts:    os.DirFS("charts"),
//		Contracts: os.DirFS("contracts"),
//		Platform:  []map[string]any{platformValues},
//		Values:    []map[string]any{instanceValues},
//		Release:   "demo-myservice",
//		Namespace: "demo",
//	})
//	files, err := compiler.Write(res, compiler.Output{Install: true}) // -o <dir> --mkinstall
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
	// layers, Platform, Project and Values, may hold Go values of other
	// types than map[string]any and []any (map[string]string, []string,
	// int32, a struct by its json tags): each is read as the values it would
	// be once written to a values file and read by Helm; what values cannot
	// hold (a func, a NaN) fails the compile, its layer and path named.
	Inputs = okdp.Inputs
	// Result: what a compile produces.
	Result = okdp.Result
	// Dependency: an instance of the namespace a connection reference points
	// to.
	Dependency = okdp.Dependency
	// Option: a setting of CompileContext (WithCostLimit,
	// WithoutObjectsChart).
	Option = okdp.Option
	// Output: the files Write gives for a result, as the flags of
	// okdp-compile choose them (--umbrella, --mkinstall, --mkdag,
	// --indent).
	Output = fsio.Output
)

// The chart keys the compiler adds: OKDP's own objects, and the main OAuth
// client (the suffix of the others).
const (
	ObjectsChart = okdp.ObjectsChart
	DcrChart     = okdp.DcrChart
)

// DefaultIndent: the indentation of the YAML files Write gives, in spaces,
// unless Output.Indent sets another.
const DefaultIndent = fsio.DefaultIndent

// DcrSource: the chart that registers an OAuth client in dcr mode, unless
// the platform names another (global.okdp.charts.oidcDcr). The platform
// names it, not the caller: this is the default the compiler writes values
// for.
func DcrSource() ChartRef {
	return okdp.DcrSource
}

// LoadDefinition reads the package at the root of fsys: service.yaml,
// checked, and values.cel.yaml; each compile reads its other files.
func LoadDefinition(fsys fs.FS) (*Definition, error) {
	return fsio.LoadDefinition(fsys)
}

// ParseValues: a values file as Helm reads it (YAML 1.1, every document of
// the file merged in order, string keys, every number a float64), for
// Inputs.Platform, Inputs.Project and Inputs.Values.
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

// Write: what `okdp-compile -o <dir>` writes for res, file name -> content,
// with the files o asks for:
//
//   - the final values of each chart, <key>.values.yaml, each naming the
//     upstream chart that renders it; or, with Umbrella (--umbrella), the
//     instance as one chart: Chart.yaml, named and versioned after
//     service.yaml (Result.Service, Result.Version), an umbrella over the
//     upstream charts of its keys (aliased to the key when it is not the
//     chart's name), and UmbrellaValues, the final values of each key under
//     the key;
//   - with Install (--mkinstall), InstallScript, a shell script to write
//     executable: `helm upgrade --install` of each chart from its upstream
//     chart (Result.Charts) with its values file, in Result.Namespace, the
//     release <Result.Release>-<key> (a namespace holds one release of a
//     name), the OAuth clients' charts first (Result.ClientCharts); with
//     Umbrella, of the umbrella chart as one Helm release, Result.Release,
//     its dependencies built in a temporary copy;
//   - with Dependencies (--mkdag), DependenciesFile.
//
// The YAML files are indented by o.Indent spaces, 2 to 9 (0:
// DefaultIndent). Every file is built before Write returns: one that fails
// leaves the caller nothing to write.
func Write(res *Result, o Output) (map[string][]byte, error) {
	return fsio.Write(res, o)
}

// IsOutputFile: name is a file Write gives, with any Output. A caller that keeps the
// files of an instance in a directory, as okdp-compile does (-o), removes
// those of a previous compile that this one does not give, and refuses any
// other.
func IsOutputFile(name string) bool {
	return fsio.IsOutputFile(name)
}

// DependenciesFile: the file of Output.Dependencies, Result.Dependencies as
// a YAML list, for a deployer that orders the releases (Flux dependsOn,
// Argo CD sync waves).
const DependenciesFile = fsio.DependenciesFile

// InstallScript: the file of Output.Install, to write executable.
const InstallScript = fsio.InstallScript

// UmbrellaValues: the file of Output.Umbrella holding the instance's
// values, given to Helm as user values (helm -f, Argo CD valueFiles, Flux
// valuesFrom).
const UmbrellaValues = fsio.UmbrellaValues
