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
// renders the upstream chart from its repository, the final values over its
// defaults. A compile reads no chart; with a chart cache (Inputs.Charts), it
// checks the final values against each chart's defaults and schema. Write gives
// the instance as one chart, an umbrella over these upstream charts, its
// values and OKDP's own objects, and what the instance publishes, whose
// outputs the other instances of the namespace reference by name
// (Inputs.Published); the instances it depends on are for a deployer
// (Result.Dependencies). CompileAll compiles instances together, each with
// what the others publish (a tree's, LoadRepository), NewGraph orders
// their releases, the dependencies checked, and a Target writes them for
// a deployer.
//
// Everything is read through fs.FS and in-memory values, so a caller (a
// server, a CLI) can compile from a checkout, an embedded filesystem or a
// git tree without touching the disk.
//
// This package is the public API; the implementation is under internal/:
// OKDP's rules and the order of the stages (okdp), the tree of a
// deployment read (repository), the deployment graph of compiled
// instances (graph), the formats compiled instances are written in
// (target: helm, the umbrella of Write, flux, argocd), the mapping
// language (compile), what the compiler reproduces of Helm (chart), and
// the values they share (value).
package compiler

import (
	"context"
	"io/fs"

	"github.com/okdp/okdp-compiler/internal/chart"
	"github.com/okdp/okdp-compiler/internal/graph"
	"github.com/okdp/okdp-compiler/internal/okdp"
	"github.com/okdp/okdp-compiler/internal/repository"
	"github.com/okdp/okdp-compiler/internal/target"
	"github.com/okdp/okdp-compiler/internal/target/argocd"
	"github.com/okdp/okdp-compiler/internal/target/flux"
	"github.com/okdp/okdp-compiler/internal/target/helm"
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
	// Info: what an instance publishes (Result.Info), as
	// InfoFile holds it: its URL, its usage, the connections it
	// provides.
	Info = okdp.Info
	// Output: a connection an instance provides (Info.Provides).
	Output = okdp.Output
	// SecretRef: the Secret of an output's credentials, by name
	// (Output.SecretRef).
	SecretRef = okdp.SecretRef
	// Dependency: an instance a connection reference points to, by one of
	// its outputs (Result.Dependencies), what a deployer orders the releases
	// with.
	Dependency = okdp.Dependency
	// Instance: an instance CompileAll compiles, its package and its
	// inputs, Published left out.
	Instance = okdp.Instance
	// Outcome: the compile of one instance of CompileAll: its result, nil
	// when it is refused, and why.
	Outcome = okdp.Outcome
	// Repository: a part of a tree LoadRepository reads: its name in
	// messages and its files.
	Repository = repository.Repository
	// Graph: the deployment graph of compiled instances (NewGraph): what
	// each release waits for (DependsOn), its Argo CD sync wave (Wave).
	Graph = graph.Graph
	// GraphNode: a release of the graph, in its namespace.
	GraphNode = graph.Node
	// GraphError: a problem of the graph, at the release it is found at.
	GraphError = graph.Error
	// Target: an output format of compiled instances (Helm, Flux, ArgoCD):
	// their files, path -> content, under projects/<namespace>/, each
	// instance's umbrella chart in instances/<release>/.
	Target = target.Target
	// Helm: the helm target: the umbrella chart of each instance, the files
	// of Write.
	Helm = helm.Target
	// Flux: the flux target: the helm target's files, each instance's Flux
	// HelmRelease, waiting for the releases it depends on, and the
	// Kustomizations; okdp-gitops' format. Its fields are its options.
	Flux = flux.Target
	// ArgoCD: the argocd target: the helm target's files, and each
	// instance's Argo CD Application, its sync wave after those of the
	// releases it depends on; okdp-gitops' format. Its fields are its
	// options.
	ArgoCD = argocd.Target
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
	UmbrellaValues = helm.ValuesFile
	// InfoFile: Result.Info, what the instance publishes (url,
	// usage, provides), written when it publishes something, for a control
	// plane or a console that reads the deployments repository.
	InfoFile = helm.InfoFile
	// ObjectsFile: Result.Objects as a YAML stream, one document per object,
	// written when there are objects: plain manifests, which ObjectsTemplate
	// renders as written in the umbrella's release.
	ObjectsFile = helm.ObjectsFile
	// ObjectsTemplate: the umbrella's template of ObjectsFile, a path in
	// the chart, written with it.
	ObjectsTemplate = helm.ObjectsTemplate
)

// DefaultFluxSource: the GitRepository of the flux target when
// Flux.Source is empty, okdp-gitops'.
const DefaultFluxSource = flux.DefaultSource

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

// CompileAll compiles instances together: those of one namespace each
// after the instances whose outputs it references, with what they publish
// (Inputs.Published, set by CompileAll). An output two instances publish
// refuses both, and the instances whose references resolved to it.
// Outcomes in the order of instances, which changes none of them.
func CompileAll(ctx context.Context, instances []Instance) []Outcome {
	return okdp.CompileAll(ctx, instances)
}

// LoadRepository: every instance of the tree that repos make together
// (docs/01_usage.md, repository layout), by project then instance, as
// CompileAll compiles them: each part of the tree in one repository
// (platform.yaml, catalog/<service>/, projects/<project>/), a contract in
// several with the same bytes, so that the order of repos changes nothing.
// Every problem at once, each naming its repository and path.
func LoadRepository(repos []Repository) ([]Instance, error) {
	return repository.Load(repos)
}

// NewGraph: the deployment graph of results, the compiled instances of
// their namespaces: a release waits for the releases its connection
// references point to (Result.Dependencies). Every problem at once, each
// a *GraphError: a dependency on a release no result has, or on one that
// does not provide what is referenced; a cycle; a release given twice.
func NewGraph(results []*Result) (*Graph, error) {
	return graph.New(results)
}

// Write: what `okdp-compile -o <dir>` writes for res, path in the
// directory -> content: Chart.yaml, the instance as one chart, the
// package's Chart.yaml with the dependencies compiled, an umbrella over the
// upstream charts of its keys; UmbrellaValues, the final values of each key
// under the key; InfoFile, when the instance publishes a URL, a usage
// or a connection; and, when res has objects, ObjectsFile and
// ObjectsTemplate. res is what Compile returned: Write does not check it
// again. Every file is built before Write returns.
func Write(res *Result) (map[string][]byte, error) {
	return helm.Write(res)
}

// ParseValues: a values file as Helm 3 reads it (its first document, YAML
// 1.1, string keys, every number a float64), for Inputs.Context and
// Inputs.Values.
func ParseValues(b []byte) (map[string]any, error) {
	return chart.ParseValues(b)
}
